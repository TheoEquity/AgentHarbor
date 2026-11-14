// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

describe("AgentHarbor task response boundary", () => {
  it("reads project tasks and defaults fields missing from an older response", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      tasks: [{
        project_id: "project", machine_id: "machine", agent_type: "codex",
        session_id: "session", status: "turn_ended_unknown",
      }],
    }), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    const tasks = await new ApiClient("https://api.example.test").listAgentHarborTasks("a/b");
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain("project_id=a%2Fb");
    expect(tasks.tasks[0]).toEqual({
      projectId: "project", machineId: "machine", machineName: "",
      agentType: "codex", sessionId: "session", status: "turn_ended_unknown",
      lastOccurredAt: "", lastReceivedAt: "", events: [],
    });
  });

  it("maps nested event fields at the API boundary", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      tasks: [{
        project_id: "project", machine_id: "machine", machine_name: "Jacobin",
        agent_type: "codex", session_id: "session", status: "running",
        last_occurred_at: "2026-11-07T16:41:39Z", last_received_at: "2026-11-07T16:42:14Z",
        events: [{
          event_id: "event", machine_id: "machine", project_id: "project",
          agent_type: "codex", session_id: "session", turn_id: "turn", local_seq: 1,
          event_type: "prompt_started", occurred_at: "2026-11-07T16:41:39Z",
          received_at: "2026-11-07T16:42:14Z", evidence: "prompt chars: 42",
        }],
      }],
    }), { headers: { "Content-Type": "application/json" } })));

    const { tasks } = await new ApiClient("https://api.example.test").listAgentHarborTasks("project");
    expect(tasks[0]).toEqual({
      projectId: "project", machineId: "machine", machineName: "Jacobin",
      agentType: "codex", sessionId: "session", status: "running",
      lastOccurredAt: "2026-11-07T16:41:39Z", lastReceivedAt: "2026-11-07T16:42:14Z",
      events: [{
        eventId: "event", machineId: "machine", projectId: "project",
        agentType: "codex", sessionId: "session", turnId: "turn", localSeq: 1,
        eventType: "prompt_started", occurredAt: "2026-11-07T16:41:39Z",
        receivedAt: "2026-11-07T16:42:14Z", evidence: "prompt chars: 42",
      }],
    });
  });

  it("keeps a task summary when nested event data is malformed", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      tasks: [{
        project_id: "project", machine_id: "machine", agent_type: "codex",
        session_id: "session", events: [{ local_seq: "invalid" }],
      }],
    }), { headers: { "Content-Type": "application/json" } })));

    expect(await new ApiClient("https://api.example.test").listAgentHarborTasks("project"))
      .toMatchObject({ tasks: [{ machineId: "machine", events: [] }] });
  });

  it("normalizes startup receipts and drops malformed or unexpected receipt evidence", async () => {
    const entry = { id: "8753d3d3-d2e6-4efd-824c-9e4c1a94ad67", revision: 2 };
    const receipt = {
      trigger: "startup", fetched: true, emitted: true, context_emitted: true,
      entries: [entry], version: "a".repeat(64),
    };
    const event = {
      event_id: "event", machine_id: "machine", project_id: "project", agent_type: "codex",
      session_id: "session", turn_id: "", local_seq: 1, event_type: "memory_read",
      occurred_at: "2026-11-07T16:41:39Z", received_at: "2026-11-07T16:42:14Z",
    };
    const evidence = [
      receipt,
      { ...receipt, entries: [], version: "", fetched: false, context_emitted: false },
      { ...receipt, content: "unexpected body" },
      { ...receipt, entries: [{ ...entry, revision: 0 }] },
      { ...receipt, fetched: false },
    ];
    const events = [
      ...evidence.map((value) => ({ ...event, evidence: JSON.stringify(value) })),
      { ...event, evidence: "{" },
      { ...event, evidence: JSON.stringify(receipt).padEnd(8193) },
    ];
    mockMemoryResponses({
      tasks: [{
        project_id: "project", machine_id: "machine", agent_type: "codex", session_id: "session",
        events, status: "running",
      }],
    });

    const { tasks } = await new ApiClient("https://api.example.test").listAgentHarborTasks("project");
    const task = tasks[0];
    expect(task?.status).toBe("running");
    expect(task?.events[0]?.memoryReceipt).toEqual({
      trigger: "startup", fetched: true, emitted: true, contextEmitted: true,
      entries: [entry], version: "a".repeat(64),
    });
    expect(task?.events[1]?.memoryReceipt).toMatchObject({ fetched: false, entries: [], contextEmitted: false });
    expect(task?.events.slice(2)).toHaveLength(5);
    for (const invalidEvent of task?.events.slice(2) ?? []) {
      expect(invalidEvent.memoryReceipt).toBeUndefined();
      expect(invalidEvent.evidence).toBe("");
    }
  });

  it("keeps the task page renderable when the response is malformed", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ tasks: "invalid" }), {
      headers: { "Content-Type": "application/json" },
    })));
    expect(await new ApiClient("https://api.example.test").listAgentHarborTasks("project"))
      .toEqual({ tasks: [] });
  });
});

describe("AgentHarbor memory response boundary", () => {
  it("reads and updates the actual project snapshot", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({
      project_id: "project", content: "Confirmed rule", version: "v1",
    }), { headers: { "Content-Type": "application/json" } })));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    expect(await client.getAgentHarborMemory("project")).toEqual({
      projectId: "project", content: "Confirmed rule", version: "v1",
    });
    expect(await client.putAgentHarborMemory("project", "Confirmed rule")).toEqual({
      projectId: "project", content: "Confirmed rule", version: "v1",
    });
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain("/api/projects/project/memory-snapshot");
    expect(String(fetchMock.mock.calls[1]?.[0])).toContain("/api/projects/project/memory/core");
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({
      method: "PUT", body: JSON.stringify({ content: "Confirmed rule" }),
    });
  });

  it("does not present malformed memory as an empty document", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ content: 1 }), {
      headers: { "Content-Type": "application/json" },
    })));
    await expect(new ApiClient("https://api.example.test").getAgentHarborMemory("project"))
      .rejects.toThrow("Invalid project memory response");
  });
});
const wireMaterial = {
  id: "material", project_id: "project", source_text: "Raw source", context: "Prompt context",
  session_id: "session", turn_id: "turn", status: "pending", related_entry_id: "entry",
  related_revision: 1, created_at: "2026-11-13T10:00:00Z",
};

const wireEntry = {
  id: "entry", project_id: null, scope: "global", type: "procedural", title: "Run the checks",
  content: "Use the managed test runner", status: "active", revision: 2,
  source_material_ids: ["material", "extra"], updated_at: "2026-11-13T10:00:00Z",
};

const wireSettings = {
  record_enabled: false, cloud_enabled: true, use_memory: true, use_global: false,
};

function mockMemoryResponses(...responses: unknown[]) {
  const fetchMock = vi.fn();
  for (const response of responses) {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(response), {
      headers: { "Content-Type": "application/json" },
    }));
  }
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("AgentHarbor reviewed memory response boundary", () => {
  const client = new ApiClient("https://api.example.test");

  it("normalizes source material and sends explicit merge options", async () => {
    const fetchMock = mockMemoryResponses(
      { materials: [wireMaterial] },
      { material: { ...wireMaterial, status: "processed" }, entry: wireEntry },
    );

    expect(await client.listAgentHarborMemoryMaterials("a/b")).toEqual([{
      id: "material", projectId: "project", sourceText: "Raw source", context: "Prompt context",
      sessionId: "session", turnId: "turn", status: "pending", relatedEntryId: "entry",
      relatedRevision: 1, createdAt: "2026-11-13T10:00:00Z",
    }]);
    expect(await client.reviewAgentHarborMemoryMaterial("a/b", "material/1", {
      action: "approve", scope: "global", type: "procedural", title: wireEntry.title, content: wireEntry.content,
      target: { entryId: "entry", expectedRevision: 1 }, additionalMaterialIds: ["extra"],
    })).toMatchObject({
      material: { status: "processed" },
      entry: { projectId: null, scope: "global", revision: 2, sourceMaterialIds: ["material", "extra"] },
    });

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/projects/a%2Fb/memory/materials");
    expect(fetchMock.mock.calls[1]?.[0]).toBe("https://api.example.test/api/projects/a%2Fb/memory/materials/material%2F1");
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({
      method: "PATCH",
      body: JSON.stringify({
        action: "approve", scope: "global", type: "procedural", title: wireEntry.title, content: wireEntry.content,
        entry_id: "entry", expected_revision: 1, additional_material_ids: ["extra"],
      }),
    });
  });

  it("requests a scope and serializes settings and formal entry revisions", async () => {
    const fetchMock = mockMemoryResponses(
      { entries: [wireEntry] },
      { ...wireEntry, revision: 3, content: "Run checks before merging" },
      wireSettings,
      { ...wireSettings, use_global: true },
    );

    expect(await client.listAgentHarborMemoryEntries("project", "global")).toEqual([{
      id: "entry", projectId: null, scope: "global", type: "procedural", title: "Run the checks",
      content: "Use the managed test runner", status: "active", revision: 2,
      sourceMaterialIds: ["material", "extra"], updatedAt: "2026-11-13T10:00:00Z",
    }]);
    expect(await client.updateAgentHarborMemoryEntry("project", "entry", {
      action: "revise", expectedRevision: 2, title: wireEntry.title, content: "Run checks before merging", type: "procedural",
    })).toMatchObject({ revision: 3, content: "Run checks before merging" });
    const settings = await client.getAgentHarborMemorySettings("project");
    expect(settings).toEqual({ recordEnabled: false, cloudEnabled: true, useMemory: true, useGlobal: false });
    expect(await client.putAgentHarborMemorySettings("project", { ...settings, useGlobal: true }))
      .toEqual({ ...settings, useGlobal: true });

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/projects/project/memory/entries?scope=global");
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({
      method: "PATCH",
      body: JSON.stringify({
        action: "revise", expected_revision: 2, title: wireEntry.title, content: "Run checks before merging", type: "procedural",
      }),
    });
    expect(fetchMock.mock.calls[3]?.[1]).toMatchObject({
      method: "PUT", body: JSON.stringify({ ...wireSettings, use_global: true }),
    });
  });

  it("maps future enum strings without allowing them to masquerade as active or pending", async () => {
    mockMemoryResponses(
      { materials: [{ ...wireMaterial, status: "future_status", related_revision: null, related_entry_id: undefined }] },
      { entries: [{ ...wireEntry, status: "future_status", scope: "future_scope", type: "future_type" }] },
    );

    expect(await client.listAgentHarborMemoryMaterials("project")).toMatchObject([
      { status: "unknown", relatedRevision: null, relatedEntryId: null },
    ]);
    expect(await client.listAgentHarborMemoryEntries("project", "global")).toMatchObject([
      { status: "unknown", scope: "unknown", type: "unknown" },
    ]);
  });

  it("imports legacy memory as source material or reports an absent source", async () => {
    const fetchMock = mockMemoryResponses({ material: wireMaterial }, {}, { material: { id: "invalid" } });
    expect(await client.importAgentHarborLegacyMemory("project")).toMatchObject({ id: "material", status: "pending" });
    expect(await client.importAgentHarborLegacyMemory("project")).toBeUndefined();
    await expect(client.importAgentHarborLegacyMemory("project")).rejects.toThrow("Malformed response");
    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/projects/project/memory/import-legacy");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "POST" });
  });

  it("reads and validates the selected entry's history and authorized sources", async () => {
    const revision = {
      revision: 1, type: "semantic", title: "Original", content: "Original wording", status: "active",
      source_material_ids: ["material"], created_at: "2026-11-13T10:00:00Z",
    };
    const fetchMock = mockMemoryResponses(
      { revisions: [revision], materials: [wireMaterial], disputes: [{ id: "correction", source_text: "New evidence", context: "" }] },
      { revisions: [{ ...revision, revision: "1" }], materials: [] },
    );

    expect(await client.getAgentHarborMemoryHistory("p/1", "entry/1")).toMatchObject({
      revisions: [{
        revision: 1, type: "semantic", title: "Original", content: "Original wording", status: "active",
        sourceMaterialIds: ["material"], createdAt: "2026-11-13T10:00:00Z",
      }],
      materials: [{ id: "material", sourceText: "Raw source" }],
      disputes: [{ id: "correction", sourceText: "New evidence", context: "" }],
    });
    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/projects/p%2F1/memory/entries/entry%2F1/history");
    await expect(client.getAgentHarborMemoryHistory("project", "entry")).rejects.toThrow("Malformed response");
  });

  it("reports malformed responses instead of showing false empty, saved or approved states", async () => {
    mockMemoryResponses(
      { materials: [{ ...wireMaterial, source_text: null }] },
      { entries: [{ ...wireEntry, source_material_ids: null }] },
      { material: { ...wireMaterial, status: "processed" } },
      { ...wireEntry, revision: "3" },
      { ...wireSettings, use_global: "false" },
      { ...wireSettings, record_enabled: undefined },
    );

    await expect(client.listAgentHarborMemoryMaterials("project")).rejects.toThrow("Malformed response");
    await expect(client.listAgentHarborMemoryEntries("project", "project")).rejects.toThrow("Malformed response");
    await expect(client.reviewAgentHarborMemoryMaterial("project", "material", {
      action: "approve", scope: "project", type: "semantic", title: "Rule", content: "Check it",
    })).rejects.toThrow("missing its memory entry");
    await expect(client.updateAgentHarborMemoryEntry("project", "entry", {
      action: "disable", expectedRevision: 2,
    })).rejects.toThrow("Malformed response");
    await expect(client.getAgentHarborMemorySettings("project")).rejects.toThrow("Malformed response");
    await expect(client.putAgentHarborMemorySettings("project", {
      recordEnabled: true, cloudEnabled: true, useMemory: true, useGlobal: false,
    })).rejects.toThrow("Malformed response");
  });
});
