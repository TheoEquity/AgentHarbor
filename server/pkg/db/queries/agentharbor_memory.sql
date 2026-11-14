-- name: CreateAgentHarborMemoryMaterial :one
INSERT INTO agentharbor_memory_material (
    workspace_id, project_id, submitted_by, request_id, source_text, context,
    session_id, turn_id, related_entry_id, related_revision
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (workspace_id, project_id, submitted_by, request_id) DO NOTHING
RETURNING *;

-- name: GetAgentHarborMemoryMaterialByRequest :one
SELECT * FROM agentharbor_memory_material
WHERE workspace_id = $1 AND project_id = $2 AND submitted_by = $3 AND request_id = $4;

-- name: ListAgentHarborMemoryMaterials :many
SELECT * FROM agentharbor_memory_material
WHERE workspace_id = $1 AND project_id = $2
ORDER BY created_at DESC, id DESC;

-- name: LockAgentHarborMemoryMaterials :many
SELECT * FROM agentharbor_memory_material
WHERE workspace_id = $1 AND project_id = $2 AND id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY id FOR UPDATE;

-- name: FinishAgentHarborMemoryMaterials :many
UPDATE agentharbor_memory_material
SET status = $1, reviewed_by = $2, reviewed_at = now()
WHERE workspace_id = $3 AND project_id = $4 AND id = ANY(sqlc.arg(ids)::uuid[])
RETURNING *;

-- name: CreateAgentHarborMemoryEntry :one
INSERT INTO agentharbor_memory_entry (
    workspace_id, project_id, owner_id, scope, type, title, content, source_material_ids
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING *;

-- name: ListAgentHarborMemoryEntries :many
SELECT * FROM agentharbor_memory_entry
WHERE agentharbor_memory_entry.workspace_id = $1
  AND (project_id = sqlc.arg(bound_project_id)::uuid OR (scope = 'global' AND owner_id = sqlc.arg(actor_id)::uuid AND sqlc.arg(include_global)::boolean))
  AND (NOT sqlc.arg(active_only)::boolean OR status = 'active')
  AND (NOT sqlc.arg(exclude_disputed)::boolean OR NOT EXISTS (
    SELECT 1 FROM agentharbor_memory_material AS m
    WHERE m.workspace_id = agentharbor_memory_entry.workspace_id
      AND m.related_entry_id = agentharbor_memory_entry.id AND m.status = 'pending'
      AND (m.project_id = sqlc.arg(bound_project_id)::uuid OR m.submitted_by = sqlc.arg(actor_id)::uuid)
  ))
  AND (sqlc.arg(scope_filter)::text = '' OR scope = sqlc.arg(scope_filter))
  AND (sqlc.arg(type_filter)::text = '' OR type = sqlc.arg(type_filter))
  AND (sqlc.arg(search_text)::text = '' OR strpos(lower(title || ' ' || content), lower(sqlc.arg(search_text))) > 0)
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(max_results)::integer;

-- name: GetAgentHarborMemoryEntry :one
SELECT * FROM agentharbor_memory_entry
WHERE workspace_id = $1 AND id = $2
  AND (project_id = sqlc.arg(bound_project_id)::uuid OR (scope = 'global' AND owner_id = sqlc.arg(actor_id)::uuid));

-- name: UpdateAgentHarborMemoryEntry :one
UPDATE agentharbor_memory_entry
SET type = $1, title = $2, content = $3, status = $4, source_material_ids = $5,
    revision = revision + 1, updated_at = now()
WHERE workspace_id = $6 AND id = $7 AND revision = sqlc.arg(expected_revision)
  AND (project_id = sqlc.arg(bound_project_id)::uuid OR (scope = 'global' AND owner_id = sqlc.arg(actor_id)::uuid))
RETURNING *;

-- name: RecordAgentHarborMemoryRevision :exec
INSERT INTO agentharbor_memory_revision (
    entry_id, revision, workspace_id, project_id, type, title, content, status, source_material_ids, changed_by
)
SELECT id, revision, workspace_id, project_id, type, title, content, status, source_material_ids, sqlc.arg(changed_by)
FROM agentharbor_memory_entry
WHERE agentharbor_memory_entry.workspace_id = $1 AND agentharbor_memory_entry.id = $2;

-- name: GetAgentHarborMemorySettings :one
SELECT * FROM agentharbor_memory_settings WHERE workspace_id = $1 AND project_id = $2;

-- name: PutAgentHarborMemorySettings :one
INSERT INTO agentharbor_memory_settings (
    project_id, workspace_id, record_enabled, cloud_enabled, use_memory, use_global
) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (project_id) DO UPDATE SET
    record_enabled = EXCLUDED.record_enabled, cloud_enabled = EXCLUDED.cloud_enabled,
    use_memory = EXCLUDED.use_memory, use_global = EXCLUDED.use_global
WHERE agentharbor_memory_settings.workspace_id = EXCLUDED.workspace_id
RETURNING *;

-- name: DeleteAgentHarborMemoryRevisionsByProject :exec
DELETE FROM agentharbor_memory_revision WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteAgentHarborMemoryEntriesByProject :exec
DELETE FROM agentharbor_memory_entry WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteAgentHarborMemoryMaterialsByProject :exec
DELETE FROM agentharbor_memory_material AS m
WHERE m.workspace_id = $1 AND m.project_id = $2
  AND NOT EXISTS (
    SELECT 1 FROM agentharbor_memory_revision AS r
    WHERE r.workspace_id = m.workspace_id AND r.project_id IS NULL AND m.id = ANY(r.source_material_ids)
  );

-- name: DeleteAgentHarborMemorySettingsByProject :exec
DELETE FROM agentharbor_memory_settings WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteAgentHarborMemoryRevisionsByWorkspace :exec
DELETE FROM agentharbor_memory_revision WHERE workspace_id = $1;

-- name: DeleteAgentHarborMemoryEntriesByWorkspace :exec
DELETE FROM agentharbor_memory_entry WHERE workspace_id = $1;

-- name: DeleteAgentHarborMemoryMaterialsByWorkspace :exec
DELETE FROM agentharbor_memory_material WHERE workspace_id = $1;

-- name: DeleteAgentHarborMemorySettingsByWorkspace :exec
DELETE FROM agentharbor_memory_settings WHERE workspace_id = $1;

-- name: ListAgentHarborMemoryRevisions :many
SELECT * FROM agentharbor_memory_revision
WHERE workspace_id = $1 AND entry_id = $2
ORDER BY revision DESC;

-- name: ListAgentHarborMemoryHistorySources :many
SELECT * FROM agentharbor_memory_material
WHERE workspace_id = $1 AND id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY created_at, id;

-- name: ListAgentHarborMemoryDisputes :many
SELECT related_entry_id, related_revision, project_id,
       id,
       CASE WHEN project_id = sqlc.arg(bound_project_id)::uuid THEN source_text ELSE '' END AS source_text,
       CASE WHEN project_id = sqlc.arg(bound_project_id)::uuid THEN context ELSE '' END AS context
FROM agentharbor_memory_material
WHERE workspace_id = $1 AND related_entry_id = ANY(sqlc.arg(entry_ids)::uuid[]) AND status = 'pending'
  AND (project_id = sqlc.arg(bound_project_id)::uuid OR submitted_by = sqlc.arg(actor_id)::uuid)
ORDER BY created_at, id;
