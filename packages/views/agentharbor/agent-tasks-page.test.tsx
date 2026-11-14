import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { agentHarborTasksOptions } from "@multica/core/agentharbor/queries";
import type { AgentHarborTask } from "@multica/core/api/schemas";
import { projectListOptions } from "@multica/core/projects/queries";
import { renderWithI18n } from "../test/i18n";
import { AgentTasksPage } from "./agent-tasks-page";

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

function renderTask(events: AgentHarborTask["events"] = []) {
  const qc = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
  qc.setQueryData(projectListOptions("ws-1").queryKey, {
    projects: [{
      id: "project", workspace_id: "ws-1", title: "Harbor", description: null,
      icon: null, status: "in_progress", priority: "none", lead_type: null,
      lead_id: null, start_date: null, due_date: null, created_at: "", updated_at: "",
      issue_count: 0, done_count: 0, resource_count: 0,
    }], total: 1,
  });
  qc.setQueryData(agentHarborTasksOptions("ws-1", "project").queryKey, {
    tasks: [{
      projectId: "project", machineId: "machine", machineName: "Jacobin",
      agentType: "codex", sessionId: "session-123", status: "turn_ended_unknown",
      lastOccurredAt: "2026-11-07T16:41:39Z", lastReceivedAt: "2026-11-07T16:42:14Z",
      events,
    }],
  });

  renderWithI18n(<QueryClientProvider client={qc}><AgentTasksPage /></QueryClientProvider>);
}

describe("Agent tasks page", () => {
  it("shows the project, machine, agent, and evidence-based status", () => {
    renderTask();
    expect(screen.getByRole("link", { name: "Harbor" })).toHaveAttribute("href", "/acme/projects/project");
    expect(screen.getByText("Jacobin")).toBeInTheDocument();
    expect(screen.getByText("Codex")).toBeInTheDocument();
    expect(screen.getByText("Turn ended · completion unknown")).toBeInTheDocument();
    expect(screen.getByText("Shows the latest synced record, not a live signal.")).toBeInTheDocument();
    expect(screen.queryByText("Startup memory")).not.toBeInTheDocument();
  });

  it("expands the latest startup memory receipt without changing the task status", async () => {
    const id = "8753d3d3-d2e6-4efd-824c-9e4c1a94ad67";
    const version = "a".repeat(64);
    const event = {
      machineId: "machine", projectId: "project", agentType: "codex", sessionId: "session-123",
      turnId: "", eventType: "memory_read", occurredAt: "2026-11-07T16:40:39Z",
      receivedAt: "2026-11-07T16:40:39Z", evidence: "",
    };
    renderTask([
      {
        ...event, eventId: "later", localSeq: 2,
        memoryReceipt: {
          trigger: "startup", fetched: true, emitted: true, contextEmitted: true,
          entries: [{ id, revision: 3 }], version,
        },
      },
      {
        ...event, eventId: "earlier", localSeq: 1,
        memoryReceipt: {
          trigger: "startup", fetched: false, emitted: false, contextEmitted: false, entries: [], version: "",
        },
      },
    ]);

    expect(screen.getByText("Turn ended · completion unknown")).toBeVisible();
    const reference = screen.getByText(`${id} · revision 3`);
    expect(reference).not.toBeVisible();
    const user = userEvent.setup();
    await user.click(screen.getByText("Startup memory"));
    expect(screen.getByText("Approved memory was retrieved.")).toBeVisible();
    expect(screen.getByText("The startup hook included approved memory in its output.")).toBeVisible();
    expect(screen.getByText(`Context version ${version}`)).toBeVisible();
    expect(reference).toBeVisible();
    expect(screen.queryByText("Could not retrieve approved memory.")).not.toBeInTheDocument();
  });
});
