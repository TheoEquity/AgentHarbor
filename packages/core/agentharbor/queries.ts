import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { AgentHarborMemoryScope, AgentHarborMonitorFilters } from "../api/schemas";

export const agentHarborTaskKeys = {
  all: (wsId: string) => ["agentharbor-tasks", wsId] as const,
  project: (wsId: string, projectId: string) =>
    [...agentHarborTaskKeys.all(wsId), projectId] as const,
};

export function agentHarborTasksOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: agentHarborTaskKeys.project(wsId, projectId),
    queryFn: () => api.listAgentHarborTasks(projectId),
    refetchInterval: 30_000,
  });
}

export const agentHarborMonitorKeys = {
  lists: (wsId: string) => ["agentharbor-monitor", wsId] as const,
  view: (wsId: string, view: AgentHarborMonitorFilters["view"]) =>
    [...agentHarborMonitorKeys.lists(wsId), view] as const,
  list: (wsId: string, { view, ...filters }: AgentHarborMonitorFilters) =>
    [...agentHarborMonitorKeys.view(wsId, view), filters] as const,
  bark: (wsId: string) => ["agentharbor-bark", wsId] as const,
};

export function agentHarborMonitorOptions(wsId: string, filters: AgentHarborMonitorFilters) {
  return queryOptions({
    queryKey: agentHarborMonitorKeys.list(wsId, filters),
    queryFn: () => api.listAgentHarborMonitor(filters, wsId),
    refetchInterval: 30_000,
  });
}

export function agentHarborBarkOptions(wsId: string) {
  return queryOptions({
    queryKey: agentHarborMonitorKeys.bark(wsId),
    queryFn: () => api.getAgentHarborBark(wsId),
  });
}

export const agentHarborMemoryKeys = {
  general: ["agentharbor-memory-global"] as const,
  project: (wsId: string, projectId: string) => ["agentharbor-memory", wsId, projectId] as const,
  materials: (wsId: string, projectId: string) =>
    [...agentHarborMemoryKeys.project(wsId, projectId), "materials"] as const,
  entries: (wsId: string, projectId: string, scope: AgentHarborMemoryScope) =>
    scope === "global"
      ? [...agentHarborMemoryKeys.general, wsId, projectId] as const
      : [...agentHarborMemoryKeys.project(wsId, projectId), "entries"] as const,
  settings: (wsId: string, projectId: string) =>
    [...agentHarborMemoryKeys.project(wsId, projectId), "settings"] as const,
  histories: (wsId: string, projectId: string) =>
    [...agentHarborMemoryKeys.project(wsId, projectId), "history"] as const,
  history: (wsId: string, projectId: string, entryId: string, revision: number) =>
    [...agentHarborMemoryKeys.histories(wsId, projectId), entryId, revision] as const,
};

export function agentHarborMemoryMaterialsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: agentHarborMemoryKeys.materials(wsId, projectId),
    queryFn: () => api.listAgentHarborMemoryMaterials(projectId),
    refetchInterval: 30_000,
  });
}

export function agentHarborMemoryEntriesOptions(wsId: string, projectId: string, scope: AgentHarborMemoryScope) {
  return queryOptions({
    queryKey: agentHarborMemoryKeys.entries(wsId, projectId, scope),
    queryFn: () => api.listAgentHarborMemoryEntries(projectId, scope),
  });
}

export function agentHarborMemorySettingsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: agentHarborMemoryKeys.settings(wsId, projectId),
    queryFn: () => api.getAgentHarborMemorySettings(projectId),
  });
}

export function agentHarborMemoryHistoryOptions(wsId: string, projectId: string, entryId: string, revision: number) {
  return queryOptions({
    queryKey: agentHarborMemoryKeys.history(wsId, projectId, entryId, revision),
    queryFn: () => api.getAgentHarborMemoryHistory(projectId, entryId),
    staleTime: 0,
    refetchInterval: 30_000,
  });
}
