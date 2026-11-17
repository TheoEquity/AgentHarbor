import { NextResponse, type NextRequest } from "next/server";
import { z } from "zod";
import { resolveRemoteApiUrl } from "../config/runtime-urls";

const workspaceList = z.array(z.object({
  slug: z.string().regex(/^[a-z0-9][a-z0-9-]*$/),
}));
const previewCookies = new Set(["multica_auth", "multica_csrf", "multica_csrf_session"]);

function mayStartPreview(req: NextRequest): boolean {
  const rawOrigin = process.env.FRONTEND_ORIGIN;
  if (process.env.APP_ENV !== "development" || !rawOrigin || !URL.canParse(rawOrigin)) return false;

  // Next's Proxy URL can contain the container listener port. Host carries
  // the origin the browser requested, without relying on forwarding headers.
  const origin = new URL(rawOrigin);
  return req.headers.get("host") === origin.host
    && req.nextUrl.protocol === origin.protocol
    && ["localhost", "127.0.0.1", "[::1]"].includes(origin.hostname)
    && req.headers.get("sec-fetch-site") !== "cross-site";
}

function fetchBackend(api: string, path: string, init?: RequestInit) {
  return fetch(`${api}${path}`, {
    ...init,
    cache: "no-store",
    redirect: "error",
    signal: AbortSignal.timeout(5000),
  });
}

async function startPreview(req: NextRequest, api: string): Promise<string[]> {
  const email = process.env.AGENTHARBOR_LOCAL_PREVIEW_EMAIL?.trim();
  const code = process.env.MULTICA_DEV_VERIFICATION_CODE?.trim();
  if (!email || !code || !/^\d{6}$/.test(code) || !mayStartPreview(req)) return [];

  const postJson = (path: string, body: Record<string, string>) =>
    fetchBackend(api, path, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });

  const sent = await postJson("/auth/send-code", { email });
  if (!sent.ok) return [];
  const verified = await postJson("/auth/verify-code", { email, code });
  if (!verified.ok) return [];
  return verified.headers.getSetCookie().filter((cookie) => previewCookies.has(cookie.split("=", 1)[0] ?? ""));
}

async function readWorkspaces(api: string, cookie: string) {
  const response = await fetchBackend(api, "/api/workspaces", { headers: { cookie } });
  if (response.status === 401) return null;
  if (!response.ok) throw new Error("Workspace lookup failed");
  return workspaceList.parse(await response.json());
}

function redirectHome(req: NextRequest, path: string, setCookies: string[] = []) {
  const url = req.nextUrl.clone();
  url.pathname = path;
  url.search = "";
  if (path === "/login") url.searchParams.set("next", "/");

  const response = NextResponse.redirect(url);
  response.headers.set("Cache-Control", "no-store");
  for (const cookie of setCookies) response.headers.append("Set-Cookie", cookie);
  return response;
}

export async function agentHarborHome(req: NextRequest) {
  const api = resolveRemoteApiUrl(process.env);
  if (!api) return redirectHome(req, "/login");

  try {
    const existing = req.cookies.has("multica_auth")
      ? await readWorkspaces(api, req.headers.get("cookie") ?? "")
      : null;
    const cookies = existing ? [] : await startPreview(req, api);
    const preview = cookies.some((cookie) => cookie.startsWith("multica_auth="));
    const workspaces = existing ?? (preview
      ? await readWorkspaces(api, cookies.map((cookie) => cookie.split(";")[0]).join("; "))
      : null);

    if (!workspaces) return redirectHome(req, "/login");
    const lastSlug = req.cookies.get("last_workspace_slug")?.value;
    const workspace = workspaces.find(({ slug }) => slug === lastSlug) ?? workspaces[0];
    return redirectHome(req, workspace ? `/${workspace.slug}/agent-tasks` : "/workspaces/new", cookies);
  } catch {
    return new NextResponse("AgentHarbor workspace is temporarily unavailable.", {
      status: 503,
      headers: { "Cache-Control": "no-store" },
    });
  }
}
