import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { api } from "@multica/core/api";
import { agentHarborMemoryOptions } from "@multica/core/agentharbor/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { renderWithI18n } from "../test/i18n";
import { ProjectMemoryPage } from "./project-memory-page";

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return { ...actual, useCurrentWorkspace: () => ({ id: "ws-1" }) };
});
vi.mock("@multica/core/api", () => ({
  api: { putAgentHarborMemory: vi.fn() },
}));

describe("Project memory page", () => {
  it("shows the actual L3 snapshot and saves a manual replacement", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
    qc.setQueryData(projectListOptions("ws-1").queryKey, {
      projects: [{
        id: "project", workspace_id: "ws-1", title: "Harbor", description: null,
        icon: null, status: "in_progress", priority: "none", lead_type: null,
        lead_id: null, start_date: null, due_date: null, created_at: "", updated_at: "",
        issue_count: 0, done_count: 0, resource_count: 0,
      }], total: 1,
    });
    qc.setQueryData(agentHarborMemoryOptions("ws-1", "project").queryKey, {
      projectId: "project", version: "v1", content: "Confirmed project rule",
    });
    vi.mocked(api.putAgentHarborMemory).mockResolvedValue({
      projectId: "project", version: "v2", content: "Updated project rule",
    });

    renderWithI18n(<QueryClientProvider client={qc}><ProjectMemoryPage /></QueryClientProvider>);

    expect(screen.getByText("Confirmed project rule")).toBeInTheDocument();
    expect(screen.getAllByText("Not connected yet")).toHaveLength(3);
    fireEvent.click(screen.getByRole("button", { name: "Edit memory" }));
    fireEvent.change(screen.getByRole("textbox", { name: "L3 · Core memory" }), {
      target: { value: "Updated project rule" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save memory" }));

    await waitFor(() => expect(screen.getByText("Updated project rule")).toBeInTheDocument());
    expect(api.putAgentHarborMemory).toHaveBeenCalledWith("project", "Updated project rule");
  });
});
