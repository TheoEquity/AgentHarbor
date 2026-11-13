CREATE TABLE agentharbor_memory_material (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    submitted_by UUID NOT NULL,
    request_id UUID NOT NULL,
    source_text TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL DEFAULT '',
    related_entry_id UUID,
    related_revision INTEGER,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processed', 'discarded')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_by UUID,
    reviewed_at TIMESTAMPTZ
);

CREATE TABLE agentharbor_memory_entry (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID,
    owner_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('project', 'global')),
    type TEXT NOT NULL CHECK (type IN ('episodic', 'semantic', 'procedural')),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    source_material_ids UUID[] NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope = 'project' AND project_id IS NOT NULL) OR (scope = 'global' AND project_id IS NULL))
);

CREATE TABLE agentharbor_memory_revision (
    entry_id UUID NOT NULL,
    revision INTEGER NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    status TEXT NOT NULL,
    source_material_ids UUID[] NOT NULL,
    changed_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agentharbor_memory_settings (
    project_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    record_enabled BOOLEAN NOT NULL,
    cloud_enabled BOOLEAN NOT NULL,
    use_memory BOOLEAN NOT NULL,
    use_global BOOLEAN NOT NULL
);
