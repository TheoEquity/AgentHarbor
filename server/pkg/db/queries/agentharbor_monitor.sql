-- name: LockAgentHarborMonitorMember :one
SELECT id FROM member WHERE workspace_id = $1 AND user_id = $2 FOR KEY SHARE;

-- name: UpsertAgentHarborMonitorSource :exec
INSERT INTO agentharbor_monitor_source (workspace_id, project_id, machine_id, machine_name, scan_status)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, project_id, machine_id) DO UPDATE SET
    machine_name = EXCLUDED.machine_name,
    scan_status = EXCLUDED.scan_status,
    last_synced_at = clock_timestamp();

-- name: UpsertAgentHarborMonitorTurn :one
INSERT INTO agentharbor_monitor_turn AS t (
    workspace_id, project_id, machine_id, session_id, turn_id, title,
    started_at, ended_at, status, historical
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, project_id, machine_id, session_id, turn_id) DO UPDATE SET
    started_at = COALESCE(t.started_at, EXCLUDED.started_at),
    title = CASE WHEN t.status = 'running' OR EXCLUDED.status = t.status THEN EXCLUDED.title ELSE t.title END,
    ended_at = COALESCE(t.ended_at, CASE WHEN t.status = 'running' OR EXCLUDED.status = t.status THEN EXCLUDED.ended_at END),
    status = CASE WHEN t.status = 'running' THEN EXCLUDED.status ELSE t.status END,
    historical = CASE WHEN t.status = 'running' THEN EXCLUDED.historical ELSE t.historical END
RETURNING *;

-- name: GetAgentHarborMonitorTurnStatus :one
SELECT status FROM agentharbor_monitor_turn
WHERE workspace_id = $1 AND project_id = $2 AND machine_id = $3 AND session_id = $4 AND turn_id = $5;

-- name: MarkAgentHarborMonitorHandled :exec
INSERT INTO agentharbor_monitor_handled (workspace_id, project_id, machine_id, session_id, turn_id, user_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (workspace_id, project_id, machine_id, session_id, turn_id, user_id) DO NOTHING;

-- name: ListAgentHarborMonitorTurns :many
WITH scoped AS NOT MATERIALIZED (
    SELECT t.* FROM agentharbor_monitor_turn t
    WHERE t.workspace_id = sqlc.arg(workspace_id)
      AND (sqlc.narg(project_id)::uuid IS NULL OR t.project_id = sqlc.narg(project_id))
      AND (sqlc.narg(machine_id)::uuid IS NULL OR t.machine_id = sqlc.narg(machine_id))
), candidates AS (
    SELECT * FROM scoped WHERE sqlc.arg(history)::boolean AND scoped.status <> 'running'
    UNION ALL
    (SELECT DISTINCT ON (project_id, machine_id, session_id) * FROM scoped
     WHERE NOT sqlc.arg(history)::boolean
     ORDER BY project_id, machine_id, session_id, started_at DESC NULLS LAST, ended_at DESC NULLS LAST, turn_id DESC)
)
SELECT t.project_id, p.title AS project_name, t.machine_id, s.machine_name, t.session_id, t.turn_id,
       t.title, t.started_at, t.ended_at, t.status, t.historical, (handled.user_id IS NOT NULL)::boolean AS has_handling
FROM candidates t
JOIN project p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
JOIN agentharbor_monitor_source s ON s.workspace_id = t.workspace_id AND s.project_id = t.project_id AND s.machine_id = t.machine_id
LEFT JOIN agentharbor_monitor_handled handled
    ON (handled.workspace_id, handled.project_id, handled.machine_id, handled.session_id, handled.turn_id) =
       (t.workspace_id, t.project_id, t.machine_id, t.session_id, t.turn_id)
    AND handled.user_id = sqlc.arg(user_id)
WHERE sqlc.arg(history)::boolean OR t.status = 'running' OR (NOT t.historical AND handled.user_id IS NULL)
ORDER BY COALESCE(t.ended_at, t.started_at) DESC NULLS LAST, t.project_id, t.machine_id, t.session_id, t.turn_id
LIMIT sqlc.narg(page_limit)::bigint OFFSET sqlc.arg(page_offset)::bigint;

-- name: ListAgentHarborMonitorSources :many
SELECT s.project_id, s.machine_id, s.machine_name, s.last_synced_at,
       (s.scan_status = 'degraded' OR s.last_synced_at < now() - interval '10 minutes') AS stale
FROM agentharbor_monitor_source s
JOIN project p ON p.workspace_id = s.workspace_id AND p.id = s.project_id
WHERE s.workspace_id = sqlc.arg(workspace_id)
  AND (sqlc.narg(project_id)::uuid IS NULL OR s.project_id = sqlc.narg(project_id))
ORDER BY s.machine_name, s.machine_id, s.project_id;

-- name: GetAgentHarborBarkSetting :one
SELECT * FROM agentharbor_bark_setting WHERE workspace_id = $1 AND user_id = $2;

-- name: UpsertAgentHarborBarkSetting :one
INSERT INTO agentharbor_bark_setting AS s (workspace_id, user_id, credentials, enabled_since)
VALUES ($1, $2, $3, CASE WHEN sqlc.arg(enabled)::boolean THEN clock_timestamp() END)
ON CONFLICT (workspace_id, user_id) DO UPDATE SET
    credentials = EXCLUDED.credentials,
    enabled_since = CASE WHEN sqlc.arg(enabled) THEN COALESCE(s.enabled_since, EXCLUDED.enabled_since) END
RETURNING *;

-- name: SetAgentHarborBarkEnabled :one
UPDATE agentharbor_bark_setting
SET enabled_since = CASE WHEN sqlc.arg(enabled)::boolean THEN COALESCE(enabled_since, clock_timestamp()) END
WHERE workspace_id = $1 AND user_id = $2
RETURNING *;

-- name: EnqueueAgentHarborBarkDelivery :exec
INSERT INTO agentharbor_bark_delivery
    (workspace_id, project_id, machine_id, session_id, turn_id, user_id, title, body)
SELECT s.workspace_id, p.id, sqlc.arg(machine_id), sqlc.arg(session_id), sqlc.arg(turn_id), s.user_id,
       sqlc.arg(title), p.title || ' · ' || sqlc.arg(outcome)::text
FROM agentharbor_bark_setting s
JOIN project p ON p.workspace_id = s.workspace_id AND p.id = sqlc.arg(project_id)
WHERE s.workspace_id = sqlc.arg(workspace_id) AND s.user_id = sqlc.arg(user_id)
  AND sqlc.arg(ended_at)::timestamptz >= s.enabled_since
ON CONFLICT (workspace_id, project_id, machine_id, session_id, turn_id, user_id) DO NOTHING;

-- name: ClaimAgentHarborBarkDelivery :one
WITH due AS (
    SELECT d.* FROM agentharbor_bark_delivery d
    JOIN agentharbor_bark_setting s USING (workspace_id, user_id)
    JOIN member m USING (workspace_id, user_id)
    WHERE d.sent_at IS NULL AND d.available_at <= clock_timestamp() AND s.enabled_since IS NOT NULL
    ORDER BY d.available_at
    LIMIT 1
    FOR UPDATE OF d SKIP LOCKED
)
UPDATE agentharbor_bark_delivery d
SET attempts = d.attempts + 1, available_at = clock_timestamp() + interval '1 minute'
FROM due
WHERE (d.workspace_id, d.project_id, d.machine_id, d.session_id, d.turn_id, d.user_id) =
      (due.workspace_id, due.project_id, due.machine_id, due.session_id, due.turn_id, due.user_id)
RETURNING d.*;

-- name: FinishAgentHarborBarkDelivery :exec
UPDATE agentharbor_bark_delivery
SET sent_at = CASE WHEN sqlc.arg(sent)::boolean THEN clock_timestamp() END,
    available_at = clock_timestamp() + sqlc.arg(retry_seconds)::integer * interval '1 second'
WHERE workspace_id = $1 AND project_id = $2 AND machine_id = $3 AND session_id = $4
  AND turn_id = $5 AND user_id = $6 AND attempts = $7 AND sent_at IS NULL;

-- name: DeleteAgentHarborMonitorProject :exec
WITH deliveries AS (DELETE FROM agentharbor_bark_delivery d WHERE d.workspace_id = sqlc.arg(workspace_id) AND d.project_id = sqlc.arg(project_id)),
     handled AS (DELETE FROM agentharbor_monitor_handled h WHERE h.workspace_id = sqlc.arg(workspace_id) AND h.project_id = sqlc.arg(project_id)),
     turns AS (DELETE FROM agentharbor_monitor_turn t WHERE t.workspace_id = sqlc.arg(workspace_id) AND t.project_id = sqlc.arg(project_id))
DELETE FROM agentharbor_monitor_source s WHERE s.workspace_id = sqlc.arg(workspace_id) AND s.project_id = sqlc.arg(project_id);

-- name: DeleteAgentHarborMonitorWorkspace :exec
WITH deliveries AS (DELETE FROM agentharbor_bark_delivery target WHERE target.workspace_id = sqlc.arg(workspace_id)),
     settings AS (DELETE FROM agentharbor_bark_setting target WHERE target.workspace_id = sqlc.arg(workspace_id)),
     handled AS (DELETE FROM agentharbor_monitor_handled target WHERE target.workspace_id = sqlc.arg(workspace_id)),
     turns AS (DELETE FROM agentharbor_monitor_turn target WHERE target.workspace_id = sqlc.arg(workspace_id))
DELETE FROM agentharbor_monitor_source target WHERE target.workspace_id = sqlc.arg(workspace_id);

-- name: DeleteAgentHarborMonitorMember :exec
WITH deliveries AS (DELETE FROM agentharbor_bark_delivery d WHERE d.workspace_id = sqlc.arg(workspace_id) AND d.user_id = sqlc.arg(user_id)),
     handled AS (DELETE FROM agentharbor_monitor_handled h WHERE h.workspace_id = sqlc.arg(workspace_id) AND h.user_id = sqlc.arg(user_id))
DELETE FROM agentharbor_bark_setting s WHERE s.workspace_id = sqlc.arg(workspace_id) AND s.user_id = sqlc.arg(user_id);
