CREATE TABLE IF NOT EXISTS agent_audit_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    app_id TEXT REFERENCES apps(id) ON DELETE SET NULL,
    tool_name TEXT NOT NULL,
    arguments_json TEXT NOT NULL DEFAULT '{}',
    success INTEGER NOT NULL CHECK (success IN (0, 1)),
    error_message TEXT NOT NULL DEFAULT '',
    approval_id TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_approvals (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    app_id TEXT REFERENCES apps(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    resource_id TEXT NOT NULL DEFAULT '',
    request_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'approved', 'denied', 'consumed', 'expired'
    )),
    expires_at TEXT NOT NULL,
    decided_at TEXT,
    consumed_at TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS agent_audit_project_idx
    ON agent_audit_events (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS agent_audit_session_idx
    ON agent_audit_events (session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS agent_approvals_pending_idx
    ON agent_approvals (project_id, status, expires_at);

