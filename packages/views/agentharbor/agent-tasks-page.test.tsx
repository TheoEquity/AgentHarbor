import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { agentHarborTasksOptions } from "@multica/core/agentharbor/queries";
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

describe("Agent tasks page", () => {
  it("shows the project, machine, agent, and evidence-based status", () => {
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
        events: [],
      }],
    });

    renderWithI18n(<QueryClientProvider client={qc}><AgentTasksPage /></QueryClientProvider>);

    expect(screen.getByRole("link", { name: "Harbor" })).toHaveAttribute("href", "/acme/projects/project");
    expect(screen.getByText("Jacobin")).toBeInTheDocument();
    expect(screen.getByText("Codex")).toBeInTheDocument();
    expect(screen.getByText("Turn ended · completion unknown")).toBeInTheDocument();
    expect(screen.getByText("Shows the latest synced record, not a live signal.")).toBeInTheDocument();
  });
});
