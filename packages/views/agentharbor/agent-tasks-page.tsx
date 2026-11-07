"use client";

import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Monitor, RefreshCw } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { IconButton } from "@multica/ui/components/common/icon-button";
import type { AgentHarborTask } from "@multica/core/api/schemas";
import { agentHarborTaskKeys, agentHarborTasksOptions } from "@multica/core/agentharbor/queries";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { projectKeys, projectListOptions } from "@multica/core/projects/queries";
import type { Project } from "@multica/core/types";
import { useT } from "../i18n";
import { AppLink } from "../navigation";
import { PageHeader } from "../layout/page-header";

const EMPTY_PROJECTS: Project[] = [];

export function AgentTasksPage() {
  const { t, i18n } = useT("layout");
  const wsId = useCurrentWorkspace()?.id ?? "";
  const paths = useWorkspacePaths();
  const queryClient = useQueryClient();
  const projectQuery = useQuery({ ...projectListOptions(wsId), enabled: !!wsId });
  const projects = projectQuery.data ?? EMPTY_PROJECTS;
  const taskQueries = useQueries({
    queries: projects.map((project) => ({
      ...agentHarborTasksOptions(wsId, project.id),
      enabled: !!wsId,
    })),
  });

  const rows = projects.flatMap((project, index) =>
    (taskQueries[index]?.data?.tasks ?? []).map((task) => ({ project, task })),
  ).sort((a, b) =>
    (Date.parse(b.task.last_received_at) || 0) - (Date.parse(a.task.last_received_at) || 0),
  );
  const loading = projectQuery.isPending || taskQueries.some((query) => query.isPending);
  const error = projectQuery.isError || taskQueries.some((query) => query.isError);
  const fetching = projectQuery.isFetching || taskQueries.some((query) => query.isFetching);

  const refresh = () => {
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: projectKeys.list(wsId) }),
      queryClient.invalidateQueries({ queryKey: agentHarborTaskKeys.all(wsId) }),
    ]);
  };
  const formatTime = (value: string) => {
    const date = new Date(value);
    return value && !Number.isNaN(date.getTime()) ? date.toLocaleString(i18n.language) : "—";
  };
  const statusLabel = (task: AgentHarborTask) => {
    switch (task.status) {
      case "running":
        return t(($) => $.agent_tasks.status_started);
      case "interrupted":
        return t(($) => $.agent_tasks.status_interrupted);
      case "turn_ended_unknown":
        return t(($) => $.agent_tasks.status_ended_unknown);
      default:
        return t(($) => $.agent_tasks.status_unknown);
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <h1 className="flex min-w-0 flex-1 items-center gap-2 text-body font-medium">
          <Monitor aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate">{t(($) => $.nav.agent_tasks)}</span>
        </h1>
        <IconButton label={t(($) => $.agent_tasks.refresh)} disabled={fetching} onClick={refresh}>
          <RefreshCw />
        </IconButton>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-auto px-4 py-4">
        <p className="mb-4 text-caption text-muted-foreground">{t(($) => $.agent_tasks.source_note)}</p>
        {error && (
          <div role="alert" className="mb-4 flex items-center gap-3 text-body text-destructive">
            <span>{t(($) => $.agent_tasks.error)}</span>
            <Button type="button" variant="ghost" size="sm" onClick={refresh}>
              {t(($) => $.agent_tasks.retry)}
            </Button>
          </div>
        )}
        {loading ? (
          <p className="py-12 text-center text-body text-muted-foreground">{t(($) => $.agent_tasks.loading)}</p>
        ) : rows.length === 0 ? (
          !error && <p className="py-12 text-center text-body text-muted-foreground">{t(($) => $.agent_tasks.empty)}</p>
        ) : (
          <div className="overflow-x-auto rounded-xl border border-surface-border">
            <table className="w-full min-w-[800px] border-collapse text-left text-body">
              <thead className="bg-surface-hover text-caption text-muted-foreground">
                <tr>
                  <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.project)}</th>
                  <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.machine)}</th>
                  <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.agent)}</th>
                  <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.status)}</th>
                  <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.occurred)}</th>
                  <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.received)}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map(({ project, task }) => (
                  <tr key={`${project.id}/${task.machine_id}/${task.agent_type}/${task.session_id}`} className="border-t border-surface-border align-top">
                    <td className="px-4 py-3 font-medium">
                      <AppLink href={paths.projectDetail(project.id)} className="hover:underline focus-visible:outline-2 focus-visible:outline-ring">
                        {project.title}
                      </AppLink>
                    </td>
                    <td className="px-4 py-3" title={task.machine_id}>{task.machine_name || task.machine_id.slice(0, 8)}</td>
                    <td className="px-4 py-3">
                      <span className="block">{task.agent_type === "codex" ? "Codex" : task.agent_type}</span>
                      <span className="block text-caption text-muted-foreground" title={task.session_id}>{task.session_id.slice(0, 8)}</span>
                    </td>
                    <td className="px-4 py-3"><span className="inline-block rounded-full bg-surface-hover px-2 py-0.5 text-caption">{statusLabel(task)}</span></td>
                    <td className="whitespace-nowrap px-4 py-3"><time dateTime={task.last_occurred_at}>{formatTime(task.last_occurred_at)}</time></td>
                    <td className="whitespace-nowrap px-4 py-3"><time dateTime={task.last_received_at}>{formatTime(task.last_received_at)}</time></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
