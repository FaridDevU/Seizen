package core

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationsUpgradeExistingProjectDatabaseIdempotently(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	path := filepath.Join(base, "config", "seizen.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.ExecContext(ctx, projectMigration); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.ExecContext(ctx, `CREATE TABLE schema_migrations (
name TEXT PRIMARY KEY,
applied_at TEXT NOT NULL
);
INSERT INTO schema_migrations (name, applied_at) VALUES ('005_app_mount', '2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.ExecContext(ctx, `INSERT INTO projects
(id, name, path, source, created_at, updated_at) VALUES
('legacy', 'Legacy', ?, 'created', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, filepath.Join(base, "Legacy")); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	database := newDatabase(path, filepath.Join(base, "projects"))
	if err = database.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := database.Pool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"apps", "app_runs", "servers", "server_services", "server_connections", "agent_audit_events", "agent_approvals", "server_runtime_events", "experiments", "experiment_targets", "project_contexts", "experiment_change_requests", "experiment_handoffs", "experiment_reviews", "schema_migrations"} {
		var count int
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing migrated table %s: count=%d err=%v", table, count, err)
		}
	}
	var projectCount, appForeignKey, serverIndex int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = 'legacy'`).Scan(&projectCount); err != nil || projectCount != 1 {
		t.Fatalf("legacy project was not preserved: count=%d err=%v", projectCount, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_list('servers') WHERE "table" = 'apps' AND "from" = 'app_id'`).Scan(&appForeignKey); err != nil || appForeignKey != 1 {
		t.Fatalf("servers App foreign key is missing: count=%d err=%v", appForeignKey, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'servers_app_idx'`).Scan(&serverIndex); err != nil || serverIndex != 1 {
		t.Fatalf("servers_app_idx is missing: count=%d err=%v", serverIndex, err)
	}
	for table, column := range map[string]string{
		"app_runs": "experiment_id", "servers": "experiment_id",
		"agent_approvals": "experiment_id", "workspace_layouts": "experiment_id",
	} {
		var count int
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing %s.%s: count=%d err=%v", table, column, count, err)
		}
	}
	var migrationCount int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name IN ('005_app_mount', '006_experiments')`).Scan(&migrationCount); err != nil || migrationCount != 2 {
		t.Fatalf("migration history was not preserved: count=%d err=%v", migrationCount, err)
	}
	database.Close()
	if err = database.Initialize(ctx); err != nil {
		t.Fatalf("migrations are not idempotent: %v", err)
	}
	database.Close()
}

func TestSQLitePersistsProjectsAndProjectRoot(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	databasePath := filepath.Join(base, "config", "seizen.db")
	defaultRoot := filepath.Join(base, "default-projects")
	database := newDatabase(databasePath, defaultRoot)
	if err := database.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	root, err := database.ProjectRoot(ctx)
	if err != nil || !samePath(root, defaultRoot) {
		t.Fatalf("expected default root %q, got %q, %v", defaultRoot, root, err)
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		t.Fatalf("expected the default root to exist: %v", statErr)
	}

	db, err := database.Pool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var journalMode string
	var busyTimeout, foreignKeys int
	if err = db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil || journalMode != "wal" {
		t.Fatalf("expected WAL mode, got %q, %v", journalMode, err)
	}
	if err = db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil || busyTimeout != 5000 {
		t.Fatalf("expected 5000ms busy timeout, got %d, %v", busyTimeout, err)
	}
	if err = db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("expected foreign keys enabled, got %d, %v", foreignKeys, err)
	}

	chosenRoot := filepath.Join(base, "workspace", "projects")
	root, err = database.SetProjectRoot(ctx, chosenRoot)
	if err != nil || !samePath(root, chosenRoot) {
		t.Fatalf("expected chosen root %q, got %q, %v", chosenRoot, root, err)
	}
	one, err := upsertProject(ctx, db, FSProjectInfo{Name: "Uno", Path: filepath.Join(root, "Uno")}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	two, err := upsertProject(ctx, db, FSProjectInfo{Name: "Dos", Path: filepath.Join(root, "Dos")}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{database: database}
	if err = app.GroupDuplicate(DuplicateGroup{
		Title: "Versiones",
		Variants: []DuplicateVariant{
			{ProjectID: one.ID, Label: "principal"},
			{ProjectID: two.ID, Label: "prueba"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	database.Close()
	reopened := newDatabase(databasePath, filepath.Join(base, "unused-default"))
	defer reopened.Close()
	root, err = reopened.ProjectRoot(ctx)
	if err != nil || !samePath(root, chosenRoot) {
		t.Fatalf("expected persisted root %q, got %q, %v", chosenRoot, root, err)
	}
	db, err = reopened.Pool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE group_title = ?`, "Versiones").Scan(&count); err != nil || count != 2 {
		t.Fatalf("expected two persisted grouped projects, got %d, %v", count, err)
	}
}
