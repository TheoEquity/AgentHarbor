"use client";

import { useId, useState, type Dispatch, type SetStateAction } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { AgentHarborMonitorResponse, AgentHarborMonitorTask, AgentHarborMonitorTaskId } from "@multica/core/api/schemas";
import { agentHarborMonitorKeys, agentHarborMonitorOptions } from "@multica/core/agentharbor/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import { projectListOptions } from "@multica/core/projects/queries";
import type { Project } from "@multica/core/types";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { copyText } from "@multica/ui/lib/clipboard";
import { useLocale, useT } from "../i18n";
import { AppLink } from "../navigation";
import { isDesktopShell } from "../platform/local-directory";

const CODEX_SESSION_UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[1-8][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i;

type MonitorView = "active" | "history";
type SelectItemOption = { value: string; label: string };
type MonitorSource = AgentHarborMonitorResponse["sources"][number];

const isFinished = (task: AgentHarborMonitorTask) => task.status === "completed" || task.status === "aborted";

export function CodexTaskMonitor({ wsId, view }: { wsId: string; view: MonitorView }) {
  const { t } = useT("layout");
  const [projectId, setProjectId] = useState("");
  const [machineId, setMachineId] = useState("");
  const [historyPage, setHistoryPage] = useState(1);
  const projects = useQuery(projectListOptions(wsId));
  const sourceList = useQuery(agentHarborMonitorOptions(wsId, { view: "active", page: 1 }));
  const monitor = useQuery(agentHarborMonitorOptions(wsId, {
    view, projectId: projectId || undefined, machineId: machineId || undefined,
    page: view === "history" ? historyPage : 1,
  }));
  const { projectNames, machineNames } = monitorSourceNames(projects.data, sourceList.data?.sources, projectId);
  const projectItems = [
    { value: "", label: t(($) => $.agent_tasks.monitor.all_projects) },
    ...Array.from(projectNames, ([value, label]) => ({ value, label })),
  ];
  const machineItems = [
    { value: "", label: t(($) => $.agent_tasks.monitor.all_machines) },
    ...Array.from(machineNames, ([value, label]) => ({ value, label })),
  ];
  const visibleSources = monitor.data?.sources.filter((source) => !machineId || source.machineId === machineId) ?? [];
  const tasks = monitor.data?.tasks ?? [];
  const running = tasks.filter((task) => task.status === "running");
  const waiting = tasks.filter((task) => isFinished(task) && task.handled === false);
  const hasFilterError = projects.isError || sourceList.isError;
  const error = monitor.isError || hasFilterError;

  function retry() {
    if (projects.isError) void projects.refetch();
    if (sourceList.isError && (view === "history" || projectId || machineId)) void sourceList.refetch();
    if (monitor.isError) void monitor.refetch();
  }

  return (
    <div className="space-y-4 pt-2">
      <div className="flex flex-wrap items-end gap-3">
        <MonitorFilter label={t(($) => $.agent_tasks.project)} items={projectItems} value={projectId}
          onChange={(value) => {
            setProjectId(value);
            setMachineId("");
            setHistoryPage(1);
          }}
        />
        <MonitorFilter label={t(($) => $.agent_tasks.machine)} items={machineItems} value={machineId}
          onChange={(value) => {
            setMachineId(value);
            setHistoryPage(1);
          }}
        />
      </div>

      {error && (
        <div role="alert" className="flex flex-wrap items-center gap-2 text-destructive">
          <span>{t(($) => $.agent_tasks.monitor.error)}</span>
          <Button type="button" variant="ghost" size="sm" onClick={retry}
            disabled={projects.isFetching || sourceList.isFetching || monitor.isFetching}
          >
            {t(($) => $.agent_tasks.retry)}
          </Button>
        </div>
      )}
      <MonitorSyncStatus sources={visibleSources} projectNames={projectNames}
        refreshFailed={monitor.isError || monitor.fetchStatus === "paused"}
      />
      {monitor.isPending && <p role="status" className="py-8 text-muted-foreground">{t(($) => $.agent_tasks.loading)}</p>}
      {monitor.data && (view === "active" ? (
        <div className="space-y-5">
          <section className="space-y-2">
            <h2 className="text-title-sm font-medium">{t(($) => $.agent_tasks.monitor.running)}</h2>
            <MonitorTaskTable tasks={running} wsId={wsId} view={view} />
          </section>
          <section className="space-y-2">
            <h2 className="text-title-sm font-medium">{t(($) => $.agent_tasks.monitor.pending)}</h2>
            <MonitorTaskTable tasks={waiting} wsId={wsId} view={view} />
          </section>
        </div>
      ) : <MonitorTaskTable tasks={tasks} wsId={wsId} view={view} />)}

      {view === "history" && (
        <MonitorHistoryPagination page={historyPage} setPage={setHistoryPage} hasMore={monitor.data?.hasMore}
          pending={monitor.isPending}
        />
      )}
    </div>
  );
}

function monitorSourceNames(projects: Project[] | undefined, sources: MonitorSource[] | undefined, projectId: string) {
  const projectNames = new Map(projects?.map(({ id, title }) => [id, title]));
  const machineNames = new Map<string, string>();
  for (const source of sources ?? []) {
    if (!projectNames.has(source.projectId)) projectNames.set(source.projectId, source.projectId);
    if (!projectId || source.projectId === projectId) {
      machineNames.set(source.machineId, source.machineName || source.machineId.slice(0, 8));
    }
  }
  return { projectNames, machineNames };
}

function MonitorHistoryPagination({ page, setPage, hasMore, pending }: {
  page: number;
  setPage: Dispatch<SetStateAction<number>>;
  hasMore?: boolean;
  pending: boolean;
}) {
  const { t } = useT("layout");
  if (page <= 1 && !hasMore) return null;

  return (
    <div className="flex flex-wrap items-center justify-end gap-2">
      <Button type="button" variant="outline" size="sm" disabled={page <= 1 || pending}
        onClick={() => setPage((current) => Math.max(1, current - 1))}
      >
        {t(($) => $.agent_tasks.monitor.previous_page)}
      </Button>
      <span className="text-caption text-muted-foreground">{t(($) => $.agent_tasks.monitor.page, { page })}</span>
      <Button type="button" variant="outline" size="sm" disabled={!hasMore || pending}
        onClick={() => setPage((current) => current + 1)}
      >
        {t(($) => $.agent_tasks.monitor.next_page)}
      </Button>
    </div>
  );
}

function MonitorFilter({ label, items, value, onChange }: {
  label: string;
  items: SelectItemOption[];
  value: string;
  onChange: (value: string) => void;
}) {
  const id = useId();
  return (
    <div className="min-w-0 space-y-1">
      <label htmlFor={id} className="block text-caption font-medium text-muted-foreground">{label}</label>
      <Select items={items} value={value} onValueChange={(next) => onChange(next ?? "")} disabled={items.length < 2}>
        <SelectTrigger id={id} className="w-48 max-w-full"><SelectValue /></SelectTrigger>
        <SelectContent align="start" alignItemWithTrigger={false}>
          {items.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}
        </SelectContent>
      </Select>
    </div>
  );
}

function MonitorTime({ value }: { value: string | null }) {
  const locale = useLocale();
  return value ? <time dateTime={value}>{new Date(value).toLocaleString(locale)}</time> : <span>&mdash;</span>;
}

function MonitorSyncStatus({ sources, projectNames, refreshFailed }: {
  sources: MonitorSource[];
  projectNames: Map<string, string>;
  refreshFailed: boolean;
}) {
  const { t } = useT("layout");
  const latest = sources.reduce<string | null>((value, source) =>
    !value || Date.parse(source.lastSyncedAt) > Date.parse(value) ? source.lastSyncedAt : value, null);
  const stale = refreshFailed || sources.some((source) => source.stale);
  if (!latest) return null;

  return (
    <details className="rounded-lg border border-surface-border px-3 py-2 text-caption text-muted-foreground">
      <summary className="cursor-pointer wrap-anywhere focus-visible:outline-2 focus-visible:outline-ring">
        <span className="ml-1 font-medium text-foreground">{t(($) => $.agent_tasks.monitor.last_sync)}</span>{" "}
        <MonitorTime value={latest} />
        {stale && <span className="ml-2 font-medium text-destructive">{t(($) => $.agent_tasks.monitor.stale)}</span>}
      </summary>
      <ul className="mt-2 space-y-1 pl-4">
        {sources.map((source) => (
          <li key={`${source.projectId}/${source.machineId}`} className="flex flex-wrap gap-x-3 wrap-anywhere">
            <span className="text-foreground">{projectNames.get(source.projectId) || source.projectId} &middot; {source.machineName || source.machineId.slice(0, 8)}</span>
            <MonitorTime value={source.lastSyncedAt} />
            {source.stale && <span className="text-destructive">{t(($) => $.agent_tasks.monitor.source_stale)}</span>}
          </li>
        ))}
      </ul>
    </details>
  );
}

function MonitorTaskTable({ tasks, wsId, view }: { tasks: AgentHarborMonitorTask[]; wsId: string; view: MonitorView }) {
  const { t } = useT("layout");
  if (tasks.length === 0) {
    return <p className="rounded-lg border border-surface-border px-4 py-5 text-body text-muted-foreground">{t(($) => $.agent_tasks.monitor.empty)}</p>;
  }
  return (
    <div className="overflow-x-auto rounded-xl border border-surface-border">
      <table className="w-full min-w-[700px] border-collapse text-left text-body">
        <thead className="bg-surface-hover text-caption text-muted-foreground">
          <tr>
            <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.monitor.task)}</th>
            <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.project)}</th>
            <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.machine)}</th>
            <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.status)}</th>
            <th scope="col" className="px-4 py-3 font-medium">{t(($) => $.agent_tasks.monitor.updated)}</th>
            <th scope="col" className="px-4 py-3 font-medium"><span className="sr-only">{t(($) => $.agent_tasks.monitor.actions)}</span></th>
          </tr>
        </thead>
        <tbody>
          {tasks.map((task) => <MonitorTaskRow key={`${task.projectId}/${task.machineId}/${task.sessionId}/${task.turnId}`} task={task} wsId={wsId} view={view} />)}
        </tbody>
      </table>
    </div>
  );
}

function MonitorTaskRow({ task, wsId, view }: { task: AgentHarborMonitorTask; wsId: string; view: MonitorView }) {
  const { t } = useT("layout");
  const paths = useWorkspacePaths();
  const queryClient = useQueryClient();
  const match = (item: AgentHarborMonitorTask) => task.projectId === item.projectId && task.machineId === item.machineId
    && task.sessionId === item.sessionId && task.turnId === item.turnId;
  const handle = useMutation({
    mutationFn: (id: AgentHarborMonitorTaskId) => api.handleAgentHarborMonitorTask(id, wsId),
    onSuccess: () => {
      queryClient.setQueriesData<AgentHarborMonitorResponse>({ queryKey: agentHarborMonitorKeys.view(wsId, "active") },
        (data) => data && { ...data, tasks: data.tasks.filter((item) => !match(item)) },
      );
      queryClient.setQueriesData<AgentHarborMonitorResponse>({ queryKey: agentHarborMonitorKeys.view(wsId, "history") },
        (data) => data && { ...data, tasks: data.tasks.map((item) => match(item) ? { ...item, handled: true } : item) },
      );
      void queryClient.invalidateQueries({ queryKey: agentHarborMonitorKeys.lists(wsId) });
    },
  });
  const status = t(($) => $.agent_tasks.monitor.result[task.status]);
  const handling = task.handled === true ? "handled" : task.handled === false ? "unhandled" : "handling_unknown";

  return (
    <tr className="border-t border-surface-border align-top">
      <th scope="row" className="max-w-72 px-4 py-3 text-left font-medium wrap-anywhere">
        {task.title || t(($) => $.agent_tasks.monitor.untitled)}
      </th>
      <td className="max-w-48 px-4 py-3 wrap-anywhere">
        <AppLink href={paths.projectDetail(task.projectId)} className="hover:underline focus-visible:outline-2 focus-visible:outline-ring">
          {task.projectName || task.projectId}
        </AppLink>
      </td>
      <td className="max-w-36 px-4 py-3 wrap-anywhere" title={task.machineId}>{task.machineName || task.machineId.slice(0, 8)}</td>
      <td className="px-4 py-3">
        <span className="inline-block rounded-full bg-surface-hover px-2 py-0.5 text-caption">{status}</span>
        {view === "history" && task.status !== "running" && (
          <span className="mt-1 block text-caption text-muted-foreground">{t(($) => $.agent_tasks.monitor[handling])}</span>
        )}
      </td>
      <td className="px-4 py-3 whitespace-nowrap"><MonitorTime value={task.endedAt ?? task.startedAt} /></td>
      <td className="px-4 py-2">
        <CodexSessionActions sessionId={task.sessionId} />
        {view === "active" && isFinished(task) && task.handled === false && (
          <Button type="button" size="xs" variant="outline" disabled={handle.isPending} aria-busy={handle.isPending}
            onClick={() => handle.mutate({
              projectId: task.projectId, machineId: task.machineId, sessionId: task.sessionId, turnId: task.turnId,
            })}
          >
            {handle.isPending && <Spinner />}
            {t(($) => $.agent_tasks.monitor.mark_handled)}
          </Button>
        )}
        {handle.isError && <p role="alert" className="text-caption text-destructive">{t(($) => $.agent_tasks.monitor.handle_error)}</p>}
      </td>
    </tr>
  );
}

export function CodexSessionActions({ sessionId }: { sessionId: string }) {
  const { t } = useT("layout");
  const [copyResult, setCopyResult] = useState<"copied" | "copy_error">();
  const codexUrl = !isDesktopShell() && CODEX_SESSION_UUID.test(sessionId) ? `codex://threads/${sessionId}` : undefined;

  return (
    <>
      <div className="flex flex-wrap gap-1">
        {codexUrl && (
          <a href={codexUrl} className={buttonVariants({ variant: "ghost", size: "xs" })}>
            {t(($) => $.agent_tasks.monitor.open_codex)}
          </a>
        )}
        <Button type="button" variant="ghost" size="xs" onClick={async () => {
          setCopyResult(await copyText(sessionId) ? "copied" : "copy_error");
        }}>
          {t(($) => $.agent_tasks.monitor.copy_session)}
        </Button>
      </div>
      {copyResult && <p role="status" className="text-caption text-muted-foreground">{t(($) => $.agent_tasks.monitor[copyResult])}</p>}
    </>
  );
}
