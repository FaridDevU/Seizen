package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
)

var topologyServiceKinds = map[string]bool{
	"internet": true, "proxy": true, "frontend": true, "backend": true,
	"database": true, "cache": true, "queue": true, "worker": true,
	"storage": true, "external": true,
}

var topologyProtocols = map[string]bool{
	"http": true, "https": true, "ws": true, "wss": true, "tcp": true,
	"udp": true, "grpc": true, "internal": true, "postgres": true,
	"mysql": true, "redis": true, "amqp": true, "mqtt": true,
	"storage": true,
}

type ServerServiceInput struct {
	ID             string `json:"id"`
	ProjectID      string `json:"projectId"`
	ServerID       string `json:"serverId"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Host           string `json:"host"`
	Port           *int   `json:"port"`
	Protocol       string `json:"protocol"`
	HealthcheckURL string `json:"healthcheckUrl"`
	MetadataJSON   string `json:"metadataJson"`
	PositionJSON   string `json:"positionJson"`
}

type ServerConnectionInput struct {
	ID              string `json:"id"`
	ProjectID       string `json:"projectId"`
	ServerID        string `json:"serverId"`
	SourceServiceID string `json:"sourceServiceId"`
	TargetServiceID string `json:"targetServiceId"`
	Protocol        string `json:"protocol"`
	Port            *int   `json:"port"`
	MetadataJSON    string `json:"metadataJson"`
}

const serverServiceColumns = `id, server_id, name, kind, host, port, protocol,
healthcheck_url, status, source, metadata_json, position_json`

const serverConnectionColumns = `id, server_id, source_service_id, target_service_id,
protocol, port, status, source, traffic_rate, error_rate, metadata_json`

func (a *App) ListServerServices(projectID, serverID string) ([]ServerService, error) {
	ctx := a.context()
	db, _, err := a.loadOwnedServer(ctx, projectID, serverID)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+serverServiceColumns+`
FROM server_services WHERE server_id = ? ORDER BY LOWER(name), id`, serverID)
	if err != nil {
		return nil, fmt.Errorf("could not load the services: %w", err)
	}
	defer rows.Close()
	items := make([]ServerService, 0)
	for rows.Next() {
		item, scanErr := scanServerService(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) ListServerConnections(projectID, serverID string) ([]ServerConnection, error) {
	ctx := a.context()
	db, _, err := a.loadOwnedServer(ctx, projectID, serverID)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+serverConnectionColumns+`
FROM server_connections WHERE server_id = ? ORDER BY id`, serverID)
	if err != nil {
		return nil, fmt.Errorf("could not load the connections: %w", err)
	}
	defer rows.Close()
	items := make([]ServerConnection, 0)
	for rows.Next() {
		item, scanErr := scanServerConnection(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// RegisterServerService stores an agent declaration. Runtime truth can only be
// added later by a verifier or a provider inspection.
func (a *App) RegisterServerService(input ServerServiceInput) (ServerService, error) {
	ctx := a.context()
	db, _, err := a.loadOwnedServer(ctx, input.ProjectID, input.ServerID)
	if err != nil {
		return ServerService{}, err
	}
	if err = normalizeServerServiceInput(&input); err != nil {
		return ServerService{}, err
	}
	id, err := newUUID()
	if err != nil {
		return ServerService{}, err
	}
	item, err := scanServerService(db.QueryRowContext(ctx, `INSERT INTO server_services (
id, server_id, name, kind, host, port, protocol, healthcheck_url, status, source,
metadata_json, position_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'unknown', 'declared', ?, ?)
RETURNING `+serverServiceColumns, id, input.ServerID, input.Name, input.Kind, input.Host,
		input.Port, input.Protocol, input.HealthcheckURL, input.MetadataJSON, input.PositionJSON))
	if err != nil {
		return ServerService{}, fmt.Errorf("could not register the service: %w", err)
	}
	a.emitAgentEvent("server.topology.service.registered", item)
	return item, nil
}

func (a *App) UpdateServerService(input ServerServiceInput) (ServerService, error) {
	if strings.TrimSpace(input.ID) == "" {
		return ServerService{}, errors.New("the service is required")
	}
	ctx := a.context()
	db, _, err := a.loadOwnedServer(ctx, input.ProjectID, input.ServerID)
	if err != nil {
		return ServerService{}, err
	}
	if err = normalizeServerServiceInput(&input); err != nil {
		return ServerService{}, err
	}
	item, err := scanServerService(db.QueryRowContext(ctx, `UPDATE server_services SET
name = ?, kind = ?, host = ?, port = ?, protocol = ?, healthcheck_url = ?,
metadata_json = ?, position_json = ?, status = 'unknown', source = 'declared'
WHERE id = ? AND server_id = ? RETURNING `+serverServiceColumns, input.Name, input.Kind,
		input.Host, input.Port, input.Protocol, input.HealthcheckURL, input.MetadataJSON,
		input.PositionJSON, input.ID, input.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return ServerService{}, errors.New("the service does not belong to the server")
	}
	if err != nil {
		return ServerService{}, fmt.Errorf("could not update the service: %w", err)
	}
	a.emitAgentEvent("server.topology.service.updated", item)
	return item, nil
}

func (a *App) RegisterServerConnection(input ServerConnectionInput) (ServerConnection, error) {
	ctx := a.context()
	db, _, err := a.loadOwnedServer(ctx, input.ProjectID, input.ServerID)
	if err != nil {
		return ServerConnection{}, err
	}
	if err = normalizeServerConnectionInput(ctx, db, &input); err != nil {
		return ServerConnection{}, err
	}
	id, err := newUUID()
	if err != nil {
		return ServerConnection{}, err
	}
	item, err := scanServerConnection(db.QueryRowContext(ctx, `INSERT INTO server_connections (
id, server_id, source_service_id, target_service_id, protocol, port, status, source,
traffic_rate, error_rate, metadata_json) VALUES (?, ?, ?, ?, ?, ?, 'unknown', 'declared', 0, 0, ?)
RETURNING `+serverConnectionColumns, id, input.ServerID, input.SourceServiceID,
		input.TargetServiceID, input.Protocol, input.Port, input.MetadataJSON))
	if err != nil {
		return ServerConnection{}, fmt.Errorf("could not register the connection: %w", err)
	}
	a.emitAgentEvent("server.topology.connection.registered", item)
	return item, nil
}

func (a *App) UpdateServerConnection(input ServerConnectionInput) (ServerConnection, error) {
	if strings.TrimSpace(input.ID) == "" {
		return ServerConnection{}, errors.New("the connection is required")
	}
	ctx := a.context()
	db, _, err := a.loadOwnedServer(ctx, input.ProjectID, input.ServerID)
	if err != nil {
		return ServerConnection{}, err
	}
	if err = normalizeServerConnectionInput(ctx, db, &input); err != nil {
		return ServerConnection{}, err
	}
	item, err := scanServerConnection(db.QueryRowContext(ctx, `UPDATE server_connections SET
source_service_id = ?, target_service_id = ?, protocol = ?, port = ?, metadata_json = ?,
status = 'unknown', source = 'declared', traffic_rate = 0, error_rate = 0
WHERE id = ? AND server_id = ? RETURNING `+serverConnectionColumns,
		input.SourceServiceID, input.TargetServiceID, input.Protocol, input.Port,
		input.MetadataJSON, input.ID, input.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return ServerConnection{}, errors.New("the connection does not belong to the server")
	}
	if err != nil {
		return ServerConnection{}, fmt.Errorf("could not update the connection: %w", err)
	}
	a.emitAgentEvent("server.topology.connection.updated", item)
	return item, nil
}

func (a *App) UpdateServicePosition(projectID, serverID, serviceID, positionJSON string) (ServerService, error) {
	ctx := a.context()
	db, _, err := a.loadOwnedServer(ctx, projectID, serverID)
	if err != nil {
		return ServerService{}, err
	}
	positionJSON, err = normalizePositionJSON(positionJSON)
	if err != nil {
		return ServerService{}, err
	}
	item, err := scanServerService(db.QueryRowContext(ctx, `UPDATE server_services
SET position_json = ? WHERE id = ? AND server_id = ? RETURNING `+serverServiceColumns,
		positionJSON, serviceID, serverID))
	if errors.Is(err, sql.ErrNoRows) {
		return ServerService{}, errors.New("the service does not belong to the server")
	}
	if err != nil {
		return ServerService{}, err
	}
	a.emitAgentEvent("server.topology.service.position", item)
	return item, nil
}

func (a *App) loadOwnedServer(ctx context.Context, projectID, serverID string) (*sql.DB, Server, error) {
	projectID, serverID = strings.TrimSpace(projectID), strings.TrimSpace(serverID)
	if projectID == "" || serverID == "" {
		return nil, Server{}, errors.New("the project and server are required")
	}
	db, err := a.database.Pool(ctx)
	if err != nil {
		return nil, Server{}, err
	}
	server, err := scanServer(db.QueryRowContext(ctx, `SELECT `+serverColumns+`
FROM servers WHERE id = ? AND project_id = ?`, serverID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Server{}, errors.New("the server does not belong to the project")
	}
	return db, server, err
}

func normalizeServerServiceInput(input *ServerServiceInput) error {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.ServerID = strings.TrimSpace(input.ServerID)
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	input.Host = strings.TrimSpace(input.Host)
	input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
	input.HealthcheckURL = strings.TrimSpace(input.HealthcheckURL)
	if input.Name == "" {
		return errors.New("the service name is required")
	}
	if !topologyServiceKinds[input.Kind] {
		return errors.New("the node type is not valid")
	}
	if input.Protocol != "" && !topologyProtocols[input.Protocol] {
		return errors.New("the service protocol is not valid")
	}
	if err := validateTopologyPort(input.Port); err != nil {
		return err
	}
	if input.Host != "" && !validTopologyHost(input.Host) {
		return errors.New("the service host is not valid")
	}
	if err := validateLocalAppURL(input.HealthcheckURL); err != nil {
		return fmt.Errorf("invalid healthcheck: %w", err)
	}
	var err error
	if input.MetadataJSON, err = normalizeJSONObject(input.MetadataJSON); err != nil {
		return fmt.Errorf("invalid metadata: %w", err)
	}
	if input.PositionJSON, err = normalizePositionJSON(input.PositionJSON); err != nil {
		return err
	}
	return nil
}

func normalizeServerConnectionInput(ctx context.Context, db *sql.DB, input *ServerConnectionInput) error {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.ServerID = strings.TrimSpace(input.ServerID)
	input.SourceServiceID = strings.TrimSpace(input.SourceServiceID)
	input.TargetServiceID = strings.TrimSpace(input.TargetServiceID)
	input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
	if input.SourceServiceID == "" || input.TargetServiceID == "" || input.SourceServiceID == input.TargetServiceID {
		return errors.New("the connection needs different source and target services")
	}
	if !topologyProtocols[input.Protocol] {
		return errors.New("the connection protocol is not valid")
	}
	if err := validateTopologyPort(input.Port); err != nil {
		return err
	}
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_services
WHERE server_id = ? AND id IN (?, ?)`, input.ServerID, input.SourceServiceID, input.TargetServiceID).Scan(&count)
	if err != nil {
		return err
	}
	if count != 2 {
		return errors.New("the connection's services must belong to the same server")
	}
	input.MetadataJSON, err = normalizeJSONObject(input.MetadataJSON)
	return err
}

func validateTopologyPort(port *int) error {
	if port != nil && (*port < 1 || *port > 65535) {
		return errors.New("the port must be between 1 and 65535")
	}
	return nil
}

func validTopologyHost(host string) bool {
	if strings.ContainsAny(host, `/\\@?#`) {
		return false
	}
	trimmed := strings.Trim(host, "[]")
	if net.ParseIP(trimmed) != nil || strings.EqualFold(trimmed, "localhost") {
		return true
	}
	if len(trimmed) == 0 || len(trimmed) > 253 {
		return false
	}
	for _, label := range strings.Split(trimmed, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
				!(character >= '0' && character <= '9') && character != '-' && character != '_' {
				return false
			}
		}
	}
	return true
}

func normalizeJSONObject(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "{}", nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
		return "", errors.New("se esperaba un objeto JSON")
	}
	normalized, err := json.Marshal(value)
	return string(normalized), err
}

func normalizePositionJSON(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return "{}", nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields["x"] == nil || fields["y"] == nil {
		return "", errors.New("the position must contain finite x/y coordinates")
	}
	var position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	if err := json.Unmarshal([]byte(raw), &position); err != nil || math.IsNaN(position.X) ||
		math.IsNaN(position.Y) || math.IsInf(position.X, 0) || math.IsInf(position.Y, 0) {
		return "", errors.New("the position must contain finite x/y coordinates")
	}
	return fmt.Sprintf(`{"x":%g,"y":%g}`, position.X, position.Y), nil
}

func topologyDialHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return "127.0.0.1"
	}
	return strings.Trim(host, "[]")
}

func topologyEndpointURL(service ServerService) (*url.URL, error) {
	if service.HealthcheckURL == "" {
		return nil, errors.New("the service has no HTTP healthcheck")
	}
	if err := validateLocalAppURL(service.HealthcheckURL); err != nil {
		return nil, err
	}
	return url.Parse(service.HealthcheckURL)
}

func scanServerService(row rowScanner) (ServerService, error) {
	var item ServerService
	err := row.Scan(&item.ID, &item.ServerID, &item.Name, &item.Kind, &item.Host,
		&item.Port, &item.Protocol, &item.HealthcheckURL, &item.Status, &item.Source,
		&item.MetadataJSON, &item.PositionJSON)
	return item, err
}

func scanServerConnection(row rowScanner) (ServerConnection, error) {
	var item ServerConnection
	err := row.Scan(&item.ID, &item.ServerID, &item.SourceServiceID, &item.TargetServiceID,
		&item.Protocol, &item.Port, &item.Status, &item.Source, &item.TrafficRate,
		&item.ErrorRate, &item.MetadataJSON)
	return item, err
}
