-- name: InsertAgentHarborEvent :exec
INSERT INTO agentharbor_event (
    event_id, workspace_id, project_id, machine_id, machine_name,
    agent_type, session_id, turn_id, local_seq, event_type, occurred_at, evidence
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
ON CONFLICT (event_id) DO NOTHING;

-- name: ListAgentHarborEvents :many
SELECT event_id, project_id, machine_id, machine_name, agent_type, session_id,
       turn_id, local_seq, event_type, occurred_at, received_at, evidence
FROM agentharbor_event
WHERE workspace_id = $1 AND project_id = $2
ORDER BY machine_id, session_id, local_seq, event_id;

-- name: DeleteAgentHarborEventsByProject :exec
DELETE FROM agentharbor_event
WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteAgentHarborEventsByWorkspace :exec
DELETE FROM agentharbor_event
WHERE workspace_id = $1;
