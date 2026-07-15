package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type serverOperation struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// ServerManager is the only runtime path for persisted servers. Providers do
// platform work; this manager owns database transitions, cancellation and events.
type ServerManager struct {
	mu         sync.Mutex
	database   *Database
	providers  map[string]ServerProvider
	operations map[string]*serverOperation
	emit       func(string, any)
}

func NewServerManager(database *Database, emit func(string, any)) *ServerManager {
	return newServerManager(database, emit, map[string]ServerProvider{
		"mock":  defaultMockServerProvider,
		"wsl":   NewWslServerProvider(),
		"incus": IncusServerProvider{},
	})
}

func newServerManager(database *Database, emit func(string, any), providers map[string]ServerProvider) *ServerManager {
	if emit == nil {
		emit = func(string, any) {}
	}
	return &ServerManager{
		database:   database,
		providers:  providers,
		operations: make(map[string]*serverOperation),
		emit:       emit,
	}
}

func (manager *ServerManager) StartServer(ctx context.Context, id string) (Server, error) {
	operationContext, finish, err := manager.beginOperation(ctx, id)
	if err != nil {
		return Server{}, err
	}
	defer finish()

	db, server, provider, err := manager.load(operationContext, id)
	if err != nil {
		return Server{}, err
	}
	if server.Status == "running" || server.Status == "degraded" {
		return server, nil
	}
	if server.Status != "draft" && server.Status != "stopped" && server.Status != "failed" {
		return Server{}, fmt.Errorf("cannot start a server in state %s", server.Status)
	}

	if server.RuntimeReference == "" {
		server, err = manager.transition(operationContext, db, server.ID, "provisioning", "")
		if err != nil {
			return Server{}, err
		}
		reference, createErr := provider.Create(operationContext, server)
		if createErr != nil {
			if reference != "" {
				server.RuntimeReference = reference
			}
			return manager.fail(operationContext, db, server, createErr)
		}
		provisioned := server
		provisioned.RuntimeReference = reference
		server, err = manager.transition(operationContext, db, server.ID, "starting", reference)
		if err != nil {
			cleanupErr := provider.Destroy(context.WithoutCancel(operationContext), provisioned)
			return Server{}, errors.Join(err, cleanupErr)
		}
	} else {
		server, err = manager.transition(operationContext, db, server.ID, "starting", server.RuntimeReference)
		if err != nil {
			return Server{}, err
		}
	}
	err = provider.Start(operationContext, server)
	if err != nil {
		return manager.fail(operationContext, db, server, err)
	}
	return manager.transition(operationContext, db, server.ID, "running", server.RuntimeReference)
}

func (manager *ServerManager) StopServer(ctx context.Context, id string) (Server, error) {
	if err := manager.cancelOperation(ctx, id); err != nil {
		return Server{}, err
	}
	operationContext, finish, err := manager.beginOperation(ctx, id)
	if err != nil {
		return Server{}, err
	}
	defer finish()

	db, server, provider, err := manager.load(operationContext, id)
	if err != nil {
		return Server{}, err
	}
	if server.Status == "draft" || server.Status == "stopped" {
		return server, nil
	}
	if server.Status == "deleting" {
		return Server{}, errors.New("the server is being deleted")
	}
	if server.RuntimeReference == "" {
		return manager.transition(operationContext, db, server.ID, "stopped", "")
	}
	server, err = manager.transition(operationContext, db, server.ID, "stopping", server.RuntimeReference)
	if err != nil {
		return Server{}, err
	}
	err = provider.Stop(operationContext, server)
	if err != nil {
		return manager.fail(operationContext, db, server, err)
	}
	return manager.transition(operationContext, db, server.ID, "stopped", server.RuntimeReference)
}

func (manager *ServerManager) RestartServer(ctx context.Context, id string) (Server, error) {
	if _, err := manager.StopServer(ctx, id); err != nil {
		return Server{}, err
	}
	return manager.StartServer(ctx, id)
}

func (manager *ServerManager) DestroyServer(ctx context.Context, id string) error {
	if err := manager.cancelOperation(ctx, id); err != nil {
		return err
	}
	operationContext, finish, err := manager.beginOperation(ctx, id)
	if err != nil {
		return err
	}
	defer finish()

	db, server, provider, err := manager.load(operationContext, id)
	if err != nil {
		return err
	}
	if server.Status != "draft" && server.Status != "stopped" && server.Status != "failed" {
		return errors.New("stop the server before deleting it")
	}
	server, err = manager.transition(operationContext, db, server.ID, "deleting", server.RuntimeReference)
	if err != nil {
		return err
	}
	if server.RuntimeReference != "" {
		if err = provider.Destroy(operationContext, server); err != nil {
			_, _ = manager.fail(operationContext, db, server, err)
			return err
		}
	}
	result, err := db.ExecContext(operationContext, `DELETE FROM servers WHERE id = ? AND status = 'deleting'`, id)
	if err != nil {
		_, _ = manager.transition(context.WithoutCancel(operationContext), db, id, "failed", "")
		return fmt.Errorf("could not delete the server: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return errors.New("the server to delete was not found")
	}
	manager.emit("server.deleted", server)
	return nil
}

func (manager *ServerManager) Exec(ctx context.Context, id, command string) (ServerExecResult, error) {
	if strings.TrimSpace(command) == "" {
		return ServerExecResult{}, errors.New("the command is empty")
	}
	_, server, provider, err := manager.load(ctx, id)
	if err != nil {
		return ServerExecResult{}, err
	}
	if server.Status != "running" && server.Status != "degraded" {
		return ServerExecResult{}, errors.New("the server is stopped")
	}
	return provider.Exec(ctx, server, command)
}

func (manager *ServerManager) Stats(ctx context.Context, id string) (ServerStats, error) {
	operationContext, finish, err := manager.beginOperation(ctx, id)
	if err != nil {
		return ServerStats{}, err
	}
	defer finish()
	_, server, provider, err := manager.load(operationContext, id)
	if err != nil {
		return ServerStats{}, err
	}
	if server.Status != "running" && server.Status != "degraded" {
		return ServerStats{}, errors.New("the server is stopped")
	}
	return provider.Stats(operationContext, server)
}

func (manager *ServerManager) InspectServices(ctx context.Context, id string) ([]ServerService, error) {
	operationContext, finish, err := manager.beginOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	defer finish()
	_, server, provider, err := manager.load(operationContext, id)
	if err != nil {
		return nil, err
	}
	if server.Status != "running" && server.Status != "degraded" {
		return nil, errors.New("the server is stopped")
	}
	return provider.InspectServices(operationContext, server)
}

func (manager *ServerManager) CheckHealth(ctx context.Context, id string) (ServerHealth, error) {
	operationContext, finish, err := manager.beginOperation(ctx, id)
	if err != nil {
		return ServerHealth{}, err
	}
	defer finish()
	db, server, provider, err := manager.load(operationContext, id)
	if err != nil {
		return ServerHealth{}, err
	}
	if server.Status != "running" && server.Status != "degraded" {
		return ServerHealth{Healthy: false, Message: "stopped"}, nil
	}
	health, err := provider.CheckHealth(operationContext, server)
	if err != nil {
		manager.logEvent(operationContext, db, server.ID, "health", "error", err.Error())
		return ServerHealth{}, err
	}
	level := "warning"
	if health.Healthy {
		level = "info"
	}
	manager.logEvent(operationContext, db, server.ID, "health", level, health.Message)
	if server.Status == "running" && !health.Healthy {
		_, _, _ = manager.transitionFrom(operationContext, db, server.ID, "running", "degraded", server.RuntimeReference)
	} else if server.Status == "degraded" && health.Healthy {
		_, _, _ = manager.transitionFrom(operationContext, db, server.ID, "degraded", "running", server.RuntimeReference)
	}
	return health, nil
}

func (manager *ServerManager) CleanupProjectServers(ctx context.Context, projectID string) error {
	return manager.cleanup(ctx, `WHERE project_id = ?`, projectID)
}

func (manager *ServerManager) PrepareServerClose(ctx context.Context) error {
	return manager.cleanup(ctx, "")
}

func (manager *ServerManager) ReconcileInterrupted(ctx context.Context) error {
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM servers WHERE status IN
('provisioning', 'starting', 'running', 'degraded', 'stopping', 'deleting')`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = errors.Join(rows.Err(), rows.Close())
	for _, id := range ids {
		server, loadErr := getServer(ctx, db, id)
		if loadErr != nil {
			err = errors.Join(err, loadErr)
			continue
		}
		provider := manager.providers[server.Provider]
		stopErr := error(nil)
		if provider == nil {
			stopErr = fmt.Errorf("unknown provider: %s", server.Provider)
		} else if server.RuntimeReference != "" {
			stopErr = provider.Stop(ctx, server)
		}
		status := "stopped"
		if stopErr != nil {
			status = "failed"
			err = errors.Join(err, stopErr)
		}
		_, stateErr := manager.transition(context.WithoutCancel(ctx), db, id, status, server.RuntimeReference)
		err = errors.Join(err, stateErr)
	}
	return err
}

func (manager *ServerManager) cleanup(ctx context.Context, where string, arguments ...any) error {
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM servers `+where, arguments...)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		ids = append(ids, id)
	}
	err = errors.Join(rows.Err(), rows.Close())
	for _, id := range ids {
		if cancelErr := manager.cancelOperation(ctx, id); cancelErr != nil {
			err = errors.Join(err, cancelErr)
			continue
		}
		if _, stopErr := manager.StopServer(ctx, id); stopErr != nil {
			err = errors.Join(err, fmt.Errorf("could not stop %s: %w", id, stopErr))
		}
	}
	return err
}

func (manager *ServerManager) load(ctx context.Context, id string) (*sql.DB, Server, ServerProvider, error) {
	if strings.TrimSpace(id) == "" {
		return nil, Server{}, nil, errors.New("the server is required")
	}
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return nil, Server{}, nil, err
	}
	server, err := getServer(ctx, db, id)
	if err != nil {
		return nil, Server{}, nil, err
	}
	provider := manager.providers[server.Provider]
	if provider == nil {
		return nil, Server{}, nil, fmt.Errorf("unknown server provider: %s", server.Provider)
	}
	return db, server, provider, nil
}

func (manager *ServerManager) transition(ctx context.Context, db *sql.DB, id, status, reference string) (Server, error) {
	server, err := updateServerState(ctx, db, id, status, reference)
	if err == nil {
		category := "lifecycle"
		if status == "provisioning" {
			category = "provisioning"
		}
		manager.logEvent(ctx, db, id, category, "info", "State: "+status)
		manager.emit("server."+status, server)
	}
	return server, err
}

func (manager *ServerManager) transitionFrom(ctx context.Context, db *sql.DB, id, expected, status, reference string) (Server, bool, error) {
	server, err := scanServer(db.QueryRowContext(ctx, `UPDATE servers
SET status = ?, runtime_reference = ?, updated_at = `+projectNow+`
WHERE id = ? AND status = ? RETURNING `+serverColumns, status, reference, id, expected))
	if errors.Is(err, sql.ErrNoRows) {
		server, err = getServer(ctx, db, id)
		return server, false, err
	}
	if err != nil {
		return Server{}, false, err
	}
	manager.logEvent(ctx, db, id, "lifecycle", "info", "State: "+status)
	manager.emit("server."+status, server)
	return server, true, nil
}

func (manager *ServerManager) fail(ctx context.Context, db *sql.DB, server Server, operationErr error) (Server, error) {
	status := "failed"
	if errors.Is(operationErr, context.Canceled) {
		status = "stopped"
	}
	failed, stateErr := manager.transition(context.WithoutCancel(ctx), db, server.ID, status, server.RuntimeReference)
	manager.logEvent(context.WithoutCancel(ctx), db, server.ID, "error", "error", operationErr.Error())
	return failed, errors.Join(operationErr, stateErr)
}

func (manager *ServerManager) logEvent(ctx context.Context, db *sql.DB, serverID, category, level, message string) {
	id, err := newUUID()
	if err != nil {
		return
	}
	if len(message) > 4096 {
		message = message[:4096]
	}
	_, _ = db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO server_runtime_events
(id, server_id, category, level, message, created_at)
VALUES (?, ?, ?, ?, ?, `+projectNow+`)`, id, serverID, category, level, message)
}

func (manager *ServerManager) beginOperation(parent context.Context, id string) (context.Context, func(), error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.operations[id]; exists {
		return nil, nil, errors.New("the server already has an operation in progress")
	}
	ctx, cancel := context.WithCancel(parent)
	operation := &serverOperation{cancel: cancel, done: make(chan struct{})}
	manager.operations[id] = operation
	return ctx, func() {
		cancel()
		manager.mu.Lock()
		if manager.operations[id] == operation {
			delete(manager.operations, id)
			close(operation.done)
		}
		manager.mu.Unlock()
	}, nil
}

func (manager *ServerManager) cancelOperation(ctx context.Context, id string) error {
	manager.mu.Lock()
	operation := manager.operations[id]
	if operation != nil {
		operation.cancel()
	}
	manager.mu.Unlock()
	if operation == nil {
		return nil
	}
	select {
	case <-operation.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
