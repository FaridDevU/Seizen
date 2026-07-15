CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    path TEXT NOT NULL UNIQUE,
    source TEXT NOT NULL CHECK (source IN ('created', 'imported', 'git')),
    git_remote TEXT,
    branch TEXT,
    favorite INTEGER NOT NULL DEFAULT 0 CHECK (favorite IN (0, 1)),
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    group_id TEXT,
    group_title TEXT,
    variant_label TEXT
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspace_layouts (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    layout TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS projects_active_order_idx
    ON projects (archived, favorite DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS projects_group_id_idx
    ON projects (group_id) WHERE group_id IS NOT NULL;
