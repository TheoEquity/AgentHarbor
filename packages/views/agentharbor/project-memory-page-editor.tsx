"use client";

import { useId, useState, type ReactNode } from "react";
import { useMutation } from "@tanstack/react-query";
import { api, errorCode } from "@multica/core/api";
import type {
  AgentHarborMemoryDraft,
  AgentHarborMemoryEntry,
  AgentHarborMemoryMaterial,
  AgentHarborMemoryReview,
  AgentHarborMemoryReviewInput,
  AgentHarborMemoryScope,
  AgentHarborMemoryUpdateInput,
} from "@multica/core/api/schemas";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n";

const MEMORY_TYPES = ["episodic", "semantic", "procedural"] as const;
const MAX_TITLE_BYTES = 240;
const MAX_CONTENT_BYTES = 8192;
const encoder = new TextEncoder();
const NEW_ENTRY_VALUE = "new";

type ReviewTarget = { entry: AgentHarborMemoryEntry; expectedRevision: number | null };

function validateDraft(draft: AgentHarborMemoryDraft) {
  const titleBytes = encoder.encode(draft.title.trim()).length;
  const contentBytes = encoder.encode(draft.content.trim()).length;
  const tooLong = titleBytes > MAX_TITLE_BYTES || contentBytes > MAX_CONTENT_BYTES;
  return { tooLong, valid: titleBytes > 0 && contentBytes > 0 && !tooLong };
}

function draftFromEntry(entry: AgentHarborMemoryEntry): AgentHarborMemoryDraft {
  return { title: entry.title, content: entry.content, type: entry.type === "unknown" ? "semantic" : entry.type };
}

function DraftFields({ draft, onChange }: {
  draft: AgentHarborMemoryDraft;
  onChange: (draft: AgentHarborMemoryDraft) => void;
}) {
  const { t } = useT("layout");
  const titleId = useId();
  const typeId = useId();
  const contentId = useId();
  const types = MEMORY_TYPES.map((type) => ({ value: type, label: t(($) => $.project_memory.types[type]) }));

  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
        <div className="space-y-1">
          <label htmlFor={titleId} className="font-medium">{t(($) => $.project_memory.title)}</label>
          <Input id={titleId} value={draft.title} required onChange={(event) => onChange({ ...draft, title: event.target.value })} />
        </div>
        <div className="space-y-1">
          <label htmlFor={typeId} className="font-medium">{t(($) => $.project_memory.type)}</label>
          <Select items={types} value={draft.type} onValueChange={(type) => {
            if (type) onChange({ ...draft, type });
          }}>
            <SelectTrigger id={typeId} className="w-full min-w-32"><SelectValue /></SelectTrigger>
            <SelectContent>
              {types.map(({ value, label }) => <SelectItem key={value} value={value}>{label}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
      </div>
      <div className="space-y-1">
        <label htmlFor={contentId} className="font-medium">{t(($) => $.project_memory.content)}</label>
        <Textarea
          id={contentId}
          value={draft.content}
          required
          onChange={(event) => onChange({ ...draft, content: event.target.value })}
          className="min-h-32 resize-y"
        />
      </div>
      {validateDraft(draft).tooLong && <p role="alert" className="text-destructive">{t(($) => $.project_memory.too_long)}</p>}
    </div>
  );
}

function SaveError({ error }: { error: Error | null }) {
  const { t } = useT("layout");
  if (!error) return null;

  const code = errorCode(error);
  return (
    <p role="alert" className="text-destructive">
      {t(($) => {
        switch (code) {
          case "memory_revision_conflict": return $.project_memory.revision_conflict;
          case "memory_already_reviewed": return $.project_memory.already_reviewed;
          case "memory_entry_conflict": return $.project_memory.entry_conflict;
          default: return $.project_memory.save_error;
        }
      })}
    </p>
  );
}

function MemoryEditorDialog({ title, description, pending, onClose, children }: {
  title: string;
  description: string;
  pending: boolean;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <Dialog open onOpenChange={(open) => { if (!open && !pending) onClose(); }}>
      <DialogContent showCloseButton={!pending} className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
  );
}

export function MaterialReview({ projectId, material, pendingMaterials, activeEntries, targetsUnavailable, onClose, onConflict, onReviewed }: {
  projectId: string;
  material: AgentHarborMemoryMaterial;
  pendingMaterials: AgentHarborMemoryMaterial[];
  activeEntries: AgentHarborMemoryEntry[];
  targetsUnavailable: boolean;
  onClose: () => void;
  onConflict: () => Promise<unknown>;
  onReviewed: (result: AgentHarborMemoryReview, input: AgentHarborMemoryReviewInput) => Promise<void>;
}) {
  const { t } = useT("layout");
  const scopeId = useId();
  const targetId = useId();
  const relatedEntry = activeEntries.find((entry) => entry.id === material.relatedEntryId);
  const relatedTarget = relatedEntry?.scope === "unknown" ? undefined : relatedEntry;
  const [selection, setSelection] = useState<ReviewTarget | typeof NEW_ENTRY_VALUE | null>(() => relatedTarget
    ? { entry: relatedTarget, expectedRevision: material.relatedRevision }
    : NEW_ENTRY_VALUE);
  const [scope, setScope] = useState<AgentHarborMemoryScope>(relatedTarget?.scope === "global" ? "global" : "project");
  const [draft, setDraft] = useState<AgentHarborMemoryDraft>(() => relatedTarget
    ? draftFromEntry(relatedTarget)
    : { title: "", content: material.sourceText, type: "semantic" });
  const [additionalIds, setAdditionalIds] = useState<string[]>([]);
  const [discarding, setDiscarding] = useState(false);
  const saving = useMutation({
    mutationFn: (input: AgentHarborMemoryReviewInput) => api.reviewAgentHarborMemoryMaterial(projectId, material.id, input),
    onSuccess: async (result, input) => {
      await onReviewed(result, input);
      onClose();
    },
    onError: (error) => {
      const code = errorCode(error);
      if (code === "memory_revision_conflict" || code === "memory_already_reviewed" || code === "memory_entry_conflict") {
        void onConflict();
      }
    },
  });
  const scopes = [
    { value: "project" as const, label: t(($) => $.project_memory.project_entries) },
    { value: "global" as const, label: t(($) => $.project_memory.global_entries) },
  ];
  const targets = activeEntries.filter((entry) => entry.scope === scope);
  const newEntry = { value: NEW_ENTRY_VALUE, label: t(($) => $.project_memory.new_entry) };
  const targetItems = [newEntry, ...targets.map((entry) => ({ value: entry.id, label: entry.title }))];
  const chosenTarget = typeof selection === "object" ? selection : null;
  const target = chosenTarget?.entry;
  const currentTarget = target ? targets.find((entry) => entry.id === target.id) : undefined;
  if (target && !currentTarget) targetItems.push({ value: target.id, label: target.title });

  const materialIsPending = pendingMaterials.some((item) => item.id === material.id);
  const additional = pendingMaterials.filter((item) => item.id !== material.id);
  const missingAdditional = additionalIds.some((id) => !additional.some((item) => item.id === id));
  const staleRelatedRevision = chosenTarget && chosenTarget.expectedRevision !== chosenTarget.entry.revision;
  const targetChanged = target && (staleRelatedRevision || currentTarget?.revision !== target.revision);
  const disabled = saving.isPending || !materialIsPending;
  const canApprove = !disabled && !!selection && !missingAdditional && !targetChanged && validateDraft(draft).valid;

  function chooseTarget(entry: AgentHarborMemoryEntry) {
    setSelection({ entry, expectedRevision: entry.revision });
    saving.reset();
  }

  return (
    <MemoryEditorDialog
      title={t(($) => $.project_memory.review_material)}
      description={t(($) => $.project_memory.review_note)}
      pending={saving.isPending}
      onClose={onClose}
    >
      <div className="space-y-2 rounded-lg border border-surface-border bg-card p-3">
        <h3 className="font-medium">{t(($) => $.project_memory.source)}</h3>
        <p className="max-h-48 overflow-auto whitespace-pre-wrap wrap-anywhere">{material.sourceText}</p>
        {material.context && <p className="max-h-32 overflow-auto whitespace-pre-wrap wrap-anywhere text-caption text-muted-foreground">{material.context}</p>}
        <dl className="space-y-1 text-caption text-muted-foreground">
          {material.sessionId && <div className="flex flex-wrap gap-x-2"><dt>{t(($) => $.project_memory.session)}</dt><dd className="wrap-anywhere">{material.sessionId}</dd></div>}
          {material.turnId && <div className="flex flex-wrap gap-x-2"><dt>{t(($) => $.project_memory.turn)}</dt><dd className="wrap-anywhere">{material.turnId}</dd></div>}
          {material.relatedEntryId && (
            <div className="flex flex-wrap gap-x-2">
              <dt>{t(($) => $.project_memory.related_entry)}</dt>
              <dd className="wrap-anywhere">{relatedEntry?.title ?? material.relatedEntryId}</dd>
              {material.relatedRevision !== null && <dd>{t(($) => $.project_memory.revision, { revision: material.relatedRevision })}</dd>}
            </div>
          )}
        </dl>
      </div>
      <form className="space-y-3" onSubmit={(event) => {
        event.preventDefault();
        if (!canApprove || discarding) return;
        saving.mutate({
          action: "approve",
          scope,
          type: draft.type,
          title: draft.title.trim(),
          content: draft.content.trim(),
          target: target ? { entryId: target.id, expectedRevision: target.revision } : undefined,
          additionalMaterialIds: additionalIds.length ? additionalIds : undefined,
        });
      }}>
        {discarding ? (
          <p className="text-muted-foreground">{t(($) => $.project_memory.discard_note)}</p>
        ) : (
          <fieldset className="min-w-0 space-y-3" disabled={saving.isPending}>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="min-w-0 space-y-1">
                <label htmlFor={scopeId} className="font-medium">{t(($) => $.project_memory.scope)}</label>
                <Select items={scopes} value={scope} onValueChange={(value) => {
                  if (!value) return;
                  setScope(value);
                  if (target) setSelection(null);
                }}>
                  <SelectTrigger id={scopeId} className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {scopes.map(({ value, label }) => <SelectItem key={value} value={value}>{label}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
              <div className="min-w-0 space-y-1">
                <label htmlFor={targetId} className="font-medium">{t(($) => $.project_memory.approval_target)}</label>
                <Select items={targetItems} value={target?.id ?? (selection === NEW_ENTRY_VALUE ? NEW_ENTRY_VALUE : null)} onValueChange={(value) => {
                  if (value === NEW_ENTRY_VALUE) {
                    setSelection(NEW_ENTRY_VALUE);
                    saving.reset();
                    return;
                  }
                  const entry = targets.find((item) => item.id === value);
                  if (entry) chooseTarget(entry);
                }}>
                  <SelectTrigger id={targetId} className="w-full">
                    <SelectValue placeholder={t(($) => $.project_memory.choose_target)} />
                  </SelectTrigger>
                  <SelectContent>
                    {targetItems.map(({ value, label }) => <SelectItem key={value} value={value}>{label}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
            </div>
            {scope === "global" && <p className="text-caption text-muted-foreground">{t(($) => $.project_memory.global_note)}</p>}
            {targetsUnavailable && <p className="text-caption text-destructive">{t(($) => $.project_memory.targets_unavailable)}</p>}
            {target && (
              <div className="space-y-1 rounded-lg bg-muted p-3">
                <h3 className="font-medium wrap-anywhere">{target.title}</h3>
                <p className="text-caption text-muted-foreground">{t(($) => $.project_memory.revision, { revision: target.revision })}</p>
                <p className="max-h-36 overflow-auto whitespace-pre-wrap wrap-anywhere">{target.content}</p>
                {targetChanged && (
                  <p role="alert" className="text-caption text-destructive">
                    {t(($) => staleRelatedRevision ? $.project_memory.stale_related_revision : $.project_memory.entry_changed)}
                  </p>
                )}
                {targetChanged && currentTarget && (
                  <Button type="button" variant="outline" size="sm" onClick={() => chooseTarget(currentTarget)}>
                    {t(($) => $.project_memory.review_latest)}
                  </Button>
                )}
              </div>
            )}
            <DraftFields draft={draft} onChange={setDraft} />
            {additional.length > 0 && (
              <details className="text-caption">
                <summary className="w-fit cursor-pointer font-medium">{t(($) => $.project_memory.additional_sources)}</summary>
                <div className="mt-2 max-h-40 space-y-3 overflow-auto p-1">
                  {additional.map((item) => (
                    <label key={item.id} className="flex items-start gap-2">
                      <Checkbox checked={additionalIds.includes(item.id)} onCheckedChange={(checked) => {
                        setAdditionalIds((previous) => checked ? [...previous, item.id] : previous.filter((id) => id !== item.id));
                      }} />
                      <span className="whitespace-pre-wrap wrap-anywhere">{item.sourceText}</span>
                    </label>
                  ))}
                </div>
              </details>
            )}
            {missingAdditional && (
              <Button type="button" variant="outline" size="sm" onClick={() => {
                setAdditionalIds((previous) => previous.filter((id) => additional.some((item) => item.id === id)));
                saving.reset();
              }}>
                {t(($) => $.project_memory.clear_unavailable_sources)}
              </Button>
            )}
          </fieldset>
        )}
        <SaveError error={saving.error} />
        {!materialIsPending && <p role="alert" className="text-destructive">{t(($) => $.project_memory.already_reviewed)}</p>}
        <DialogFooter>
          {discarding ? (
            <>
              <Button type="button" variant="outline" disabled={saving.isPending} onClick={() => { setDiscarding(false); saving.reset(); }}>
                {t(($) => $.project_memory.cancel)}
              </Button>
              <Button type="button" variant="destructive" disabled={disabled} aria-busy={saving.isPending} onClick={() => saving.mutate({ action: "discard" })}>
                {t(($) => $.project_memory.discard)}
              </Button>
            </>
          ) : (
            <>
              <Button type="button" variant="ghost" disabled={disabled} onClick={() => { setDiscarding(true); saving.reset(); }}>
                {t(($) => $.project_memory.discard)}
              </Button>
              <Button type="button" variant="outline" disabled={saving.isPending} onClick={onClose}>
                {t(($) => $.project_memory.defer)}
              </Button>
              <Button type="submit" disabled={!canApprove} aria-busy={saving.isPending}>{t(($) => $.project_memory.approve)}</Button>
            </>
          )}
        </DialogFooter>
      </form>
    </MemoryEditorDialog>
  );
}

export function EntryEditor({ projectId, entry, current, onClose, onConflict, onUpdated }: {
  projectId: string;
  entry: AgentHarborMemoryEntry;
  current?: AgentHarborMemoryEntry;
  onClose: () => void;
  onConflict: () => Promise<unknown>;
  onUpdated: () => Promise<void>;
}) {
  const { t } = useT("layout");
  const [snapshot, setSnapshot] = useState(entry);
  const [draft, setDraft] = useState(() => draftFromEntry(entry));
  const [disabling, setDisabling] = useState(false);
  const saving = useMutation({
    mutationFn: (input: AgentHarborMemoryUpdateInput) => api.updateAgentHarborMemoryEntry(projectId, entry.id, input),
    onSuccess: async () => {
      await onUpdated();
      onClose();
    },
    onError: (error) => {
      const code = errorCode(error);
      if (code === "memory_revision_conflict" || code === "memory_entry_conflict") void onConflict();
    },
  });
  const changed = current?.revision !== snapshot.revision;
  const disabled = saving.isPending || changed;
  const canSave = !disabled && validateDraft(draft).valid;
  return (
    <MemoryEditorDialog
      title={t(($) => disabling ? $.project_memory.disable : $.project_memory.edit)}
      description={t(($) => disabling ? $.project_memory.disable_note : $.project_memory.revision, { revision: snapshot.revision })}
      pending={saving.isPending}
      onClose={onClose}
    >
      <form className="space-y-3" onSubmit={(event) => {
        event.preventDefault();
        if (!canSave || disabling) return;
        saving.mutate({
          action: "revise",
          expectedRevision: snapshot.revision,
          type: draft.type,
          title: draft.title.trim(),
          content: draft.content.trim(),
        });
      }}>
        {!disabling && (
          <fieldset className="min-w-0" disabled={saving.isPending}>
            <DraftFields draft={draft} onChange={setDraft} />
          </fieldset>
        )}
        {changed && <p role="alert" className="text-destructive">{t(($) => $.project_memory.entry_changed)}</p>}
        {changed && current && (
          <div className="space-y-2 rounded-lg bg-muted p-3">
            <h3 className="font-medium wrap-anywhere">{current.title}</h3>
            <p className="max-h-36 overflow-auto whitespace-pre-wrap wrap-anywhere">{current.content}</p>
            <Button type="button" variant="outline" size="sm" onClick={() => {
              setSnapshot(current);
              saving.reset();
            }}>
              {t(($) => $.project_memory.review_latest)}
            </Button>
          </div>
        )}
        <SaveError error={saving.error} />
        <DialogFooter>
          {disabling ? (
            <>
              <Button type="button" variant="outline" disabled={saving.isPending} onClick={() => { setDisabling(false); saving.reset(); }}>
                {t(($) => $.project_memory.cancel)}
              </Button>
              <Button type="button" variant="destructive" disabled={disabled} aria-busy={saving.isPending} onClick={() => {
                saving.mutate({ action: "disable", expectedRevision: snapshot.revision });
              }}>
                {t(($) => $.project_memory.disable)}
              </Button>
            </>
          ) : (
            <>
              <Button type="button" variant="ghost" disabled={disabled} onClick={() => { setDisabling(true); saving.reset(); }}>
                {t(($) => $.project_memory.disable)}
              </Button>
              <Button type="button" variant="outline" disabled={saving.isPending} onClick={onClose}>{t(($) => $.project_memory.cancel)}</Button>
              <Button type="submit" disabled={!canSave} aria-busy={saving.isPending}>{t(($) => $.project_memory.save)}</Button>
            </>
          )}
        </DialogFooter>
      </form>
    </MemoryEditorDialog>
  );
}
