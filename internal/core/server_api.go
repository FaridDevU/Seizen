package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type GlobalServer struct {
	Server
	ProjectName string       `json:"projectName"`
	AppName     string       `json:"appName"`
	Stats       *ServerStats `json:"stats,omitempty"`
}

func (a *App) StartServer(id string) (Server, error) {
	return a.projectServerManager().StartServer(a.context(), id)
}

func (a *App) StopServer(id string) (Server, error) {
	if terminals := a.currentTerminalManager(); terminals != nil {
		terminals.stopServer(id)
	}
	return a.projectServerManager().StopServer(a.context(), id)
}

func (a *App) RestartServer(id string) (Server, error) {
	if _, err := a.StopServer(id); err != nil {
		return Server{}, err
	}
	return a.StartServer(id)
}

func (a *App) DestroyServer(id string) error {
	if terminals := a.currentTerminalManager(); terminals != nil {
		terminals.stopServer(id)
	}
	return a.projectServerManager().DestroyServer(a.context(), id)
}

func (a *App) GetServerStats(id string) (ServerStats, error) {
	return a.projectServerManager().Stats(a.context(), id)
}

func (a *App) CheckServerHealth(id string) (ServerHealth, error) {
	return a.projectServerManager().CheckHealth(a.context(), id)
}

func (a *App) CleanupProjectServers(projectID string) error {
	a.ensureAgentTokenStore().RevokeProject(projectID)
	if terminals := a.currentTerminalManager(); terminals != nil {
		terminals.stopProjectServers(projectID)
	}
	return a.projectServerManager().CleanupProjectServers(a.context(), projectID)
}

func (a *App) PrepareServerClose() error {
	a.mu.RLock()
	manager := a.servers
	a.mu.RUnlock()
	if manager == nil {
		return nil
	}
	return manager.PrepareServerClose(a.context())
}

func (a *App) ListAllServers() ([]GlobalServer, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT servers.id, servers.project_id, servers.app_id,
servers.name, servers.provider, servers.distro, servers.runtime_reference, servers.status,
servers.cpu_limit, servers.memory_mb, servers.disk_gb, servers.keep_alive,
servers.created_at, servers.updated_at, projects.name, apps.name
FROM servers
JOIN projects ON projects.id = servers.project_id
JOIN apps ON apps.id = servers.app_id AND apps.project_id = servers.project_id
ORDER BY servers.updated_at DESC, LOWER(servers.name)`)
	if err != nil {
		return nil, fmt.Errorf("could not load the servers: %w", err)
	}
	defer rows.Close()
	servers := make([]GlobalServer, 0)
	for rows.Next() {
		var item GlobalServer
		if err = rows.Scan(
			&item.ID, &item.ProjectID, &item.AppID, &item.Name, &item.Provider,
			&item.Distro, &item.RuntimeReference, &item.Status, &item.CPULimit,
			&item.MemoryMB, &item.DiskGB, &item.KeepAlive, &item.CreatedAt,
			&item.UpdatedAt, &item.ProjectName, &item.AppName,
		); err != nil {
			return nil, err
		}
		servers = append(servers, item)
	}
	return servers, rows.Err()
}

func (a *App) GetServerLogs(id string) (string, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return "", err
	}
	server, err := getServer(ctx, db, id)
	if err != nil {
		return "", err
	}
	rows, err := db.QueryContext(ctx, `SELECT created_at, category, level, message FROM (
    SELECT created_at, category, level, message
    FROM server_runtime_events WHERE server_id = ?
    UNION ALL
    SELECT created_at, 'agent', CASE WHEN success THEN 'info' ELSE 'error' END,
           tool_name || CASE WHEN error_message = '' THEN '' ELSE ': ' || error_message END
    FROM agent_audit_events
    WHERE project_id = ? AND tool_name LIKE 'seizen_server_%'
      AND (json_extract(arguments_json, '$.serverId') = ?
           OR json_extract(arguments_json, '$.id') = ?)
) ORDER BY created_at DESC LIMIT 500`, id, server.ProjectID, id, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var createdAt, category, level, message string
		if err = rows.Scan(&createdAt, &category, &level, &message); err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s [%s/%s] %s", createdAt, category, level, message))
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return strings.Join(lines, "\n"), nil
}

func serverProjectPath(ctx context.Context, db *sql.DB, serverID string) (Server, string, error) {
	server, err := getServer(ctx, db, serverID)
	if err != nil {
		return Server{}, "", err
	}
	var projectPath string
	err = db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, server.ProjectID).Scan(&projectPath)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, "", errors.New("the server's project was not found")
	}
	return server, projectPath, err
}
