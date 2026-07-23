package core

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_projects.sql
var projectMigration string

//go:embed migrations/002_apps_servers.sql
var appServerMigration string

//go:embed migrations/003_agent_bridge.sql
var agentBridgeMigration string

//go:embed migrations/004_server_runtime.sql
var serverRuntimeMigration string

//go:embed migrations/005_app_mounting.sql
var appMountingMigration string

//go:embed migrations/006_experiments.sql
var experimentMigration string

//go:embed migrations/007_assistant_chats.sql
var assistantChatMigration string

const (
	projectRootSetting      = "project_root"
	appearanceModeSetting   = "appearance_mode"
	appearanceAccentSetting = "appearance_accent"
)

type Appearance struct {
	Mode   string `json:"mode"`
	Accent string `json:"accent"`
}

var defaultAppearance = Appearance{Mode: "light", Accent: "blue"}

type Database struct {
	mu                 sync.Mutex
	db                 *sql.DB
	path               string
	defaultProjectRoot string
}

func NewDatabase() *Database {
	return &Database{}
}

func newDatabase(path, defaultProjectRoot string) *Database {
	return &Database{path: path, defaultProjectRoot: defaultProjectRoot}
}

func (d *Database) Initialize(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db != nil {
		return nil
	}

	path, err := d.databasePath()
	if err != nil {
		return err
	}
	dataDir := filepath.Dir(path)
	if err = os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("could not create the Seizen data folder: %w", err)
	}
	if err = os.Chmod(dataDir, 0o700); err != nil {
		return fmt.Errorf("could not protect the Seizen data folder: %w", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return fmt.Errorf("could not prepare the local database: %w", err)
	}
	// ponytail: one connection is enough for a desktop library and keeps SQLite settings deterministic.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.PingContext(ctx); err == nil {
		err = os.Chmod(path, 0o600)
	}
	if err == nil {
		for _, migration := range []string{projectMigration, appServerMigration, agentBridgeMigration, serverRuntimeMigration, assistantChatMigration} {
			if _, err = db.ExecContext(ctx, migration); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = applyAppMountingMigration(ctx, db)
	}
	if err == nil {
		err = applyExperimentMigration(ctx, db)
	}
	if err == nil {
		// Job Objects kill these processes when Seizen exits, so stale active states are never truthful.
		_, err = db.ExecContext(ctx, `UPDATE apps SET status = 'stopped', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status IN ('starting', 'running', 'testing', 'stopping');
UPDATE app_runs SET status = 'stopped', stopped_at = COALESCE(stopped_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
WHERE stopped_at IS NULL AND status IN ('starting', 'running', 'testing', 'stopping');`)
	}
	if err == nil {
		err = d.initializeProjectRoot(ctx, db)
	}
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("could not initialize the local database: %w", err)
	}

	d.path = path
	d.db = db
	return nil
}

func applyAppMountingMigration(ctx context.Context, db *sql.DB) error {
	columns := map[string]bool{}
	readColumns := func(table string) error {
		rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, kind string
			var notNull, primaryKey int
			var defaultValue sql.NullString
			if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
				return err
			}
			columns[table+"."+name] = true
		}
		return rows.Err()
	}
	if err := readColumns("apps"); err != nil {
		return err
	}
	if err := readColumns("app_runs"); err != nil {
		return err
	}
	statements := strings.Split(appMountingMigration, ";")
	keys := []string{"apps.is_primary", "app_runs.terminal_session_id", "app_runs.ownership", "app_runs.discovery_source", "app_runs.detected_port", "app_runs.last_verified_at", "", "", "", ""}
	for index, statement := range statements {
		statement = strings.TrimSpace(statement)
		if statement == "" || (index < len(keys) && keys[index] != "" && columns[keys[index]]) {
			continue
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("could not apply the App mounting migration: %w", err)
		}
	}
	return nil
}

func applyExperimentMigration(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, experimentMigration); err != nil {
		return fmt.Errorf("could not create the experiments model: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	hasColumn := func(table, column string) (bool, error) {
		rows, queryErr := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if queryErr != nil {
			return false, queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, kind string
			var defaultValue sql.NullString
			if queryErr = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); queryErr != nil {
				return false, queryErr
			}
			if name == column {
				return true, nil
			}
		}
		return false, rows.Err()
	}
	ensureColumn := func(table, column, definition string) error {
		exists, columnErr := hasColumn(table, column)
		if columnErr != nil || exists {
			return columnErr
		}
		_, columnErr = tx.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
		return columnErr
	}
	for _, column := range []struct{ table, name, definition string }{
		{"experiments", "configuration_json", "TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(configuration_json))"},
		{"servers", "experiment_id", "TEXT REFERENCES experiments(id) ON DELETE CASCADE"},
		{"servers", "base_server_id", "TEXT REFERENCES servers(id) ON DELETE RESTRICT"},
		{"agent_approvals", "experiment_id", "TEXT REFERENCES experiments(id) ON DELETE CASCADE"},
		{"agent_audit_events", "experiment_id", "TEXT REFERENCES experiments(id) ON DELETE SET NULL"},
	} {
		if err = ensureColumn(column.table, column.name, column.definition); err != nil {
			return fmt.Errorf("could not add %s.%s: %w", column.table, column.name, err)
		}
	}
	appRunsProject, err := hasColumn("app_runs", "project_id")
	if err != nil {
		return err
	}
	appRunsExperiment, err := hasColumn("app_runs", "experiment_id")
	if err != nil {
		return err
	}
	if !appRunsProject || !appRunsExperiment {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE app_runs RENAME TO app_runs_legacy;
CREATE TABLE app_runs (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL,
    experiment_id TEXT,
    target TEXT NOT NULL CHECK (target IN ('development', 'server')),
    runtime_provider TEXT NOT NULL,
    runtime_reference TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    preview_url TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    stopped_at TEXT,
    exit_code INTEGER,
    error_message TEXT NOT NULL DEFAULT '',
    terminal_session_id TEXT NOT NULL DEFAULT '',
    ownership TEXT NOT NULL DEFAULT 'managed' CHECK (ownership IN ('managed', 'attached')),
    discovery_source TEXT NOT NULL DEFAULT 'manual' CHECK (discovery_source IN ('manual', 'detected', 'agent')),
    detected_port INTEGER CHECK (detected_port IS NULL OR detected_port BETWEEN 1 AND 65535),
    last_verified_at TEXT,
    FOREIGN KEY (app_id, project_id) REFERENCES apps(id, project_id) ON DELETE CASCADE,
    FOREIGN KEY (experiment_id, project_id) REFERENCES experiments(id, project_id) ON DELETE CASCADE
);
INSERT INTO app_runs (id, project_id, app_id, target, runtime_provider, runtime_reference,
status, preview_url, started_at, stopped_at, exit_code, error_message, terminal_session_id,
ownership, discovery_source, detected_port, last_verified_at)
SELECT legacy.id, apps.project_id, legacy.app_id, legacy.target, legacy.runtime_provider,
legacy.runtime_reference, legacy.status, legacy.preview_url, legacy.started_at, legacy.stopped_at,
legacy.exit_code, legacy.error_message, legacy.terminal_session_id, legacy.ownership,
legacy.discovery_source, legacy.detected_port, legacy.last_verified_at
FROM app_runs_legacy AS legacy JOIN apps ON apps.id = legacy.app_id;
DROP TABLE app_runs_legacy;
CREATE INDEX app_runs_app_idx ON app_runs (app_id, started_at DESC);
CREATE INDEX app_runs_context_idx ON app_runs (project_id, experiment_id, started_at DESC);
CREATE INDEX app_runs_terminal_idx ON app_runs (terminal_session_id) WHERE terminal_session_id <> '';`); err != nil {
			return fmt.Errorf("could not isolate App runs: %w", err)
		}
	}
	workspaceExperiment, err := hasColumn("workspace_layouts", "experiment_id")
	if err != nil {
		return err
	}
	if !workspaceExperiment {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE workspace_layouts RENAME TO workspace_layouts_legacy;
CREATE TABLE workspace_layouts (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    experiment_id TEXT,
    context_key TEXT NOT NULL DEFAULT '',
    layout TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, context_key),
    FOREIGN KEY (experiment_id, project_id) REFERENCES experiments(id, project_id) ON DELETE CASCADE,
    CHECK ((experiment_id IS NULL AND context_key = '') OR experiment_id = context_key)
);
INSERT INTO workspace_layouts (project_id, experiment_id, context_key, layout, updated_at)
SELECT project_id, NULL, '', layout, updated_at FROM workspace_layouts_legacy;
DROP TABLE workspace_layouts_legacy;
CREATE INDEX workspace_layouts_experiment_idx ON workspace_layouts (experiment_id) WHERE experiment_id IS NOT NULL;`); err != nil {
			return fmt.Errorf("could not isolate the canvases: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS servers_experiment_idx ON servers (project_id, experiment_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS agent_approvals_experiment_idx ON agent_approvals (experiment_id, status, expires_at);
CREATE INDEX IF NOT EXISTS agent_audit_experiment_idx ON agent_audit_events (experiment_id, created_at DESC);
CREATE TRIGGER IF NOT EXISTS servers_experiment_scope_insert BEFORE INSERT ON servers
WHEN NEW.experiment_id IS NOT NULL BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM experiments WHERE id = NEW.experiment_id
        AND project_id = NEW.project_id AND app_id = NEW.app_id
        AND (kind = 'app' OR base_server_id = NEW.base_server_id)
    ) THEN RAISE(ABORT, 'experiment server scope mismatch') END;
END;
CREATE TRIGGER IF NOT EXISTS servers_experiment_scope_update BEFORE UPDATE OF project_id, app_id, experiment_id, base_server_id ON servers
WHEN NEW.experiment_id IS NOT NULL BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM experiments WHERE id = NEW.experiment_id
        AND project_id = NEW.project_id AND app_id = NEW.app_id
        AND (kind = 'app' OR base_server_id = NEW.base_server_id)
    ) THEN RAISE(ABORT, 'experiment server scope mismatch') END;
END;
CREATE TRIGGER IF NOT EXISTS servers_main_scope_insert BEFORE INSERT ON servers
WHEN NEW.experiment_id IS NULL AND NEW.base_server_id IS NOT NULL BEGIN
    SELECT RAISE(ABORT, 'main server cannot have a base server');
END;
INSERT OR IGNORE INTO schema_migrations (name, applied_at)
VALUES ('006_experiments', `+projectNow+`);`); err != nil {
		return fmt.Errorf("could not complete the experiments migration: %w", err)
	}
	return tx.Commit()
}

func (d *Database) Pool(ctx context.Context) (*sql.DB, error) {
	if err := d.Initialize(ctx); err != nil {
		return nil, err
	}
	d.mu.Lock()
	db := d.db
	d.mu.Unlock()
	if db == nil {
		return nil, errors.New("the local database is closed")
	}
	return db, nil
}

func (d *Database) ProjectRoot(ctx context.Context) (string, error) {
	db, err := d.Pool(ctx)
	if err != nil {
		return "", err
	}
	var root string
	if err = db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, projectRootSetting).Scan(&root); err != nil {
		return "", fmt.Errorf("could not load the projects folder: %w", err)
	}
	root, err = ensureProjectRoot(root)
	if err != nil {
		return "", err
	}
	return root, nil
}

func (d *Database) SetProjectRoot(ctx context.Context, path string) (string, error) {
	root, err := ensureProjectRoot(path)
	if err != nil {
		return "", err
	}
	db, err := d.Pool(ctx)
	if err != nil {
		return "", err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, projectRootSetting, root)
	if err != nil {
		return "", fmt.Errorf("could not save the projects folder: %w", err)
	}
	return root, nil
}

func (d *Database) Appearance(ctx context.Context) (Appearance, error) {
	db, err := d.Pool(ctx)
	if err != nil {
		return Appearance{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN (?, ?)`, appearanceModeSetting, appearanceAccentSetting)
	if err != nil {
		return Appearance{}, fmt.Errorf("could not load the appearance: %w", err)
	}
	defer rows.Close()

	appearance := defaultAppearance
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return Appearance{}, fmt.Errorf("could not load the appearance: %w", err)
		}
		switch key {
		case appearanceModeSetting:
			appearance.Mode = value
		case appearanceAccentSetting:
			appearance.Accent = value
		}
	}
	if err = rows.Err(); err != nil {
		return Appearance{}, fmt.Errorf("could not load the appearance: %w", err)
	}
	return appearance, nil
}

func (d *Database) SetAppearance(ctx context.Context, mode, accent string) (Appearance, error) {
	if mode != "light" && mode != "dark" {
		return Appearance{}, errors.New("the appearance mode is not valid")
	}
	if accent != "blue" && accent != "violet" && accent != "emerald" && accent != "amber" {
		return Appearance{}, errors.New("the accent color is not valid")
	}
	db, err := d.Pool(ctx)
	if err != nil {
		return Appearance{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Appearance{}, fmt.Errorf("could not save the appearance: %w", err)
	}
	defer tx.Rollback()

	const upsert = `INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`
	if _, err = tx.ExecContext(ctx, upsert, appearanceModeSetting, mode); err == nil {
		_, err = tx.ExecContext(ctx, upsert, appearanceAccentSetting, accent)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return Appearance{}, fmt.Errorf("could not save the appearance: %w", err)
	}
	return Appearance{Mode: mode, Accent: accent}, nil
}

func (d *Database) Close() {
	d.mu.Lock()
	db := d.db
	d.db = nil
	d.mu.Unlock()
	if db != nil {
		_ = db.Close()
	}
}

func (d *Database) databasePath() (string, error) {
	if d.path != "" {
		return filepath.Abs(d.path)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not find the configuration folder: %w", err)
	}
	return filepath.Join(configDir, "Seizen", "seizen.db"), nil
}

func (d *Database) initializeProjectRoot(ctx context.Context, db *sql.DB) error {
	var root string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, projectRootSetting).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		root = d.defaultProjectRoot
		if root == "" {
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				return fmt.Errorf("could not find the home folder: %w", homeErr)
			}
			root = filepath.Join(home, "Seizen", "Projects")
		}
	} else if err != nil {
		return err
	}

	root, err = ensureProjectRoot(root)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, projectRootSetting, root)
	return err
}

func ensureProjectRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return "", errors.New("the projects folder is not valid")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("the projects folder is not valid: %w", err)
	}
	if err = os.MkdirAll(absolute, 0o755); err != nil {
		return "", fmt.Errorf("could not create the projects folder: %w", err)
	}
	root, err := canonicalPath(absolute)
	if err != nil {
		return "", fmt.Errorf("could not open the projects folder: %w", err)
	}
	return displayPath(root), nil
}

func sqliteDSN(path string) string {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: query.Encode(),
	}).String()
}
