import { z } from "zod";

const AgentHarborStartupMemoryReceiptSchema = z.strictObject({
  trigger: z.literal("startup"),
  fetched: z.boolean(),
  emitted: z.boolean(),
  context_emitted: z.boolean(),
  entries: z.array(z.strictObject({
    id: z.guid(),
    revision: z.number().int().positive(),
  })).max(100),
  version: z.string().regex(/^(?:[a-f0-9]{64})?$/),
}).refine((receipt) =>
  Boolean(receipt.version) === (receipt.entries.length > 0) &&
  (receipt.fetched || receipt.entries.length === 0) &&
  (!receipt.context_emitted || (receipt.emitted && receipt.entries.length > 0)),
).transform(({ context_emitted, ...receipt }) => ({ ...receipt, contextEmitted: context_emitted }));

function parseAgentHarborStartupMemoryReceipt(evidence: string): AgentHarborStartupMemoryReceipt | undefined {
  // Valid receipts contain only ASCII metadata.
  if (evidence.length > 8192) return undefined;
  try {
    const parsed = AgentHarborStartupMemoryReceiptSchema.safeParse(JSON.parse(evidence));
    return parsed.success ? parsed.data : undefined;
  } catch {
    return undefined;
  }
}

export const AgentHarborEventSchema = z.object({
  event_id: z.string(),
  machine_id: z.string(),
  project_id: z.string(),
  agent_type: z.string(),
  session_id: z.string(),
  turn_id: z.string(),
  local_seq: z.number(),
  event_type: z.string(),
  occurred_at: z.string(),
  received_at: z.string(),
  evidence: z.string(),
}).loose().transform((event) => ({
  eventId: event.event_id,
  machineId: event.machine_id,
  projectId: event.project_id,
  agentType: event.agent_type,
  sessionId: event.session_id,
  turnId: event.turn_id,
  localSeq: event.local_seq,
  eventType: event.event_type,
  occurredAt: event.occurred_at,
  receivedAt: event.received_at,
  evidence: event.event_type === "memory_read" ? "" : event.evidence,
  memoryReceipt: event.event_type === "memory_read"
    ? parseAgentHarborStartupMemoryReceipt(event.evidence)
    : undefined,
}));

export const AgentHarborTaskSchema = z.object({
  project_id: z.string(),
  machine_id: z.string(),
  machine_name: z.string().default(""),
  agent_type: z.string(),
  session_id: z.string(),
  status: z.string().default("unknown"),
  last_occurred_at: z.string().default(""),
  last_received_at: z.string().default(""),
  events: z.array(AgentHarborEventSchema).default([]).catch([]),
}).loose().transform((task) => ({
  projectId: task.project_id,
  machineId: task.machine_id,
  machineName: task.machine_name,
  agentType: task.agent_type,
  sessionId: task.session_id,
  status: task.status,
  lastOccurredAt: task.last_occurred_at,
  lastReceivedAt: task.last_received_at,
  events: task.events,
}));

export const AgentHarborTasksResponseSchema = z.object({
  tasks: z.array(AgentHarborTaskSchema).default([]),
}).loose();

export type AgentHarborStartupMemoryReceipt = z.infer<typeof AgentHarborStartupMemoryReceiptSchema>;
export type AgentHarborEvent = z.infer<typeof AgentHarborEventSchema>;
export type AgentHarborTask = z.infer<typeof AgentHarborTaskSchema>;
export type AgentHarborTasksResponse = z.infer<typeof AgentHarborTasksResponseSchema>;

const AgentHarborMonitorTaskSchema = z.object({
  project_id: z.string().min(1),
  project_name: z.string().catch(""),
  machine_id: z.string().min(1),
  machine_name: z.string().catch(""),
  session_id: z.string().min(1),
  turn_id: z.string().min(1),
  title: z.string().catch(""),
  started_at: z.iso.datetime({ offset: true }).nullable().catch(null),
  ended_at: z.iso.datetime({ offset: true }).nullable().catch(null),
  status: z.enum(["running", "completed", "aborted", "unknown"]).catch("unknown"),
  handled: z.boolean().nullable().catch(null),
  historical: z.boolean().catch(false),
}).transform((task) => ({
  projectId: task.project_id,
  projectName: task.project_name,
  machineId: task.machine_id,
  machineName: task.machine_name,
  sessionId: task.session_id,
  turnId: task.turn_id,
  title: task.title,
  startedAt: task.started_at,
  endedAt: task.ended_at,
  status: task.status,
  handled: task.handled,
  historical: task.historical,
}));

const AgentHarborMonitorSourceSchema = z.object({
  project_id: z.string().min(1),
  machine_id: z.string().min(1),
  machine_name: z.string().catch(""),
  last_synced_at: z.iso.datetime({ offset: true }),
  stale: z.boolean().catch(true),
}).transform((source) => ({
  projectId: source.project_id,
  machineId: source.machine_id,
  machineName: source.machine_name,
  lastSyncedAt: source.last_synced_at,
  stale: source.stale,
}));

export const AgentHarborMonitorResponseSchema = z.object({
  tasks: z.array(AgentHarborMonitorTaskSchema),
  sources: z.array(AgentHarborMonitorSourceSchema),
  has_more: z.boolean().catch(false),
}).transform(({ tasks, sources, has_more }) => ({ tasks, sources, hasMore: has_more }));

export const AgentHarborMonitorHandleSchema = z.object({ handled: z.literal(true) });

export const AgentHarborBarkSettingsSchema = z.object({
  configured: z.boolean(),
  enabled: z.boolean(),
});

export const AgentHarborBarkTestSchema = z.object({ sent: z.literal(true) });

export type AgentHarborMonitorTask = z.infer<typeof AgentHarborMonitorTaskSchema>;
export type AgentHarborMonitorResponse = z.infer<typeof AgentHarborMonitorResponseSchema>;
export type AgentHarborMonitorTaskId = Pick<AgentHarborMonitorTask, "projectId" | "machineId" | "sessionId" | "turnId">;
export type AgentHarborBarkSettings = z.infer<typeof AgentHarborBarkSettingsSchema>;

export interface AgentHarborMonitorFilters {
  view: "active" | "history";
  projectId?: string;
  machineId?: string;
  page?: number;
}

export type AgentHarborBarkUpdate = { enabled: boolean } & (
  | { serverUrl: string; deviceKey: string }
  | { serverUrl?: never; deviceKey?: never }
);

export const AgentHarborMemorySnapshotSchema = z.object({
  project_id: z.string(),
  version: z.string().default(""),
  content: z.string().default(""),
}).transform(({ project_id, version, content }) => ({ projectId: project_id, version, content }));

export type AgentHarborMemorySnapshot = z.infer<typeof AgentHarborMemorySnapshotSchema>;

const AgentHarborMemoryScopeSchema = z.enum(["project", "global"]);
const AgentHarborMemoryTypeSchema = z.enum(["episodic", "semantic", "procedural"]);
const UnknownMemoryValueSchema = z.string().transform(() => "unknown" as const);
const AgentHarborMemoryEntryFields = z.object({
  type: AgentHarborMemoryTypeSchema.or(UnknownMemoryValueSchema),
  title: z.string(),
  content: z.string(),
  status: z.enum(["active", "disabled"]).or(UnknownMemoryValueSchema),
  revision: z.number().int().positive(),
  source_material_ids: z.array(z.string().min(1)),
});

export type AgentHarborMemoryScope = z.infer<typeof AgentHarborMemoryScopeSchema>;
export type AgentHarborMemoryType = z.infer<typeof AgentHarborMemoryTypeSchema>;

const AgentHarborMemoryMaterialSchema = z.object({
  id: z.string().min(1),
  project_id: z.string().min(1),
  source_text: z.string(),
  context: z.string(),
  session_id: z.string(),
  turn_id: z.string(),
  status: z.enum(["pending", "processed", "discarded"]).or(UnknownMemoryValueSchema),
  related_entry_id: z.string().min(1).nullish(),
  related_revision: z.number().int().positive().nullish(),
  created_at: z.string(),
}).transform((material) => ({
  id: material.id,
  projectId: material.project_id,
  sourceText: material.source_text,
  context: material.context,
  sessionId: material.session_id,
  turnId: material.turn_id,
  status: material.status,
  relatedEntryId: material.related_entry_id ?? null,
  relatedRevision: material.related_revision ?? null,
  createdAt: material.created_at,
}));

export const AgentHarborMemoryEntrySchema = AgentHarborMemoryEntryFields.extend({
  id: z.string().min(1),
  project_id: z.string().min(1).nullish(),
  scope: AgentHarborMemoryScopeSchema.or(UnknownMemoryValueSchema),
  updated_at: z.string(),
}).transform((entry) => ({
  id: entry.id,
  projectId: entry.project_id ?? null,
  scope: entry.scope,
  type: entry.type,
  title: entry.title,
  content: entry.content,
  status: entry.status,
  revision: entry.revision,
  sourceMaterialIds: entry.source_material_ids,
  updatedAt: entry.updated_at,
}));

export const AgentHarborMemoryMaterialsSchema = z.object({
  materials: z.array(AgentHarborMemoryMaterialSchema),
});

export const AgentHarborMemoryEntriesSchema = z.object({
  entries: z.array(AgentHarborMemoryEntrySchema),
});

const AgentHarborMemoryRevisionSchema = AgentHarborMemoryEntryFields.extend({
  created_at: z.string(),
}).transform(({ source_material_ids, created_at, ...revision }) => ({
  ...revision,
  sourceMaterialIds: source_material_ids,
  createdAt: created_at,
}));

export const AgentHarborMemoryHistorySchema = z.object({
  revisions: z.array(AgentHarborMemoryRevisionSchema),
  materials: z.array(AgentHarborMemoryMaterialSchema),
  disputes: z.array(z.object({
    id: z.string().min(1),
    source_text: z.string(),
    context: z.string(),
  }).transform(({ id, source_text, context }) => ({ id, sourceText: source_text, context }))).default([]),
});

export const AgentHarborMemoryReviewSchema = z.object({
  material: AgentHarborMemoryMaterialSchema,
  entry: AgentHarborMemoryEntrySchema.optional(),
});

export const AgentHarborMemorySettingsSchema = z.object({
  record_enabled: z.boolean(),
  cloud_enabled: z.boolean(),
  use_memory: z.boolean(),
  use_global: z.boolean(),
}).transform((settings) => ({
  recordEnabled: settings.record_enabled,
  cloudEnabled: settings.cloud_enabled,
  useMemory: settings.use_memory,
  useGlobal: settings.use_global,
}));

export const AgentHarborMemoryAccessTokenSchema = z.object({
  token: z.string().startsWith("ahm_"),
  project_id: z.string().uuid(),
  expires_at: z.string().datetime({ offset: true }),
}).transform(({ token, project_id, expires_at }) => ({
  token,
  projectId: project_id,
  expiresAt: expires_at,
}));

export type AgentHarborMemoryMaterial = z.infer<typeof AgentHarborMemoryMaterialSchema>;
export type AgentHarborMemoryEntry = z.infer<typeof AgentHarborMemoryEntrySchema>;
export type AgentHarborMemoryHistory = z.infer<typeof AgentHarborMemoryHistorySchema>;
export type AgentHarborMemoryRevision = z.infer<typeof AgentHarborMemoryRevisionSchema>;
export type AgentHarborMemoryReview = z.infer<typeof AgentHarborMemoryReviewSchema>;
export type AgentHarborMemorySettings = z.infer<typeof AgentHarborMemorySettingsSchema>;
export type AgentHarborMemoryAccessToken = z.infer<typeof AgentHarborMemoryAccessTokenSchema>;

export interface AgentHarborMemoryDraft {
  type: AgentHarborMemoryType;
  title: string;
  content: string;
}

export type AgentHarborMemoryReviewInput =
  | { action: "discard" }
  | (AgentHarborMemoryDraft & {
    action: "approve";
    scope: AgentHarborMemoryScope;
    target?: { entryId: string; expectedRevision: number };
    additionalMaterialIds?: string[];
  });

export type AgentHarborMemoryUpdateInput =
  | { action: "disable"; expectedRevision: number }
  | (AgentHarborMemoryDraft & { action: "revise"; expectedRevision: number });
