import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, api } from "@multica/core/api";
import type { AgentHarborMemoryEntry, AgentHarborMemoryMaterial, AgentHarborMemorySettings } from "@multica/core/api/schemas";
import { agentHarborMemoryEntriesOptions } from "@multica/core/agentharbor/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { renderWithI18n } from "../test/i18n";
import { ProjectMemoryPage } from "./project-memory-page";

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return { ...actual, useCurrentWorkspace: () => ({ id: "ws-1" }) };
});
vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: {
      listAgentHarborMemoryMaterials: vi.fn(),
      importAgentHarborLegacyMemory: vi.fn(),
      listAgentHarborMemoryEntries: vi.fn(),
      getAgentHarborMemoryHistory: vi.fn(),
      reviewAgentHarborMemoryMaterial: vi.fn(),
      updateAgentHarborMemoryEntry: vi.fn(),
      getAgentHarborMemorySettings: vi.fn(),
      putAgentHarborMemorySettings: vi.fn(),
    },
  };
});

const source: AgentHarborMemoryMaterial = {
  id: "material", projectId: "project", sourceText: "Use the test runner before pushing",
  context: "CI discussion", sessionId: "session-1", turnId: "turn-1",
  status: "pending", relatedEntryId: null, relatedRevision: null,
  createdAt: "2026-11-13T10:00:00Z",
};

const projectEntry: AgentHarborMemoryEntry = {
  id: "entry", projectId: "project", scope: "project", type: "procedural", title: "Run checks",
  content: "Previous wording", status: "active", revision: 2, sourceMaterialIds: [],
  updatedAt: "2026-11-13T10:00:00Z",
};

const settings: AgentHarborMemorySettings = {
  recordEnabled: true, cloudEnabled: true, useMemory: true, useGlobal: false,
};

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
  client.setQueryData(projectListOptions("ws-1").queryKey, {
    projects: [{
      id: "project", workspace_id: "ws-1", title: "Harbor", description: null,
      icon: null, status: "in_progress", priority: "none", lead_type: null,
      lead_id: null, start_date: null, due_date: null, created_at: "", updated_at: "",
      issue_count: 0, done_count: 0, resource_count: 0,
    }], total: 1,
  });

  renderWithI18n(<QueryClientProvider client={client}><ProjectMemoryPage /></QueryClientProvider>);
  return { client, user: userEvent.setup() };
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.listAgentHarborMemoryMaterials).mockResolvedValue([source]);
  vi.mocked(api.listAgentHarborMemoryEntries).mockImplementation(async (_projectId, scope) =>
    scope === "project" ? [projectEntry] : [],
  );
  vi.mocked(api.getAgentHarborMemorySettings).mockResolvedValue(settings);
  vi.mocked(api.getAgentHarborMemoryHistory).mockResolvedValue({ revisions: [], materials: [source], disputes: [] });
});

describe("Project memory page", () => {
  it("approves edited material into general memory and opens authorized provenance and history", async () => {
    const reviewed = { ...source, status: "processed" as const };
    const approved = {
      ...projectEntry, id: "general", projectId: null, scope: "global" as const, type: "procedural" as const,
      title: "Check before pushing", content: "Run the managed checks before pushing.", revision: 1,
      sourceMaterialIds: [source.id, "source-from-another-project", "deleted-source"],
    };
    vi.mocked(api.reviewAgentHarborMemoryMaterial).mockImplementation(async () => {
      vi.mocked(api.listAgentHarborMemoryMaterials).mockResolvedValue([reviewed]);
      vi.mocked(api.listAgentHarborMemoryEntries).mockImplementation(async (_projectId, scope) =>
        scope === "global" ? [approved] : [projectEntry],
      );
      return { material: reviewed, entry: approved };
    });
    const { user } = renderPage();

    await user.click(await screen.findByRole("button", { name: "Review" }));
    const dialog = screen.getByRole("dialog", { name: "Review material" });
    expect(within(dialog).getByText(source.sourceText, { selector: "p" })).toBeInTheDocument();
    expect(within(dialog).getByText(source.context)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("combobox", { name: "Scope" }));
    await user.click(await screen.findByRole("option", { name: "General memory" }));
    await user.click(within(dialog).getByRole("combobox", { name: "Type" }));
    await user.click(await screen.findByRole("option", { name: "Procedural" }));
    await user.type(within(dialog).getByRole("textbox", { name: "Title" }), approved.title);
    fireEvent.change(within(dialog).getByRole("textbox", { name: "Content" }), { target: { value: approved.content } });
    await user.click(within(dialog).getByRole("button", { name: "Approve" }));

    await screen.findByText("No materials to review");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.reviewAgentHarborMemoryMaterial).toHaveBeenCalledWith("project", "material", {
      action: "approve", scope: "global", type: "procedural", title: approved.title, content: approved.content,
    });
    await user.click(screen.getByRole("tab", { name: "General memory" }));
    expect(await screen.findByText(approved.content)).toBeInTheDocument();
    expect(api.getAgentHarborMemoryHistory).not.toHaveBeenCalled();
    vi.mocked(api.getAgentHarborMemoryHistory).mockResolvedValue({
      disputes: [{ id: "correction", sourceText: "The procedure changed", context: "Check the current fixture" }],
      materials: [reviewed, {
        ...source, id: "source-from-another-project", projectId: "another-project", sourceText: "Authorized source from another project",
      }],
      revisions: [{
        revision: 1, type: "episodic", title: "First draft", content: "First wording", status: "active",
        sourceMaterialIds: ["source-from-another-project"], createdAt: "2026-11-12T10:00:00Z",
      }],
    });
    await user.click(screen.getByRole("button", { name: "Sources" }));
    expect(screen.getByText(source.sourceText)).toBeVisible();
    expect(await screen.findByText("Authorized source from another project")).toBeVisible();
    expect(screen.getByText("Correction pending review")).toBeVisible();
    expect(screen.getByText("The procedure changed")).toBeVisible();
    expect(screen.getByText("Source deleted-source is unavailable here")).toBeVisible();
    expect(api.getAgentHarborMemoryHistory).toHaveBeenCalledWith("project", "general");
    await user.click(screen.getByRole("button", { name: "Version history" }));
    await user.click(await screen.findByText("Revision 1 · First draft"));
    expect(screen.getByText("First wording")).toBeVisible();
    expect(screen.getByText("Authorized source from another project")).toBeVisible();
    expect(api.getAgentHarborMemoryHistory).toHaveBeenCalledTimes(1);
  });

  it("blocks a correction whose related memory changed before review", async () => {
    vi.mocked(api.listAgentHarborMemoryMaterials).mockResolvedValue([{
      ...source, relatedEntryId: projectEntry.id, relatedRevision: 1,
    }]);
    const { user } = renderPage();
    const review = await screen.findByRole("button", { name: "Review" });
    await waitFor(() => expect(review).toBeEnabled());
    await user.click(review);

    const dialog = within(screen.getByRole("dialog", { name: "Review material" }));
    expect(dialog.getByRole("combobox", { name: "Save to" })).toHaveTextContent(projectEntry.title);
    expect(dialog.getByText("Revision 1")).toBeVisible();
    expect(dialog.getByText("Revision 2")).toBeVisible();
    expect(dialog.getByText(projectEntry.content, { selector: "p" })).toBeVisible();
    expect(dialog.getByRole("alert")).toHaveTextContent("The related memory changed");
    expect(dialog.getByRole("button", { name: "Approve" })).toBeDisabled();
    await user.click(dialog.getByRole("button", { name: "Use latest revision" }));
    expect(dialog.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(api.reviewAgentHarborMemoryMaterial).not.toHaveBeenCalled();
  });

  it("holds a selected revision until the reviewer explicitly uses the latest version", async () => {
    vi.mocked(api.listAgentHarborMemoryMaterials).mockResolvedValue([{
      ...source, relatedEntryId: projectEntry.id, relatedRevision: projectEntry.revision,
    }]);
    const newer = { ...projectEntry, content: "Another reviewer changed this", revision: 3 };
    vi.mocked(api.reviewAgentHarborMemoryMaterial)
      .mockImplementationOnce(async () => {
        vi.mocked(api.listAgentHarborMemoryEntries).mockImplementation(async (_projectId, scope) =>
          scope === "project" ? [newer] : [],
        );
        throw new ApiError("Revision conflict", 409, "Conflict", { code: "memory_revision_conflict" });
      })
      .mockResolvedValueOnce({
        material: { ...source, status: "processed" },
        entry: { ...newer, revision: 4, content: "Reviewed wording" },
      });
    const { client, user } = renderPage();
    await waitFor(() => expect(client.getQueryData(agentHarborMemoryEntriesOptions("ws-1", "project", "project").queryKey))
      .toEqual([projectEntry]));

    await user.click(await screen.findByRole("button", { name: "Review" }));
    const dialog = within(screen.getByRole("dialog", { name: "Review material" }));
    expect(dialog.getByRole("combobox", { name: "Save to" })).toHaveTextContent(projectEntry.title);
    expect(dialog.getByText(projectEntry.content, { selector: "p" })).toBeInTheDocument();
    fireEvent.change(dialog.getByRole("textbox", { name: "Content" }), { target: { value: "Reviewed wording" } });
    await user.click(dialog.getByRole("button", { name: "Approve" }));

    const reload = await dialog.findByRole("button", { name: "Use latest revision" });
    expect(dialog.getByRole("button", { name: "Approve" })).toBeDisabled();
    await user.click(reload);
    expect(dialog.getByText(newer.content)).toBeInTheDocument();
    expect(dialog.getByRole("textbox", { name: "Content" })).toHaveValue("Reviewed wording");
    await user.click(dialog.getByRole("button", { name: "Approve" }));

    expect(api.reviewAgentHarborMemoryMaterial).toHaveBeenNthCalledWith(1, "project", "material", expect.objectContaining({
      target: { entryId: "entry", expectedRevision: 2 },
    }));
    expect(api.reviewAgentHarborMemoryMaterial).toHaveBeenNthCalledWith(2, "project", "material", expect.objectContaining({
      target: { entryId: "entry", expectedRevision: 3 },
    }));
  });

  it("defers without mutating and discards after confirmation", async () => {
    vi.mocked(api.reviewAgentHarborMemoryMaterial).mockImplementation(async () => {
      const discarded = { ...source, status: "discarded" as const };
      vi.mocked(api.listAgentHarborMemoryMaterials).mockResolvedValue([discarded]);
      return { material: discarded };
    });
    const { user } = renderPage();
    const open = await screen.findByRole("button", { name: "Review" });
    await user.click(open);
    await user.click(screen.getByRole("button", { name: "Defer" }));
    expect(api.reviewAgentHarborMemoryMaterial).not.toHaveBeenCalled();

    await user.click(open);
    await user.click(screen.getByRole("button", { name: "Discard" }));
    expect(api.reviewAgentHarborMemoryMaterial).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Discard" }));

    await screen.findByText("No materials to review");
    expect(api.reviewAgentHarborMemoryMaterial).toHaveBeenCalledWith("project", "material", { action: "discard" });
  });

  it("revises a formal entry and saves the project memory options", async () => {
    const updated = { ...projectEntry, title: "Run CI", revision: 3 };
    vi.mocked(api.updateAgentHarborMemoryEntry).mockImplementation(async () => {
      vi.mocked(api.listAgentHarborMemoryEntries).mockImplementation(async (_projectId, scope) =>
        scope === "project" ? [updated] : [],
      );
      return updated;
    });
    vi.mocked(api.putAgentHarborMemorySettings).mockResolvedValue({ ...settings, useMemory: false });
    const { user } = renderPage();

    await user.click(screen.getByRole("tab", { name: "Project memory" }));
    await user.click(await screen.findByRole("button", { name: "Edit memory" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Title" }), { target: { value: updated.title } });
    await user.click(screen.getByRole("button", { name: "Save memory" }));
    expect(await screen.findByRole("heading", { name: updated.title })).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.updateAgentHarborMemoryEntry).toHaveBeenCalledWith("project", "entry", {
      action: "revise", expectedRevision: 2, title: updated.title, type: updated.type, content: updated.content,
    });

    await user.click(screen.getByRole("tab", { name: "Settings" }));
    await user.click(await screen.findByRole("switch", { name: "Use approved memory" }));
    expect(screen.getByRole("switch", { name: "Include general memory" })).toHaveAttribute("aria-disabled", "true");
    await user.click(screen.getByRole("button", { name: "Save settings" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Settings saved");
    expect(api.putAgentHarborMemorySettings).toHaveBeenCalledWith("project", { ...settings, useMemory: false });
  });

  it("keeps the draft visible when saving fails", async () => {
    vi.mocked(api.reviewAgentHarborMemoryMaterial).mockRejectedValue(new Error("Network failure"));
    const { user } = renderPage();
    await user.click(await screen.findByRole("button", { name: "Review" }));
    const title = screen.getByRole("textbox", { name: "Title" });
    await user.type(title, "Keep this draft");
    await user.click(screen.getByRole("button", { name: "Approve" }));
    await screen.findByRole("alert");
    expect(title).toHaveValue("Keep this draft");
  });

  it("reports an unavailable legacy source and can retry into the pending inbox", async () => {
    vi.mocked(api.listAgentHarborMemoryMaterials).mockResolvedValue([]);
    vi.mocked(api.importAgentHarborLegacyMemory)
      .mockRejectedValueOnce(new ApiError("Unavailable", 503, "Service Unavailable"))
      .mockImplementationOnce(async () => {
        const legacy = { ...source, sourceText: "Original project rules" };
        vi.mocked(api.listAgentHarborMemoryMaterials).mockResolvedValue([legacy]);
        return legacy;
      });
    const { user } = renderPage();
    const importButton = screen.getByRole("button", { name: "Import legacy memory" });
    await user.click(importButton);
    expect(await screen.findByRole("alert")).toHaveTextContent("Legacy memory is temporarily unavailable.");
    await user.click(importButton);
    expect(await screen.findByRole("status")).toHaveTextContent("Legacy memory is ready for review.");
    expect(screen.getByText("Original project rules")).toBeInTheDocument();
  });
});
