import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

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

export const agentHarborMemoryKeys = {
  project: (wsId: string, projectId: string) => ["agentharbor-memory", wsId, projectId] as const,
};

export function agentHarborMemoryOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: agentHarborMemoryKeys.project(wsId, projectId),
    queryFn: () => api.getAgentHarborMemory(projectId),
    refetchInterval: 30_000,
  });
}
