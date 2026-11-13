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
