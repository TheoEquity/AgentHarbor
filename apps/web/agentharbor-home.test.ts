// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";
import { proxy } from "./proxy";

const local = "http://localhost:18245";
const backend = "http://backend:8080";

function enableHome() {
  vi.stubEnv("AGENTHARBOR_HOME", "agent-tasks");
  vi.stubEnv("AGENTHARBOR_LOCAL_PREVIEW_EMAIL", "preview@example.test");
  vi.stubEnv("MULTICA_DEV_VERIFICATION_CODE", "123456");
  vi.stubEnv("APP_ENV", "development");
  vi.stubEnv("FRONTEND_ORIGIN", local);
  vi.stubEnv("REMOTE_API_URL", backend);
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

describe("AgentHarbor web home", () => {
  it("enters the workspace task page using a backend session from local preview", async () => {
    enableHome();
    const fetchBackend = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(null, { status: 200 }))
      .mockResolvedValueOnce(new Response(null, {
        headers: { "set-cookie": "multica_auth=preview-session; Path=/; HttpOnly" },
      }))
      .mockResolvedValueOnce(Response.json([{ slug: "local-workspace" }]));
    vi.stubGlobal("fetch", fetchBackend);

    const response = await proxy(new NextRequest("http://localhost:3000/", {
      headers: { host: "localhost:18245" },
    }));

    expect(new URL(response.headers.get("location") ?? "", local).pathname).toBe(
      "/local-workspace/agent-tasks",
    );
    expect(response.headers.getSetCookie()).toContain(
      "multica_auth=preview-session; Path=/; HttpOnly",
    );
    expect(fetchBackend.mock.calls.map(([url]) => String(url))).toEqual([
      `${backend}/auth/send-code`,
      `${backend}/auth/verify-code`,
      `${backend}/api/workspaces`,
    ]);
  });

  it.each(["https://public.example.test", "http://localhost:18246"])(
    "requires normal login on %s",
    async (origin) => {
      enableHome();
      const fetchBackend = vi.fn();
      vi.stubGlobal("fetch", fetchBackend);

      const response = await proxy(new NextRequest(origin));

      expect(response.headers.get("location")).toBe(`${origin}/login?next=%2F`);
      expect(response.headers.getSetCookie()).toEqual([]);
      expect(fetchBackend).not.toHaveBeenCalled();
    },
  );

  it("refuses preview when the request Host differs from the configured local origin", async () => {
    enableHome();
    const fetchBackend = vi.fn();
    vi.stubGlobal("fetch", fetchBackend);

    const response = await proxy(new NextRequest(local, {
      headers: { host: "public.example.test", "x-forwarded-host": "localhost:18245" },
    }));

    expect(new URL(response.headers.get("location") ?? "", local).pathname).toBe("/login");
    expect(fetchBackend).not.toHaveBeenCalled();
  });

  it("prefers the last workspace only when it belongs to the current session", async () => {
    enableHome();
    const fetchBackend = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json([{ slug: "primary" }, { slug: "recent" }]));
    vi.stubGlobal("fetch", fetchBackend);

    const response = await proxy(new NextRequest(local, {
      headers: { cookie: "multica_auth=existing-session; last_workspace_slug=recent" },
    }));

    expect(response.headers.get("location")).toBe(`${local}/recent/agent-tasks`);
    expect(fetchBackend).toHaveBeenCalledTimes(1);
    expect(fetchBackend.mock.calls[0]?.[1]?.headers).toEqual({
      cookie: "multica_auth=existing-session; last_workspace_slug=recent",
    });
  });
});
