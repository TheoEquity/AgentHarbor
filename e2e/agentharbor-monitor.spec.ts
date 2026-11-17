import { randomUUID } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";

const monitorPath = "/api/agentharbor/monitor";

type Turn = {
  session_id: string;
  turn_id: string;
  title: string;
  status: "running" | "completed" | "aborted";
  started_at: string;
  ended_at?: string;
  historical?: boolean;
};

async function openTasks(page: Page, client: TestApiClient, slug: string) {
  const token = client.getToken();
  if (!token) throw new Error("No token for AgentHarbor browser session");
  await page.addInitScript((value) => {
    localStorage.setItem("multica_token", value);
    localStorage.setItem("multica:chat:isOpen", "false");
  }, token);
  await page.goto(`/${slug}/agent-tasks`);
  await expect(page.getByRole("heading", { name: "Agent tasks" })).toBeVisible();
}

test("Codex monitor preserves turn identity and source state across the browser and API", async ({ page, browser, request }, testInfo) => {
  test.setTimeout(90_000);
  const apiBase = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
  const owner = new TestApiClient();
  const teammate = new TestApiClient();
  const suffix = randomUUID().slice(0, 8);
  const ownerEmail = `monitor-owner-${suffix}@multica.ai`;
  await owner.login(ownerEmail, "Monitor Owner");
  await owner.markUserOnboarded();
  const workspace = await owner.ensureWorkspace("Monitor E2E", `monitor-e2e-${suffix}`);
  const ownerHeaders = { Authorization: `Bearer ${owner.getToken()}`, "X-Workspace-ID": workspace.id };
  let patId: string | undefined;

  try {
    await teammate.login(`monitor-member-${suffix}@multica.ai`, "Monitor Teammate");
    await teammate.markUserOnboarded();
    await owner.addMemberByEmail(teammate.getEmail());
    const project = await owner.createProject("Monitor Project");
    const other = await owner.createProject("Other Monitor Project");
    const issued = await request.post(`${apiBase}/api/tokens`, {
      headers: ownerHeaders, data: { name: "Monitor E2E", expires_in_days: 1 },
    });
    expect(issued.status()).toBe(201);
    const pat = await issued.json();
    patId = pat.id;
    const syncHeaders = { ...ownerHeaders, Authorization: `Bearer ${pat.token}` };

    const laptop = { id: randomUUID(), name: "Monitor Laptop" };
    const desktop = { id: randomUUID(), name: "Monitor Desktop" };
    const session = randomUUID();
    const base = Date.now() - 300_000;
    const time = (offset: number) => new Date(base + offset * 1_000).toISOString();
    const running: Turn = {
      session_id: session, turn_id: "current", title: "Current turn", status: "running", started_at: time(120),
    };
    async function sync(turns: Turn[], machine = laptop, projectId = project.id, scan = "ok") {
      const response = await request.post(`${apiBase}${monitorPath}/sync`, {
        headers: syncHeaders, data: { project_id: projectId, machine, scan_status: scan, turns },
      });
      expect(response.status(), await response.text()).toBe(200);
      expect(await response.json()).toEqual({ accepted_count: turns.length });
    }

    const taskPath = `/${workspace.slug}/agent-tasks`;
    const refresh = page.getByRole("button", { name: "Refresh tasks" });
    const current = page.getByRole("row", { name: /^Current turn/ });
    const history = page.getByRole("tab", { name: "History" });
    const monitor = page.getByRole("tab", { name: "Monitor" });

    await test.step("a late old completion and a degraded scan preserve the latest running turn", async () => {
      await sync([running]);
      await sync([{
        ...running, turn_id: "old", title: "Late old turn", started_at: time(0), ended_at: time(30),
        status: "completed", historical: true,
      }]);
      await sync([], laptop, project.id, "degraded");
      await openTasks(page, owner, workspace.slug);
      await expect(current.getByRole("cell", { name: "Running", exact: true })).toBeVisible();
      await expect(current.getByRole("button", { name: "Mark as handled" })).toHaveCount(0);
      await expect(page.getByText("Late old turn", { exact: true })).toHaveCount(0);
      await expect(page.getByText("Last sync", { exact: true })).toBeVisible();
      await expect(page.getByText("Data may be out of date")).toBeVisible();
      await expect(current.getByRole("link", { name: "Open in Codex" })).toHaveAttribute("href", `codex://threads/${session}`);
    });

    await test.step("replayed starts do not overwrite an abort; handling is durable and personal", async () => {
      await sync([{ ...running, status: "aborted", ended_at: time(150) }]);
      await sync([running]);
      await refresh.click();
      await expect(current.getByRole("cell", { name: "Aborted", exact: true })).toBeVisible();
      await expect(page.getByText("Data may be out of date")).toHaveCount(0);
      await current.getByRole("button", { name: "Mark as handled" }).click();
      await expect(current).toHaveCount(0);

      const repeat = await request.post(`${apiBase}${monitorPath}/handle`, {
        headers: ownerHeaders, data: {
          project_id: project.id, machine_id: laptop.id, session_id: session, turn_id: running.turn_id,
        },
      });
      expect(repeat.status()).toBe(200);
      await page.reload();
      await expect(page.getByText("Last sync", { exact: true })).toBeVisible();
      await expect(current).toHaveCount(0);
      await history.click();
      await expect(current.getByText("Handled", { exact: true })).toBeVisible();
      await expect(page.getByRole("row", { name: /^Late old turn/ }).getByText("Handling status unknown")).toBeVisible();

      const freshContext = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
      try {
        const freshPage = await freshContext.newPage();
        await owner.login(ownerEmail, "Monitor Owner");
        await openTasks(freshPage, owner, workspace.slug);
        await expect(freshPage.getByText("Last sync", { exact: true })).toBeVisible();
        await expect(freshPage.getByRole("row", { name: /^Current turn/ })).toHaveCount(0);
      } finally {
        await freshContext.close();
      }

      const teammateContext = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
      try {
        const teammatePage = await teammateContext.newPage();
        await openTasks(teammatePage, teammate, workspace.slug);
        await expect(teammatePage.getByRole("row", { name: /^Current turn/ })
          .getByRole("button", { name: "Mark as handled" })).toBeVisible();
      } finally {
        await teammateContext.close();
      }

      await sync([{
        ...running, turn_id: "next", title: "Next turn", started_at: time(180), ended_at: time(210), status: "completed",
      }]);
      await monitor.click();
      await refresh.click();
      await expect(page.getByRole("row", { name: /^Next turn/ })
        .getByRole("button", { name: "Mark as handled" })).toBeVisible();
    });

    await test.step("project and machine filters, history pagination and failed refresh work with synced data", async () => {
      const nonUuidSession = "legacy-format-session";
      await sync([{
        ...running, session_id: nonUuidSession, turn_id: "desktop", title: "Desktop turn", started_at: time(180),
      }], desktop);
      await sync([{
        ...running, session_id: randomUUID(), turn_id: "other", title: "Other project turn",
      }], laptop, other.id);
      await sync(Array.from({ length: 51 }, (_, index) => ({
        session_id: randomUUID(),
        turn_id: `history-${index}`,
        title: `Historic fixture ${index}`,
        status: "completed",
        historical: true,
        started_at: time(index),
        ended_at: time(index + 1),
      })));

      await refresh.click();
      const desktopTurn = page.getByRole("row", { name: /^Desktop turn/ });
      const nextTurn = page.getByRole("row", { name: /^Next turn/ });
      await expect(desktopTurn.getByRole("cell", { name: desktop.name })).toBeVisible();
      await expect(nextTurn.getByRole("cell", { name: laptop.name })).toBeVisible();
      await expect(page.getByText("Other project turn", { exact: true })).toBeVisible();
      const screenshot = testInfo.outputPath("codex-monitor.png");
      await page.screenshot({ path: screenshot });
      await testInfo.attach("Codex monitor across two synthetic machines", { path: screenshot, contentType: "image/png" });

      await page.getByRole("combobox", { name: "Project" }).click();
      await page.getByRole("option", { name: project.title, exact: true }).click();
      await expect(page.getByText("Other project turn", { exact: true })).toHaveCount(0);
      await page.getByRole("combobox", { name: "Machine" }).click();
      await page.getByRole("option", { name: desktop.name, exact: true }).click();
      await expect(desktopTurn).toBeVisible();
      await expect(nextTurn).toHaveCount(0);
      await expect(desktopTurn.getByRole("link", { name: "Open in Codex" })).toHaveCount(0);

      await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
      await desktopTurn.getByRole("button", { name: "Copy session ID" }).click();
      await expect(desktopTurn.getByText("Session ID copied")).toBeVisible();
      expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(nonUuidSession);

      await page.getByRole("combobox", { name: "Machine" }).click();
      await page.getByRole("option", { name: laptop.name, exact: true }).click();
      await history.click();
      const oldest = page.getByRole("row", { name: /^Historic fixture 0 / });
      await expect(page.getByText("Page 1", { exact: true })).toBeVisible();
      await expect(oldest).toHaveCount(0);
      await page.getByRole("button", { name: "Next page" }).click();
      await expect(page.getByText("Page 2", { exact: true })).toBeVisible();
      await expect(oldest).toBeVisible();

      const listing = /\/api\/agentharbor\/monitor\?/;
      await page.route(listing, (route) => route.fulfill({ status: 503, json: { error: "Synthetic refresh outage" } }));
      try {
        await refresh.click();
        await expect(page.getByText("Unable to load Codex tasks")).toBeVisible({ timeout: 15_000 });
        await expect(page.getByText("Data may be out of date")).toBeVisible();
        await expect(oldest).toBeVisible();
      } finally {
        await page.unroute(listing);
      }
    });

    await test.step("legacy Stop remains outcome unknown and startup memory evidence still renders", async () => {
      const eventTypes = ["prompt_started", "memory_read", "turn_stopped"];
      const legacy = await request.post(`${apiBase}/api/agentharbor/sync`, {
        headers: syncHeaders, data: {
          machine: laptop,
          events: eventTypes.map((event_type, index) => ({
            event_id: randomUUID(), project_id: project.id, machine_id: laptop.id, session_id: session,
            turn_id: "legacy-hook", local_seq: index + 1, agent_type: "codex", event_type, occurred_at: time(index),
            evidence: event_type === "memory_read" ? JSON.stringify({
              trigger: "startup", fetched: true, emitted: true, context_emitted: false, entries: [], version: "",
            }) : "",
          })),
        },
      });
      expect(legacy.status(), await legacy.text()).toBe(200);
      await page.goto(taskPath);
      await page.getByRole("tab", { name: "Hook records" }).click();
      const legacyRow = page.getByRole("row", { name: /Turn ended · completion unknown/ });
      await expect(legacyRow).toBeVisible();
      await legacyRow.getByText("Startup memory", { exact: true }).click();
      await expect(legacyRow.getByText("No approved memory was returned.")).toBeVisible();
    });
  } finally {
    if (patId) await request.delete(`${apiBase}/api/tokens/${patId}`, { headers: ownerHeaders });
    await owner.cleanup();
    await owner.deleteWorkspace();
  }
});
