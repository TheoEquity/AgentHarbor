CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS agentharbor_monitor_handled_identity ON agentharbor_monitor_handled (workspace_id, project_id, machine_id, session_id, turn_id, user_id);
