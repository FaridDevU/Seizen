package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const maxWorkspaceLayoutSize = 1 << 20

func (a *App) GetProjectWorkspace(projectID, path string) (string, error) {
	return a.getProjectWorkspace(projectID, "", path)
}

func (a *App) GetProjectWorkspaceContext(projectID, experimentID string) (string, error) {
	return a.getProjectWorkspace(projectID, experimentID, "")
}

func (a *App) getProjectWorkspace(projectID, experimentID, requestedPath string) (string, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return "", err
	}

	storedPath, err := projectPathForExperiment(ctx, db, projectID, experimentID)
	if err != nil {
		return "", err
	}
	if requestedPath != "" && !sameRequestedPath(storedPath, requestedPath) {
		return "", errors.New("the given path does not match the project's saved path")
	}
	var layout sql.NullString
	err = db.QueryRowContext(ctx, `SELECT layout FROM workspace_layouts WHERE project_id = ? AND context_key = ?`, projectID, experimentID).Scan(&layout)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not load the canvas: %w", err)
	}
	if !layout.Valid {
		return "", nil
	}
	if err = validateWorkspaceLayout(layout.String); err != nil {
		return "", fmt.Errorf("the saved canvas is not valid: %w", err)
	}
	return layout.String, nil
}

func (a *App) SaveProjectWorkspace(projectID, path, layout string) error {
	return a.saveProjectWorkspace(projectID, "", path, layout)
}

func (a *App) SaveProjectWorkspaceContext(projectID, experimentID, layout string) error {
	return a.saveProjectWorkspace(projectID, experimentID, "", layout)
}

func (a *App) saveProjectWorkspace(projectID, experimentID, requestedPath, layout string) error {
	if err := validateWorkspaceLayout(layout); err != nil {
		return err
	}

	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	storedPath, err := projectPathForExperiment(ctx, db, projectID, experimentID)
	if err != nil {
		return err
	}
	if requestedPath != "" && !sameRequestedPath(storedPath, requestedPath) {
		return errors.New("the given path does not match the project's saved path")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not start saving the canvas: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_layouts (project_id, experiment_id, context_key, layout, updated_at)
VALUES (?, NULLIF(?, ''), ?, ?, `+projectNow+`)
ON CONFLICT (project_id, context_key) DO UPDATE SET
    layout = excluded.layout,
    updated_at = excluded.updated_at`, projectID, experimentID, experimentID, layout)
	if err != nil {
		return fmt.Errorf("could not save the canvas: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("could not complete saving the canvas: %w", err)
	}
	return nil
}

func validateWorkspaceLayout(layout string) error {
	if len(layout) > maxWorkspaceLayoutSize {
		return errors.New("the canvas exceeds the 1 MiB limit")
	}
	if !utf8.ValidString(layout) || !json.Valid([]byte(layout)) {
		return errors.New("the canvas must be valid JSON")
	}
	return nil
}
