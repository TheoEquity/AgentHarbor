// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { noopLogger } from "../logger";
import { ApiClient } from "./client";
import { setSchemaLogger } from "./schema";

const now = "2026-11-13T10:00:00Z";
const wireTask = {
  project_id: "project", project_name: "Harbor", machine_id: "machine", machine_name: "Jacobin",
  session_id: "session", turn_id: "turn", title: "Fix the query", started_at: null, ended_at: now,
  status: "aborted", handled: null, historical: true,
};

function mockResponses(...bodies: unknown[]) {
  const fetchMock = vi.fn();
  for (const body of bodies) fetchMock.mockResolvedValueOnce(Response.json(body));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  vi.unstubAllGlobals();
  setSchemaLogger(noopLogger);
});

describe("AgentHarbor monitor response boundary", () => {
  it("encodes filters and keeps missing or invalid handling evidence unknown", async () => {
    const fetchMock = mockResponses({
      tasks: [wireTask, { ...wireTask, turn_id: "turn2", status: "future-status", handled: "false" }],
      sources: [{ project_id: "project", machine_id: "machine", machine_name: "Jacobin", last_synced_at: now, stale: true }],
      has_more: true,
    });

    const result = await new ApiClient("https://api.example.test").listAgentHarborMonitor({
      view: "history", projectId: "p/a", machineId: "m&x", page: 2,
    });
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("https://api.example.test/api/agentharbor/monitor?view=history&project_id=p%2Fa&machine_id=m%26x&page=2");
    expect(result).toEqual({
      tasks: [
        { projectId: "project", projectName: "Harbor", machineId: "machine", machineName: "Jacobin",
          sessionId: "session", turnId: "turn", title: "Fix the query", startedAt: null, endedAt: now,
          status: "aborted", handled: null, historical: true },
        { projectId: "project", projectName: "Harbor", machineId: "machine", machineName: "Jacobin",
          sessionId: "session", turnId: "turn2", title: "Fix the query", startedAt: null, endedAt: now,
          status: "unknown", handled: null, historical: true },
      ],
      sources: [{ projectId: "project", machineId: "machine", machineName: "Jacobin", lastSyncedAt: now, stale: true }],
      hasMore: true,
    });
  });

  it("rejects malformed list data so it cannot replace previously synced results", async () => {
    mockResponses({ tasks: "invalid", sources: [], has_more: false });

    await expect(new ApiClient("https://api.example.test").listAgentHarborMonitor({ view: "active" }))
      .rejects.toThrow("Malformed response");
  });

  it("requires affirmative confirmation for the requested turn", async () => {
    const fetchMock = mockResponses({ handled: true }, { handled: false });
    const client = new ApiClient("https://api.example.test");
    const turn = { projectId: "project", machineId: "machine", sessionId: "session", turnId: "turn" };

    await expect(client.handleAgentHarborMonitorTask(turn)).resolves.toEqual({ handled: true });
    expect(fetchMock.mock.calls[0]).toEqual([
      "https://api.example.test/api/agentharbor/monitor/handle",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ project_id: "project", machine_id: "machine", session_id: "session", turn_id: "turn" }),
      }),
    ]);
    await expect(client.handleAgentHarborMonitorTask(turn)).rejects.toThrow("Malformed response");
  });

  it("only reads Bark status and sends test requests without any credentials", async () => {
    const fetchMock = mockResponses(
      { configured: true, enabled: true, server_url: "https://private.example.test", device_key: "private-key" },
      { configured: true, enabled: false },
      { sent: true },
      { sent: false },
    );
    const client = new ApiClient("https://api.example.test");

    await expect(client.getAgentHarborBark()).resolves.toEqual({ configured: true, enabled: true });
    await expect(client.putAgentHarborBark({ enabled: false })).resolves.toEqual({ configured: true, enabled: false });
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({ method: "PUT", body: '{"enabled":false}' });
    await expect(client.testAgentHarborBark()).resolves.toEqual({ sent: true });
    expect(fetchMock.mock.calls[2]).toEqual([
      "https://api.example.test/api/agentharbor/monitor/bark/test",
      expect.objectContaining({ method: "POST" }),
    ]);
    expect(fetchMock.mock.calls[2]?.[1].body).toBeUndefined();
    await expect(client.testAgentHarborBark()).rejects.toThrow("Malformed response");
  });

  it("does not report Bark credentials from malformed or unsuccessful responses", async () => {
    const warn = vi.fn();
    const error = vi.fn();
    setSchemaLogger({ ...noopLogger, warn });
    const secret = "unexpected-private-value";
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(Response.json({ enabled: secret, device_key: secret }))
      .mockResolvedValueOnce(Response.json({ error: secret, device_key: secret }, { status: 400 })));
    const client = new ApiClient("https://api.example.test", { logger: { ...noopLogger, error } });

    await expect(client.getAgentHarborBark()).rejects.toThrow("Malformed response");
    expect(warn).toHaveBeenCalled();
    expect(JSON.stringify(warn.mock.calls)).not.toContain(secret);
    await expect(client.putAgentHarborBark({ enabled: true, serverUrl: "https://bark.example.test", deviceKey: secret }))
      .rejects.toMatchObject({ status: 400, body: undefined, message: "API error: 400" });
    expect(error).toHaveBeenCalled();
    expect(JSON.stringify(error.mock.calls)).not.toContain(secret);
  });
});
