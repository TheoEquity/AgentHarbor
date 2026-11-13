"use client";

import { useId, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "@multica/core/api";
import {
  agentHarborMemoryEntriesOptions,
  agentHarborMemoryHistoryOptions,
  agentHarborMemoryKeys,
  agentHarborMemoryMaterialsOptions,
  agentHarborMemorySettingsOptions,
} from "@multica/core/agentharbor/queries";
import type {
  AgentHarborMemoryEntry,
  AgentHarborMemoryMaterial,
  AgentHarborMemorySettings,
} from "@multica/core/api/schemas";
import { useCurrentWorkspace } from "@multica/core/paths";
import { projectKeys, projectListOptions } from "@multica/core/projects/queries";
import { BookOpenText, RefreshCw } from "@multica/ui/icons";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { Button } from "@multica/ui/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Switch } from "@multica/ui/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { useLocale, useT } from "../i18n";
import { PageHeader } from "../layout/page-header";
import { EntryEditor, MaterialReview } from "./project-memory-page-editor";

const EMPTY_ENTRIES: AgentHarborMemoryEntry[] = [];
const EMPTY_MATERIALS: AgentHarborMemoryMaterial[] = [];

export function ProjectMemoryPage() {
  const { t } = useT("layout");
  const wsId = useCurrentWorkspace()?.id ?? "";
  const queryClient = useQueryClient();
  const [selectedProjectId, setSelectedProjectId] = useState("");
  const projectsQuery = useQuery({ ...projectListOptions(wsId), enabled: !!wsId });
  const projects = projectsQuery.data ?? [];
  const project = projects.find((item) => item.id === selectedProjectId) ?? projects[0];
  const projectItems = projects.map((item) => ({ value: item.id, label: item.title }));

  function refresh() {
    void queryClient.invalidateQueries({ queryKey: projectKeys.list(wsId) });
    if (project) {
      void queryClient.invalidateQueries({ queryKey: agentHarborMemoryKeys.project(wsId, project.id) });
      void queryClient.invalidateQueries({ queryKey: agentHarborMemoryKeys.entries(wsId, project.id, "global") });
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <h1 className="flex min-w-0 flex-1 items-center gap-2 text-body font-medium">
          <BookOpenText aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate">{t(($) => $.nav.project_memory)}</span>
        </h1>
        <IconButton label={t(($) => $.project_memory.refresh)} onClick={refresh} disabled={projectsQuery.isFetching}>
          <RefreshCw />
        </IconButton>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-auto px-4 py-4">
        <div className="mx-auto max-w-4xl space-y-4">
          {projectsQuery.isError && <MemoryLoadError retry={() => void projectsQuery.refetch()} />}
          {projectsQuery.isPending ? (
            <MemoryMessage>{t(($) => $.project_memory.loading)}</MemoryMessage>
          ) : !project ? (
            !projectsQuery.isError && <MemoryMessage>{t(($) => $.project_memory.no_projects)}</MemoryMessage>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-3">
                <label htmlFor="memory-project" className="text-body font-medium">{t(($) => $.project_memory.project)}</label>
                <Select items={projectItems} value={project.id} onValueChange={(value) => {
                  if (value) setSelectedProjectId(value);
                }}>
                  <SelectTrigger id="memory-project" className="max-w-full min-w-52"><SelectValue /></SelectTrigger>
                  <SelectContent align="start">
                    {projectItems.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
              <ProjectMemory key={`${wsId}/${project.id}`} wsId={wsId} projectId={project.id} />
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function ProjectMemory({ wsId, projectId }: { wsId: string; projectId: string }) {
  const { t } = useT("layout");
  const queryClient = useQueryClient();
  const materialsQuery = useQuery(agentHarborMemoryMaterialsOptions(wsId, projectId));
  const projectQuery = useQuery(agentHarborMemoryEntriesOptions(wsId, projectId, "project"));
  const generalQuery = useQuery(agentHarborMemoryEntriesOptions(wsId, projectId, "global"));
  const materials = materialsQuery.data ?? EMPTY_MATERIALS;
  const projectEntries = projectQuery.data ?? EMPTY_ENTRIES;
  const generalEntries = generalQuery.data ?? EMPTY_ENTRIES;
  const pendingMaterials = materials.filter((material) => material.status === "pending");
  const activeEntries = [...projectEntries, ...generalEntries].filter((entry) => entry.status === "active");
  const sources = new Map(materials.map((material) => [material.id, material]));
  const [review, setReview] = useState<AgentHarborMemoryMaterial | null>(null);
  const [edit, setEdit] = useState<AgentHarborMemoryEntry | null>(null);

  async function refreshEntries() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: agentHarborMemoryKeys.entries(wsId, projectId, "project") }),
      queryClient.invalidateQueries({ queryKey: agentHarborMemoryKeys.general }),
    ]);
  }

  async function refreshMaterials() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: agentHarborMemoryKeys.materials(wsId, projectId) }),
      queryClient.invalidateQueries({ queryKey: agentHarborMemoryKeys.histories(wsId, projectId) }),
    ]);
  }

  const importLegacy = useMutation({
    mutationFn: () => api.importAgentHarborLegacyMemory(projectId),
    onSuccess: async (material) => {
      if (material) await refreshMaterials();
    },
  });

  return (
    <Tabs defaultValue="pending" className="gap-4">
      <TabsList aria-label={t(($) => $.nav.project_memory)} className="max-w-full overflow-x-auto">
        <TabsTrigger value="pending" className="px-3">{t(($) => $.project_memory.pending)}</TabsTrigger>
        <TabsTrigger value="project" className="px-3">{t(($) => $.project_memory.project_entries)}</TabsTrigger>
        <TabsTrigger value="global" className="px-3">{t(($) => $.project_memory.global_entries)}</TabsTrigger>
        <TabsTrigger value="settings" className="px-3">{t(($) => $.project_memory.settings)}</TabsTrigger>
      </TabsList>
      <TabsContent value="pending">
        <div className="mb-2 flex flex-wrap items-center justify-end gap-2">
          {importLegacy.isError && (
            <span role="alert" className="text-caption text-destructive">
              {t(($) => importLegacy.error instanceof ApiError && importLegacy.error.status === 503
                ? $.project_memory.legacy_unavailable
                : $.project_memory.legacy_error)}
            </span>
          )}
          {importLegacy.isSuccess && (
            <span role="status" className="text-caption text-muted-foreground">
              {t(($) => {
                if (importLegacy.data?.status === "pending") return $.project_memory.legacy_imported;
                if (importLegacy.data) return $.project_memory.legacy_reviewed;
                return $.project_memory.legacy_empty;
              })}
            </span>
          )}
          <Button type="button" variant="ghost" size="sm" disabled={importLegacy.isPending} aria-busy={importLegacy.isPending} onClick={() => importLegacy.mutate()}>
            {t(($) => $.project_memory.import_legacy)}
          </Button>
        </div>
        {materialsQuery.isError ? (
          <MemoryLoadError retry={() => void materialsQuery.refetch()} />
        ) : materialsQuery.isPending ? (
          <MemoryMessage>{t(($) => $.project_memory.loading)}</MemoryMessage>
        ) : pendingMaterials.length === 0 ? (
          <MemoryMessage>{t(($) => $.project_memory.no_pending)}</MemoryMessage>
        ) : (
          <ul className="space-y-2">
            {pendingMaterials.map((material) => (
              <PendingRow
                key={material.id}
                material={material}
                disabled={!!material.relatedEntryId && (projectQuery.isPending || generalQuery.isPending)}
                review={() => setReview(material)}
              />
            ))}
          </ul>
        )}
      </TabsContent>
      {(["project", "global"] as const).map((scope) => {
        const query = scope === "project" ? projectQuery : generalQuery;
        const entries = scope === "project" ? projectEntries : generalEntries;

        return (
          <TabsContent key={scope} value={scope}>
            {query.isError ? (
              <MemoryLoadError retry={() => void query.refetch()} />
            ) : query.isPending ? (
              <MemoryMessage>{t(($) => $.project_memory.loading)}</MemoryMessage>
            ) : entries.length === 0 ? (
              <MemoryMessage>{t(($) => scope === "project" ? $.project_memory.no_project_entries : $.project_memory.no_global_entries)}</MemoryMessage>
            ) : (
              <ul className="space-y-2">
                {entries.map((entry) => (
                  <EntryRow key={entry.id} wsId={wsId} projectId={projectId} entry={entry} sources={sources} edit={() => setEdit(entry)} />
                ))}
              </ul>
            )}
          </TabsContent>
        );
      })}
      <TabsContent value="settings">
        <ProjectMemorySettings wsId={wsId} projectId={projectId} />
      </TabsContent>
      {review && (
        <MaterialReview
          projectId={projectId}
          material={review}
          pendingMaterials={pendingMaterials}
          activeEntries={activeEntries}
          targetsUnavailable={projectQuery.isError || generalQuery.isError}
          onClose={() => setReview(null)}
          onConflict={() => Promise.all([refreshMaterials(), refreshEntries()])}
          onReviewed={async (result, input) => {
            const materialKey = agentHarborMemoryKeys.materials(wsId, projectId);
            const processedIds = new Set(input.action === "approve" ? input.additionalMaterialIds : []);
            queryClient.setQueryData<AgentHarborMemoryMaterial[]>(materialKey, (previous) => previous?.map((item) => {
              if (item.id === result.material.id) return result.material;
              return processedIds.has(item.id) ? { ...item, status: "processed" } : item;
            }));
            await Promise.all([refreshMaterials(), result.entry && refreshEntries()]);
          }}
        />
      )}
      {edit && (
        <EntryEditor
          projectId={projectId}
          entry={edit}
          current={activeEntries.find((entry) => entry.id === edit.id)}
          onClose={() => setEdit(null)}
          onConflict={refreshEntries}
          onUpdated={refreshEntries}
        />
      )}
    </Tabs>
  );
}

function PendingRow({ material, disabled, review }: {
  material: AgentHarborMemoryMaterial;
  disabled: boolean;
  review: () => void;
}) {
  const { t } = useT("layout");
  const locale = useLocale();
  const created = new Date(material.createdAt);
  return (
    <li className="flex flex-wrap items-start justify-between gap-3 rounded-xl border border-surface-border bg-card px-4 py-3">
      <div className="min-w-0 flex-1 space-y-1">
        <p className="line-clamp-2 whitespace-pre-wrap wrap-anywhere">{material.sourceText}</p>
        {!Number.isNaN(created.getTime()) && (
          <time dateTime={material.createdAt} className="text-caption text-muted-foreground">{created.toLocaleString(locale)}</time>
        )}
      </div>
      <Button type="button" variant="outline" size="sm" disabled={disabled} onClick={review}>{t(($) => $.project_memory.review)}</Button>
    </li>
  );
}

function EntryRow({ wsId, projectId, entry, sources, edit }: {
  wsId: string;
  projectId: string;
  entry: AgentHarborMemoryEntry;
  sources: Map<string, AgentHarborMemoryMaterial>;
  edit: () => void;
}) {
  const { t } = useT("layout");
  const status = entry.status;
  return (
    <li className="space-y-2 rounded-xl border border-surface-border bg-card px-4 py-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <h2 className="wrap-anywhere text-title-sm font-medium">{entry.title}</h2>
          <div className="flex flex-wrap gap-x-3 text-caption text-muted-foreground">
            <span>{t(($) => $.project_memory.types[entry.type])}</span>
            <span>{t(($) => $.project_memory.revision, { revision: entry.revision })}</span>
            {status !== "active" && <span>{t(($) => $.project_memory.status[status])}</span>}
          </div>
        </div>
        {entry.status === "active" && (
          <Button type="button" variant="outline" size="sm" onClick={edit}>{t(($) => $.project_memory.edit)}</Button>
        )}
      </div>
      <p className="whitespace-pre-wrap wrap-anywhere">{entry.content}</p>
      <EntryProvenance wsId={wsId} projectId={projectId} entry={entry} projectSources={sources} />
    </li>
  );
}

function EntryProvenance({ wsId, projectId, entry, projectSources }: {
  wsId: string;
  projectId: string;
  entry: AgentHarborMemoryEntry;
  projectSources: Map<string, AgentHarborMemoryMaterial>;
}) {
  const { t } = useT("layout");
  const locale = useLocale();
  const panelId = useId();
  const [panel, setPanel] = useState<"sources" | "history" | null>(null);
  const query = useQuery({
    ...agentHarborMemoryHistoryOptions(wsId, projectId, entry.id, entry.revision),
    enabled: panel !== null,
  });
  const response = query.data;
  const sources = response
    ? new Map(response.materials.map((source) => [source.id, source]))
    : projectSources;

  return (
    <div className="space-y-2 text-caption">
      <div className="flex flex-wrap gap-3">
        {entry.sourceMaterialIds.length > 0 && (
          <Button
            type="button"
            size="xs"
            variant="ghost"
            aria-expanded={panel === "sources"}
            aria-controls={panelId}
            onClick={() => setPanel(panel === "sources" ? null : "sources")}
          >
            {t(($) => $.project_memory.sources)}
          </Button>
        )}
        <Button
          type="button"
          size="xs"
          variant="ghost"
          aria-expanded={panel === "history"}
          aria-controls={panelId}
          onClick={() => setPanel(panel === "history" ? null : "history")}
        >
          {t(($) => $.project_memory.history)}
        </Button>
      </div>
      <div id={panelId} hidden={panel === null} className="space-y-2">
        {panel && query.isPending && <p role="status" className="text-muted-foreground">{t(($) => $.project_memory.loading_history)}</p>}
        {panel && query.isError && (
          <MemoryLoadError message={t(($) => $.project_memory.history_error)} retry={() => void query.refetch()} />
        )}
        {panel && !!response?.disputes.length && (
          <div className="rounded-lg border border-surface-border p-2">
            <p className="font-medium">{t(($) => $.project_memory.dispute_pending)}</p>
            <ul className="space-y-2">
              {response.disputes.filter((report) => report.sourceText).map((report) => (
                <li key={report.id} className="whitespace-pre-wrap wrap-anywhere">
                  <p>{report.sourceText}</p>
                  {report.context && <p className="text-muted-foreground">{report.context}</p>}
                </li>
              ))}
            </ul>
          </div>
        )}
        {panel === "sources" && (
          <SourceList
            sourceIds={query.isPending ? entry.sourceMaterialIds.filter((id) => sources.has(id)) : entry.sourceMaterialIds}
            sources={sources}
          />
        )}
        {panel === "history" && response && (
          response.revisions.length === 0 ? (
            <p className="text-muted-foreground">{t(($) => $.project_memory.no_history)}</p>
          ) : (
            <ol className="space-y-2">
              {[...response.revisions].sort((first, second) => second.revision - first.revision).map((revision) => {
                const status = revision.status;
                const createdAt = new Date(revision.createdAt);
                return (
                  <li key={revision.revision} className="rounded-lg border border-surface-border p-2">
                    <details>
                      <summary className="cursor-pointer wrap-anywhere">
                        <span className="font-medium">{t(($) => $.project_memory.revision, { revision: revision.revision })} · {revision.title}</span>
                        {!Number.isNaN(createdAt.getTime()) && (
                          <time className="ms-2 text-muted-foreground" dateTime={revision.createdAt}>{createdAt.toLocaleString(locale)}</time>
                        )}
                      </summary>
                      <div className="mt-2 space-y-2">
                        <div className="flex flex-wrap gap-x-3 text-muted-foreground">
                          <span>{t(($) => $.project_memory.types[revision.type])}</span>
                          {status !== "active" && <span>{t(($) => $.project_memory.status[status])}</span>}
                        </div>
                        <p className="whitespace-pre-wrap wrap-anywhere text-body">{revision.content}</p>
                        {revision.sourceMaterialIds.length > 0 && (
                          <div className="space-y-1">
                            <p className="font-medium">{t(($) => $.project_memory.sources)}</p>
                            <SourceList sourceIds={revision.sourceMaterialIds} sources={sources} />
                          </div>
                        )}
                      </div>
                    </details>
                  </li>
                );
              })}
            </ol>
          )
        )}
      </div>
    </div>
  );
}

function SourceList({ sourceIds, sources }: {
  sourceIds: string[];
  sources: Map<string, AgentHarborMemoryMaterial>;
}) {
  const { t } = useT("layout");
  if (sourceIds.length === 0) return null;
  return (
    <ul className="space-y-2">
      {sourceIds.map((id) => {
        const source = sources.get(id);
        return (
          <li key={id} className="border-l border-surface-border pl-2">
            {source ? (
              <p className="whitespace-pre-wrap wrap-anywhere text-foreground">{source.sourceText}</p>
            ) : (
              <span className="wrap-anywhere text-muted-foreground">{t(($) => $.project_memory.source_unavailable, { id })}</span>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function ProjectMemorySettings({ wsId, projectId }: { wsId: string; projectId: string }) {
  const { t } = useT("layout");
  const queryClient = useQueryClient();
  const query = useQuery(agentHarborMemorySettingsOptions(wsId, projectId));
  const [draft, setDraft] = useState<AgentHarborMemorySettings | null>(null);
  const save = useMutation({
    mutationFn: (settings: AgentHarborMemorySettings) => api.putAgentHarborMemorySettings(projectId, settings),
    onSuccess: (settings) => {
      queryClient.setQueryData(agentHarborMemoryKeys.settings(wsId, projectId), settings);
      setDraft(null);
    },
  });
  if (query.isError) return <MemoryLoadError retry={() => void query.refetch()} />;
  if (!query.data) return <MemoryMessage>{t(($) => $.project_memory.loading)}</MemoryMessage>;

  const settings = draft ?? query.data;
  const saved = query.data;
  const fields = [
    { key: "recordEnabled", label: t(($) => $.project_memory.record_enabled), description: t(($) => $.project_memory.record_note) },
    { key: "cloudEnabled", label: t(($) => $.project_memory.cloud_enabled), description: t(($) => $.project_memory.cloud_note) },
    { key: "useMemory", label: t(($) => $.project_memory.use_memory), description: t(($) => $.project_memory.use_memory_note) },
    { key: "useGlobal", label: t(($) => $.project_memory.use_global), disabled: !settings.useMemory },
  ] as const;
  const changed = fields.some(({ key }) => settings[key] !== saved[key]);

  return (
    <form className="space-y-4 rounded-xl border border-surface-border bg-card p-4" onSubmit={(event) => {
      event.preventDefault();
      if (changed && !save.isPending) save.mutate(settings);
    }}>
      {fields.map(({ key, label, ...field }) => (
        <SettingRow
          key={key}
          label={label}
          description={"description" in field ? field.description : undefined}
          disabled={save.isPending || ("disabled" in field && field.disabled)}
          checked={settings[key]}
          onChange={(checked) => {
            setDraft({ ...settings, [key]: checked });
            save.reset();
          }}
        />
      ))}
      {save.isError && <p role="alert" className="text-destructive">{t(($) => $.project_memory.save_error)}</p>}
      {save.isSuccess && <p role="status" className="text-muted-foreground">{t(($) => $.project_memory.settings_saved)}</p>}
      <div className="flex justify-end">
        <Button type="submit" size="sm" disabled={!changed || save.isPending} aria-busy={save.isPending}>
          {t(($) => $.project_memory.save_settings)}
        </Button>
      </div>
    </form>
  );
}

function SettingRow({ label, description, disabled, checked, onChange }: {
  label: string;
  description?: string;
  disabled: boolean;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  const id = useId();
  const descriptionId = useId();
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="min-w-0 space-y-1">
        <label htmlFor={id} className="font-medium">{label}</label>
        {description && <p id={descriptionId} className="text-caption text-muted-foreground">{description}</p>}
      </div>
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onChange}
        disabled={disabled}
        aria-describedby={description ? descriptionId : undefined}
      />
    </div>
  );
}

function MemoryMessage({ children }: { children: ReactNode }) {
  return <p className="py-10 text-center text-body text-muted-foreground">{children}</p>;
}

function MemoryLoadError({ message, retry }: { message?: string; retry: () => void }) {
  const { t } = useT("layout");
  return (
    <div role="alert" className="flex flex-wrap items-center gap-2 text-body text-destructive">
      <span>{message ?? t(($) => $.project_memory.load_error)}</span>
      <Button type="button" variant="ghost" size="sm" onClick={retry}>{t(($) => $.agent_tasks.retry)}</Button>
    </div>
  );
}
