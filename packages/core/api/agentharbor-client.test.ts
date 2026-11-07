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
    expect(tasks.tasks[0]).toMatchObject({
      machine_name: "", last_occurred_at: "", last_received_at: "",
    });
  });

  it("keeps the task page renderable when the response is malformed", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ tasks: "invalid" }), {
      headers: { "Content-Type": "application/json" },
    })));
    expect(await new ApiClient("https://api.example.test").listAgentHarborTasks("project"))
      .toEqual({ tasks: [] });
  });
});
