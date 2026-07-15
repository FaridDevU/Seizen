CREATE TABLE IF NOT EXISTS apps (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('web', 'desktop')),
    working_directory TEXT NOT NULL,
    start_command TEXT NOT NULL DEFAULT '',
    stop_command TEXT NOT NULL DEFAULT '',
    test_command TEXT NOT NULL DEFAULT '',
    executable TEXT NOT NULL DEFAULT '',
    arguments_json TEXT NOT NULL DEFAULT '[]',
    preview_url TEXT NOT NULL DEFAULT '',
    healthcheck_url TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'unconfigured' CHECK (status IN (
        'unconfigured', 'stopped', 'starting', 'running', 'testing', 'failed', 'stopping'
    )),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (id, project_id)
);

CREATE TABLE IF NOT EXISTS app_runs (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    target TEXT NOT NULL CHECK (target IN ('development', 'server')),
    runtime_provider TEXT NOT NULL,
    runtime_reference TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    preview_url TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    stopped_at TEXT,
    exit_code INTEGER,
    error_message TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS servers (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL,
    name TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN ('mock', 'wsl', 'incus')),
    distro TEXT NOT NULL DEFAULT 'Debian 12',
    runtime_reference TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN (
        'draft', 'provisioning', 'stopped', 'starting', 'running', 'degraded',
        'stopping', 'failed', 'deleting'
    )),
    cpu_limit REAL NOT NULL CHECK (cpu_limit > 0),
    memory_mb INTEGER NOT NULL CHECK (memory_mb > 0),
    disk_gb INTEGER NOT NULL CHECK (disk_gb > 0),
    keep_alive INTEGER NOT NULL DEFAULT 0 CHECK (keep_alive IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (id, project_id),
    FOREIGN KEY (app_id, project_id) REFERENCES apps(id, project_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_services (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    host TEXT NOT NULL DEFAULT '',
    port INTEGER CHECK (port IS NULL OR (port BETWEEN 1 AND 65535)),
    protocol TEXT NOT NULL DEFAULT '',
    healthcheck_url TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'unknown',
    source TEXT NOT NULL DEFAULT 'declared' CHECK (source IN ('declared', 'verified', 'observed')),
    metadata_json TEXT NOT NULL DEFAULT '{}',
    position_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE (id, server_id)
);

CREATE TABLE IF NOT EXISTS server_connections (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    source_service_id TEXT,
    target_service_id TEXT,
    protocol TEXT NOT NULL,
    port INTEGER CHECK (port IS NULL OR (port BETWEEN 1 AND 65535)),
    status TEXT NOT NULL DEFAULT 'unknown',
    source TEXT NOT NULL DEFAULT 'declared' CHECK (source IN ('declared', 'verified', 'observed')),
    traffic_rate REAL NOT NULL DEFAULT 0 CHECK (traffic_rate >= 0),
    error_rate REAL NOT NULL DEFAULT 0 CHECK (error_rate >= 0),
    metadata_json TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (source_service_id, server_id) REFERENCES server_services(id, server_id) ON DELETE CASCADE,
    FOREIGN KEY (target_service_id, server_id) REFERENCES server_services(id, server_id) ON DELETE CASCADE,
    CHECK (source_service_id IS NOT NULL OR target_service_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS apps_project_idx ON apps (project_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS apps_status_idx ON apps (status);
CREATE INDEX IF NOT EXISTS app_runs_app_idx ON app_runs (app_id, started_at DESC);
CREATE INDEX IF NOT EXISTS servers_project_idx ON servers (project_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS servers_app_idx ON servers (app_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS servers_status_idx ON servers (status);
CREATE INDEX IF NOT EXISTS server_services_server_idx ON server_services (server_id);
CREATE INDEX IF NOT EXISTS server_connections_server_idx ON server_connections (server_id);
