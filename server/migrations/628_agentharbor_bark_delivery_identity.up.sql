CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS agentharbor_bark_delivery_identity ON agentharbor_bark_delivery (workspace_id, project_id, machine_id, session_id, turn_id, user_id);
