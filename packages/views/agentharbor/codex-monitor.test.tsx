import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@multica/core/api";
import type { AgentHarborMonitorTask } from "@multica/core/api/schemas";
import { copyText } from "@multica/ui/lib/clipboard";
import { renderWithI18n } from "../test/i18n";
import { AgentTasksPage } from "./agent-tasks-page";

vi.mock("@multica/core/api", () => ({
  api: {
    listProjects: vi.fn(),
    listAgentHarborMonitor: vi.fn(),
    handleAgentHarborMonitorTask: vi.fn(),
    getAgentHarborBark: vi.fn(),
    putAgentHarborBark: vi.fn(),
    testAgentHarborBark: vi.fn(),
  },
}));
vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useCurrentWorkspace: () => ({ id: "ws-1" }),
    useWorkspacePaths: () => actual.paths.workspace("acme"),
  };
});
vi.mock("../navigation", () => ({
  AppLink: ({ href, children }: { href: string; children: ReactNode }) =>
    <a href={href}>{children}</a>,
}));
vi.mock("@multica/ui/lib/clipboard", () => ({ copyText: vi.fn() }));

const now = "2026-11-13T10:00:00Z";
const sessionId = "019a0927-f85d-7766-a0dc-5f218ced525b";
const terminal: AgentHarborMonitorTask = {
  projectId: "project", projectName: "Harbor", machineId: "machine", machineName: "Jacobin",
  sessionId, turnId: "turn-1", title: "Fix the query", startedAt: now, endedAt: now,
  status: "completed", handled: false, historical: false,
};
const source = {
  projectId: "project", machineId: "machine", machineName: "Jacobin", lastSyncedAt: now, stale: false,
};
const otherSource = { ...source, machineId: "other-machine", machineName: "Dover", stale: true };

function project(id: string, title: string) {
  return {
    id, workspace_id: "ws-1", title, description: null,
    icon: null, status: "in_progress" as const, priority: "none" as const, lead_type: null,
    lead_id: null, start_date: null, due_date: null, created_at: "", updated_at: "",
    issue_count: 0, done_count: 0, resource_count: 0,
  };
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.listProjects).mockResolvedValue({
    projects: [project("project", "Harbor"), project("other-project", "Other")],
    total: 2,
  });
  vi.mocked(api.listAgentHarborMonitor).mockResolvedValue({ tasks: [], sources: [source, otherSource], hasMore: false });
});

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { staleTime: Infinity, retry: false }, mutations: { retry: false } },
  });
  renderWithI18n(<QueryClientProvider client={qc}><AgentTasksPage /></QueryClientProvider>);
  return qc;
}

describe("Codex monitor", () => {
  it("confirms only the selected completed turn and shows it as handled in history", async () => {
    const running = { ...terminal, turnId: "turn-2", title: "Check the index", endedAt: null,
      status: "running" as const, handled: null };
    const historical = { ...terminal, turnId: "old", title: "Historic turn", handled: null, historical: true };
    let handled = false;
    let accept!: (value: { handled: true }) => void;
    vi.mocked(api.handleAgentHarborMonitorTask).mockImplementation(() => new Promise((resolve) => { accept = resolve; }));
    vi.mocked(api.listAgentHarborMonitor).mockImplementation(async ({ view }) => ({
      tasks: view === "history" ? [{ ...terminal, handled }, historical] : [running, ...(handled ? [] : [terminal])],
      sources: [source],
      hasMore: false,
    }));
    renderPage();
    const user = userEvent.setup();

    const runningRow = (await screen.findByText(running.title)).closest("tr")!;
    expect(within(runningRow).queryByRole("button", { name: "Mark as handled" })).not.toBeInTheDocument();
    const doneRow = screen.getByText(terminal.title).closest("tr")!;
    const handle = within(doneRow).getByRole("button", { name: "Mark as handled" });
    await user.click(handle);
    expect(handle).toBeDisabled();
    expect(api.handleAgentHarborMonitorTask).toHaveBeenCalledWith({
      projectId: "project", machineId: "machine", sessionId, turnId: "turn-1",
    }, "ws-1");
    handled = true;
    accept({ handled: true });
    await waitFor(() => expect(screen.queryByText(terminal.title)).not.toBeInTheDocument());

    await user.click(screen.getByRole("tab", { name: "History" }));
    const historyRow = (await screen.findByText(terminal.title)).closest("tr")!;
    expect(within(historyRow).getByText("Handled")).toBeVisible();
    const oldRow = screen.getByText(historical.title).closest("tr")!;
    expect(within(oldRow).getByText("Handling status unknown")).toBeVisible();
    expect(within(oldRow).queryByRole("button", { name: "Mark as handled" })).not.toBeInTheDocument();
  });

  it("applies project and machine filters across history pages and keeps results after a refresh error", async () => {
    let fail = false;
    vi.mocked(api.listAgentHarborMonitor).mockImplementation(async (filters) => {
      if (fail) throw new Error("offline");
      const task = filters.machineId === "other-machine"
        ? { ...terminal, machineId: "other-machine", machineName: "Dover" }
        : terminal;
      return {
        tasks: [{ ...task, title: `Task page ${filters.page}` }],
        sources: [source, otherSource],
        hasMore: filters.page === 1,
      };
    });
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("Task page 1");
    await user.click(screen.getByRole("combobox", { name: "Project" }));
    await user.click(await screen.findByRole("option", { name: "Harbor" }));
    await user.click(screen.getByRole("combobox", { name: "Machine" }));
    await user.click(await screen.findByRole("option", { name: "Dover" }));
    await user.click(screen.getByRole("tab", { name: "History" }));
    await user.click(await screen.findByRole("button", { name: "Next page" }));
    const row = (await screen.findByText("Task page 2")).closest("tr")!;
    expect(within(row).getByText("Dover")).toBeVisible();
    expect(screen.getByText("Data may be out of date")).toBeVisible();
    const summary = screen.getByText("Last sync");
    await user.click(summary);
    const syncStatus = summary.closest("details")!;
    expect(within(syncStatus).getByText(/Harbor.*Dover/)).toBeVisible();
    expect(within(syncStatus).queryByText(/Harbor.*Jacobin/)).not.toBeInTheDocument();
    expect(api.listAgentHarborMonitor).toHaveBeenLastCalledWith({
      view: "history", page: 2, projectId: "project", machineId: "other-machine",
    }, "ws-1");

    fail = true;
    await user.click(screen.getByRole("button", { name: "Refresh tasks" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Codex tasks");
    expect(screen.getByText("Task page 2")).toBeVisible();
  });

  it("copies the original session ID and only links UUID sessions to Codex", async () => {
    const unusualId = "session/with?characters";
    vi.mocked(api.listAgentHarborMonitor).mockResolvedValue({
      tasks: [
        terminal,
        { ...terminal, sessionId: unusualId, title: "Imported turn" },
        { ...terminal, sessionId: sessionId + "\n", title: "Malformed session ID" },
      ],
      sources: [source],
      hasMore: false,
    });
    vi.mocked(copyText).mockResolvedValue(true);
    renderPage();
    const user = userEvent.setup();
    const linkedRow = (await screen.findByText(terminal.title)).closest("tr")!;
    const importedRow = screen.getByText("Imported turn").closest("tr")!;
    const malformedRow = screen.getByText("Malformed session ID").closest("tr")!;

    expect(within(linkedRow).getByRole("link", { name: "Open in Codex" }))
      .toHaveAttribute("href", "codex://threads/019a0927-f85d-7766-a0dc-5f218ced525b");
    expect(within(importedRow).queryByRole("link", { name: "Open in Codex" })).not.toBeInTheDocument();
    expect(within(malformedRow).queryByRole("link", { name: "Open in Codex" })).not.toBeInTheDocument();
    await user.click(within(importedRow).getByRole("button", { name: "Copy session ID" }));
    expect(copyText).toHaveBeenCalledWith(unusualId);
    expect(within(importedRow).getByText("Session ID copied")).toBeVisible();
  });

  it("saves paired Bark credentials, clears them, preserves them on toggle, and tests while disabled", async () => {
    vi.mocked(api.getAgentHarborBark).mockResolvedValue({ configured: false, enabled: false });
    vi.mocked(api.putAgentHarborBark)
      .mockResolvedValueOnce({ configured: true, enabled: true })
      .mockResolvedValueOnce({ configured: true, enabled: false });
    vi.mocked(api.testAgentHarborBark).mockResolvedValue({ sent: true });
    const qc = renderPage();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Bark settings" }));
    const url = await screen.findByLabelText("Bark server URL");
    const key = screen.getByLabelText("Device key");
    await user.type(url, "https://bark.example.test");
    await user.type(key, "private-test-key");
    expect(key).toHaveAttribute("type", "password");
    await user.click(screen.getByRole("switch", { name: "Task notifications" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Bark settings saved")).toBeVisible();
    expect(api.putAgentHarborBark).toHaveBeenNthCalledWith(1, {
      enabled: true, serverUrl: "https://bark.example.test", deviceKey: "private-test-key",
    }, "ws-1");
    expect(url).toHaveValue("");
    expect(key).toHaveValue("");
    expect(qc.getMutationCache().getAll().every((mutation) => mutation.state.variables === undefined)).toBe(true);

    await user.click(screen.getByRole("switch", { name: "Task notifications" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.putAgentHarborBark).toHaveBeenNthCalledWith(2, { enabled: false }, "ws-1"));

    await user.click(screen.getByRole("button", { name: "Send test notification" }));
    expect(await screen.findByText("Test notification sent")).toBeVisible();
    expect(api.testAgentHarborBark).toHaveBeenCalledWith("ws-1");
  });
});
