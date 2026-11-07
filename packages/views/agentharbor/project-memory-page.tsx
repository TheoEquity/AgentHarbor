"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { agentHarborMemoryKeys, agentHarborMemoryOptions } from "@multica/core/agentharbor/queries";
import { useCurrentWorkspace } from "@multica/core/paths";
import { projectKeys, projectListOptions } from "@multica/core/projects/queries";
import type { Project } from "@multica/core/types";
import { BookOpenText, RefreshCw } from "@multica/ui/icons";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { Button } from "@multica/ui/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n";
import { PageHeader } from "../layout/page-header";

const EMPTY_PROJECTS: Project[] = [];
const MAX_MEMORY_BYTES = 16 * 1024;

export function ProjectMemoryPage() {
  const { t } = useT("layout");
  const wsId = useCurrentWorkspace()?.id ?? "";
  const queryClient = useQueryClient();
  const [selectedProjectId, setSelectedProjectId] = useState("");
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const projectQuery = useQuery({ ...projectListOptions(wsId), enabled: !!wsId });
  const projects = projectQuery.data ?? EMPTY_PROJECTS;
  const project = projects.find((item) => item.id === selectedProjectId) ?? projects[0];
  const projectId = project?.id ?? "";
  const memoryQuery = useQuery({ ...agentHarborMemoryOptions(wsId, projectId), enabled: !!wsId && !!projectId });
  const content = memoryQuery.data?.content ?? "";
  const memoryKey = agentHarborMemoryKeys.project(wsId, projectId);
  const save = useMutation({
    mutationFn: (value: string) => api.putAgentHarborMemory(projectId, value),
    onSuccess: (snapshot) => {
      queryClient.setQueryData(memoryKey, snapshot);
      setEditing(false);
    },
  });
  const draftBytes = new TextEncoder().encode(draft).length;
  const canSave = draft.trim().length > 0 && draft !== content && draftBytes <= MAX_MEMORY_BYTES && !save.isPending;
  const projectItems = projects.map((item) => ({ value: item.id, label: item.title }));

  const refresh = () => {
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: projectKeys.list(wsId) }),
      projectId ? queryClient.invalidateQueries({ queryKey: memoryKey }) : Promise.resolve(),
    ]);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <h1 className="flex min-w-0 flex-1 items-center gap-2 text-body font-medium">
          <BookOpenText aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate">{t(($) => $.nav.project_memory)}</span>
        </h1>
        <IconButton label={t(($) => $.project_memory.refresh)} disabled={projectQuery.isFetching || memoryQuery.isFetching || editing} onClick={refresh}>
          <RefreshCw />
        </IconButton>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-auto px-4 py-4">
        {(projectQuery.isError || memoryQuery.isError) && (
          <div role="alert" className="mb-4 flex items-center gap-3 text-body text-destructive">
            <span>{t(($) => $.project_memory.load_error)}</span>
            <Button type="button" variant="ghost" size="sm" onClick={refresh}>{t(($) => $.agent_tasks.retry)}</Button>
          </div>
        )}
        {projectQuery.isPending ? (
          <p className="py-12 text-center text-body text-muted-foreground">{t(($) => $.project_memory.loading)}</p>
        ) : !project ? (
          !projectQuery.isError && <p className="py-12 text-center text-body text-muted-foreground">{t(($) => $.project_memory.no_projects)}</p>
        ) : (
          <div className="mx-auto max-w-5xl space-y-5">
            <div className="flex flex-wrap items-center gap-3">
              <span className="text-body font-medium">{t(($) => $.project_memory.project)}</span>
              <Select
                items={projectItems}
                value={projectId}
                onValueChange={(value) => { if (value) setSelectedProjectId(value); }}
                disabled={editing}
              >
                <SelectTrigger aria-label={t(($) => $.project_memory.project)} className="max-w-full min-w-52">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent align="start">
                  {projectItems.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>

            <ol className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {(["l0", "l1", "l2", "l3"] as const).map((layer) => (
                <li key={layer} className={`rounded-xl border p-3 ${layer === "l3" ? "border-ring bg-card" : "border-surface-border"}`}>
                  <div className="text-label font-medium">{t(($) => $.project_memory[layer])}</div>
                  <div className="mt-2 text-caption text-muted-foreground">
                    {t(($) => layer === "l3" ? $.project_memory.available : $.project_memory.unavailable)}
                  </div>
                </li>
              ))}
            </ol>

            <section aria-labelledby="core-memory-heading" className="rounded-xl border border-surface-border bg-card p-4">
              <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                <h2 id="core-memory-heading" className="text-title font-medium">{t(($) => $.project_memory.l3)}</h2>
                {!editing && !memoryQuery.isPending && !memoryQuery.isError && (
                  <Button type="button" variant="outline" size="sm" onClick={() => { setDraft(content); setEditing(true); save.reset(); }}>
                    {t(($) => content ? $.project_memory.edit : $.project_memory.add)}
                  </Button>
                )}
              </div>
              {memoryQuery.isPending ? (
                <p className="text-body text-muted-foreground">{t(($) => $.project_memory.loading)}</p>
              ) : memoryQuery.isError ? null : editing ? (
                <div className="space-y-3">
                  <Textarea
                    aria-label={t(($) => $.project_memory.l3)}
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                    className="min-h-72 resize-y whitespace-pre-wrap"
                  />
                  <p className="text-caption text-muted-foreground">{t(($) => $.project_memory.replace_note)}</p>
                  {draftBytes > MAX_MEMORY_BYTES && <p role="alert" className="text-caption text-destructive">{t(($) => $.project_memory.too_long)}</p>}
                  {save.isError && <p role="alert" className="text-caption text-destructive">{t(($) => $.project_memory.save_error)}</p>}
                  <div className="flex justify-end gap-2">
                    <Button type="button" variant="outline" size="sm" disabled={save.isPending} onClick={() => setEditing(false)}>{t(($) => $.project_memory.cancel)}</Button>
                    <Button type="button" size="sm" disabled={!canSave} aria-busy={save.isPending} onClick={() => save.mutate(draft)}>
                      {save.isPending ? t(($) => $.project_memory.saving) : t(($) => $.project_memory.save)}
                    </Button>
                  </div>
                </div>
              ) : content ? (
                <div className="whitespace-pre-wrap break-words text-body leading-6">{content}</div>
              ) : (
                <p className="text-body text-muted-foreground">{t(($) => $.project_memory.empty)}</p>
              )}
            </section>
          </div>
        )}
      </div>
    </div>
  );
}
