CREATE TABLE IF NOT EXISTS schema_migrations (
    name TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS experiments (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('app', 'server')),
    app_id TEXT NOT NULL,
    base_server_id TEXT,
    name TEXT NOT NULL,
    objective TEXT NOT NULL DEFAULT '',
    base_branch TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    base_commit TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN (
        'draft', 'creating', 'active', 'paused', 'awaiting_approval',
        'review_ready', 'integrating', 'integrated', 'conflicted', 'failed',
        'discarded', 'archived'
    )),
    created_by TEXT NOT NULL CHECK (created_by IN ('user', 'agent')),
    agent_session_id TEXT,
    risk_level TEXT NOT NULL DEFAULT 'low' CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
    risk_reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(risk_reasons_json)),
    configuration_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(configuration_json)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    review_ready_at TEXT,
    integrated_at TEXT,
    discarded_at TEXT,
    UNIQUE (id, project_id),
    FOREIGN KEY (app_id, project_id) REFERENCES apps(id, project_id) ON DELETE CASCADE,
    FOREIGN KEY (base_server_id, project_id) REFERENCES servers(id, project_id) ON DELETE RESTRICT,
    CHECK ((kind = 'server' AND base_server_id IS NOT NULL) OR kind = 'app')
);

CREATE TABLE IF NOT EXISTS experiment_targets (
    experiment_id TEXT NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
    target_type TEXT NOT NULL CHECK (target_type IN ('app', 'server')),
    target_id TEXT NOT NULL,
    PRIMARY KEY (experiment_id, target_type, target_id)
);

CREATE TABLE IF NOT EXISTS project_contexts (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    experiment_id TEXT,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (experiment_id, project_id) REFERENCES experiments(id, project_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS experiment_change_requests (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    plan_hash TEXT NOT NULL,
    input_json TEXT NOT NULL CHECK (json_valid(input_json)),
    analysis_json TEXT NOT NULL CHECK (json_valid(analysis_json)),
    approval_id TEXT,
    decision TEXT NOT NULL DEFAULT 'pending' CHECK (decision IN ('pending', 'approved', 'rejected', 'principal')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (session_id, project_id, plan_hash)
);

CREATE TABLE IF NOT EXISTS experiment_handoffs (
    id TEXT PRIMARY KEY,
    experiment_id TEXT NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
    source_session_id TEXT NOT NULL,
    target_session_id TEXT NOT NULL,
    objective TEXT NOT NULL,
    plan_json TEXT NOT NULL CHECK (json_valid(plan_json)),
    decisions_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(decisions_json)),
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS experiment_reviews (
    experiment_id TEXT PRIMARY KEY REFERENCES experiments(id) ON DELETE CASCADE,
    comparison_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(comparison_json)),
    tests_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tests_json)),
    secrets_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(secrets_json)),
    integration_path TEXT NOT NULL DEFAULT '',
    integration_commit TEXT NOT NULL DEFAULT '',
    main_head TEXT NOT NULL DEFAULT '',
    reproducible_verified INTEGER NOT NULL DEFAULT 0 CHECK (reproducible_verified IN (0, 1)),
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS experiments_project_kind_idx
    ON experiments (project_id, kind, updated_at DESC);
CREATE INDEX IF NOT EXISTS experiments_app_idx
    ON experiments (app_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS experiments_status_idx
    ON experiments (status, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS experiments_branch_idx
    ON experiments (project_id, branch_name) WHERE branch_name <> '';
CREATE UNIQUE INDEX IF NOT EXISTS experiments_worktree_idx
    ON experiments (worktree_path) WHERE worktree_path <> '';
CREATE INDEX IF NOT EXISTS experiment_targets_target_idx
    ON experiment_targets (target_type, target_id);
