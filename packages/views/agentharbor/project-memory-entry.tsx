"use client";

import { useId, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentHarborMemoryHistoryOptions } from "@multica/core/agentharbor/queries";
import type { AgentHarborMemoryEntry, AgentHarborMemoryMaterial } from "@multica/core/api/schemas";
import { Button } from "@multica/ui/components/ui/button";
import { useLocale, useT } from "../i18n";
import { MemoryLoadError } from "./project-memory-feedback";

export function MemoryEntryRow({ wsId, projectId, entry, sources, edit }: {
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
