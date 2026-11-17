CREATE TABLE agentharbor_monitor_source (
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    machine_id UUID NOT NULL,
    machine_name TEXT NOT NULL,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    scan_status TEXT NOT NULL CHECK (scan_status IN ('ok', 'degraded'))
);

CREATE TABLE agentharbor_monitor_turn (
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    machine_id UUID NOT NULL,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    title TEXT NOT NULL,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'aborted')),
    historical BOOLEAN NOT NULL
);

CREATE TABLE agentharbor_monitor_handled (
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    machine_id UUID NOT NULL,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    user_id UUID NOT NULL
);

CREATE TABLE agentharbor_bark_setting (
    workspace_id UUID NOT NULL,
    user_id UUID NOT NULL,
    credentials BYTEA NOT NULL,
    enabled_since TIMESTAMPTZ
);

CREATE TABLE agentharbor_bark_delivery (
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    machine_id UUID NOT NULL,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    user_id UUID NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    attempts INTEGER NOT NULL DEFAULT 0,
    sent_at TIMESTAMPTZ
);
