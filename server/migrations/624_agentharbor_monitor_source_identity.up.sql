CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS agentharbor_monitor_source_identity ON agentharbor_monitor_source (workspace_id, project_id, machine_id);
