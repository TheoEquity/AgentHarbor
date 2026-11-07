CREATE INDEX CONCURRENTLY IF NOT EXISTS agentharbor_event_project_session_idx
    ON agentharbor_event (workspace_id, project_id, machine_id, session_id, local_seq DESC);
