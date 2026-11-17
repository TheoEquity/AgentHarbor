CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS agentharbor_monitor_turn_identity ON agentharbor_monitor_turn (workspace_id, project_id, machine_id, session_id, turn_id);
