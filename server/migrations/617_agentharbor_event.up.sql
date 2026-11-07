CREATE TABLE agentharbor_event (
    event_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    machine_id UUID NOT NULL,
    machine_name TEXT NOT NULL,
    agent_type TEXT NOT NULL CHECK (agent_type = 'codex'),
    session_id TEXT NOT NULL CHECK (session_id <> ''),
    turn_id TEXT NOT NULL DEFAULT '',
    local_seq BIGINT NOT NULL CHECK (local_seq > 0),
    event_type TEXT NOT NULL CHECK (event_type IN ('prompt_started', 'turn_stopped', 'interrupted')),
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    evidence TEXT NOT NULL DEFAULT '' CHECK (char_length(evidence) <= 512)
);
