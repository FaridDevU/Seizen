package core

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"time"
)

func (a *App) attachedAppStatus(ctx context.Context, appID string) (AppRuntimeStatus, bool, error) {
	db, err := a.database.Pool(ctx)
	if err != nil {
		return AppRuntimeStatus{}, false, err
	}
	var terminalID, previewURL, discoverySource string
	var detectedPort sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT terminal_session_id, preview_url, detected_port, discovery_source
FROM app_runs WHERE app_id = ? AND ownership = 'attached' AND stopped_at IS NULL
ORDER BY started_at DESC LIMIT 1`, appID).Scan(&terminalID, &previewURL, &detectedPort, &discoverySource)
	if errors.Is(err, sql.ErrNoRows) {
		return AppRuntimeStatus{}, false, nil
	}
	if err != nil {
		return AppRuntimeStatus{}, true, err
	}
	app, err := getProjectApp(ctx, db, appID)
	if err != nil {
		return AppRuntimeStatus{}, true, err
	}
	manager := a.currentTerminalManager()
	sessionAlive := manager != nil && manager.session(terminalID) != nil
	parsed, _ := url.Parse(previewURL)
	host, port := parsed.Hostname(), int(detectedPort.Int64)
	if port == 0 {
		port, _ = strconv.Atoi(parsed.Port())
		if port == 0 && parsed.Scheme == "http" {
			port = 80
		} else if port == 0 {
			port = 443
		}
	}
	endpointAlive := sessionAlive && canConnectLocalPort(host, port)
	if endpointAlive && discoverySource != "manual" {
		endpointAlive = a.terminalOwnsPort(terminalID, port)
	}
	if sessionAlive && !endpointAlive {
		time.Sleep(100 * time.Millisecond)
		endpointAlive = canConnectLocalPort(host, port)
		if endpointAlive && discoverySource != "manual" {
			endpointAlive = a.terminalOwnsPort(terminalID, port)
		}
	}
	if !sessionAlive {
		a.handleAttachedTerminalExit(terminalID, "")
		app, _ = getProjectApp(ctx, db, appID)
	} else if !endpointAlive {
		a.finishAttachedApp(appID, "failed", "the verified port stopped responding")
		app, _ = getProjectApp(ctx, db, appID)
	} else {
		_, _ = db.ExecContext(ctx, `UPDATE app_runs SET last_verified_at = `+projectNow+`
WHERE app_id = ? AND ownership = 'attached' AND stopped_at IS NULL`, appID)
	}
	return AppRuntimeStatus{
		App: app, RuntimeReference: terminalID, ProcessAlive: sessionAlive && endpointAlive,
		HealthcheckPassed: endpointAlive,
	}, true, nil
}

func (a *App) stopAttachedApp(ctx context.Context, appID string) (ProjectApp, bool, error) {
	db, err := a.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, false, err
	}
	var terminalID, previewURL string
	var port sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT terminal_session_id, detected_port, preview_url FROM app_runs
WHERE app_id = ? AND ownership = 'attached' AND stopped_at IS NULL ORDER BY started_at DESC LIMIT 1`, appID).Scan(&terminalID, &port, &previewURL)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectApp{}, false, nil
	}
	if err != nil {
		return ProjectApp{}, true, err
	}
	manager := a.currentTerminalManager()
	if manager != nil && manager.session(terminalID) != nil {
		parsed, _ := url.Parse(previewURL)
		host := parsed.Hostname()
		if !port.Valid {
			parsedPort, _ := strconv.Atoi(parsed.Port())
			if parsedPort == 0 && parsed.Scheme == "http" {
				parsedPort = 80
			} else if parsedPort == 0 {
				parsedPort = 443
			}
			port = sql.NullInt64{Int64: int64(parsedPort), Valid: parsedPort > 0}
		}
		_ = manager.writeBytes(terminalID, []byte{3})
		deadline := time.Now().Add(3 * time.Second)
		for port.Valid && canConnectLocalPort(host, int(port.Int64)) && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if port.Valid && canConnectLocalPort(host, int(port.Int64)) {
			_ = manager.stop(terminalID)
		}
	}
	app, err := scanProjectApp(db.QueryRowContext(ctx, `UPDATE apps SET status = 'stopped', updated_at = `+projectNow+`
WHERE id = ? RETURNING `+appColumns, appID))
	if err == nil {
		_, err = db.ExecContext(ctx, `UPDATE app_runs SET status = 'stopped', stopped_at = `+projectNow+`
WHERE app_id = ? AND ownership = 'attached' AND stopped_at IS NULL`, appID)
	}
	if err == nil {
		a.emitAgentEvent("app.stopped", app)
	}
	return app, true, err
}

func (a *App) handleAttachedTerminalExit(sessionID, terminalError string) {
	if sessionID == "" {
		return
	}
	db, err := a.database.Pool(context.Background())
	if err != nil {
		return
	}
	status := "stopped"
	if terminalError != "" {
		status = "failed"
	}
	rows, err := db.Query(`SELECT app_id FROM app_runs WHERE terminal_session_id = ? AND ownership = 'attached' AND stopped_at IS NULL`, sessionID)
	if err != nil {
		return
	}
	appIDs := make([]string, 0)
	for rows.Next() {
		var appID string
		if rows.Scan(&appID) == nil {
			appIDs = append(appIDs, appID)
		}
	}
	_ = rows.Close()
	for _, appID := range appIDs {
		a.finishAttachedApp(appID, status, terminalError)
	}
}

func (a *App) finishAttachedApp(appID, status, message string) {
	db, err := a.database.Pool(context.Background())
	if err != nil {
		return
	}
	_, _ = db.Exec(`UPDATE app_runs SET status = ?, stopped_at = `+projectNow+`, error_message = ?
WHERE app_id = ? AND ownership = 'attached' AND stopped_at IS NULL`, status, message, appID)
	app, updateErr := setAppRuntimeState(context.Background(), db, appID, status)
	if updateErr == nil {
		a.emitAgentEvent("app."+status, app)
	}
}

func (a *App) cleanupAttachedApps(ctx context.Context, projectID string) error {
	db, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	query := `SELECT DISTINCT apps.id FROM apps JOIN app_runs ON app_runs.app_id = apps.id
WHERE app_runs.ownership = 'attached' AND app_runs.stopped_at IS NULL`
	args := []any{}
	if projectID != "" {
		query += ` AND apps.project_id = ?`
		args = append(args, projectID)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		if _, _, stopErr := a.stopAttachedApp(ctx, id); stopErr != nil {
			return stopErr
		}
	}
	return nil
}
