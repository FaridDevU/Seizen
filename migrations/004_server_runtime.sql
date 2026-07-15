CREATE TABLE IF NOT EXISTS server_runtime_events (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    category TEXT NOT NULL CHECK (category IN ('provisioning', 'lifecycle', 'health', 'service', 'agent', 'error')),
    level TEXT NOT NULL CHECK (level IN ('info', 'warning', 'error')),
    message TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS server_runtime_events_server_idx
    ON server_runtime_events (server_id, created_at DESC);
