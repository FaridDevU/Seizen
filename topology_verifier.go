package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const topologyCheckTimeout = 3 * time.Second

type TopologyHealthcheckResult struct {
	SequenceID   string `json:"sequenceId"`
	ServerID     string `json:"serverId"`
	ServiceID    string `json:"serviceId,omitempty"`
	ConnectionID string `json:"connectionId,omitempty"`
	Healthy      bool   `json:"healthy"`
	StatusCode   int    `json:"statusCode,omitempty"`
	DurationMS   int64  `json:"durationMs"`
	Message      string `json:"message"`
	CheckedAt    string `json:"checkedAt"`
}

func (a *App) VerifyServerService(projectID, serverID, serviceID string) (ServerService, error) {
	ctx, finish, err := a.projectServerManager().beginOperation(a.context(), serverID)
	if err != nil {
		return ServerService{}, err
	}
	defer finish()
	db, server, err := a.loadOwnedServer(ctx, projectID, serverID)
	if err != nil {
		return ServerService{}, err
	}
	service, err := loadServerService(ctx, db, serverID, serviceID)
	if err != nil {
		return ServerService{}, err
	}
	provider, err := a.topologyServerProvider(server)
	if err != nil {
		return ServerService{}, err
	}
	if err = requireRunningServer(ctx, server, provider); err != nil {
		return ServerService{}, err
	}

	result := a.beginTopologyCheck(serverID, serviceID, "")
	if service.HealthcheckURL != "" {
		result.StatusCode, result.DurationMS, err = checkServerTopologyHTTP(ctx, provider, server, service.HealthcheckURL)
	} else if service.Protocol == "udp" {
		err = errors.New("the UDP check requires provider inspection")
	} else if service.Port != nil {
		result.DurationMS, err = checkServerTopologyTCP(ctx, provider, server, service.Host, *service.Port)
	} else {
		err = errors.New("the service does not declare a verifiable port or healthcheck")
	}
	result.Healthy = err == nil
	result.Message = topologyCheckMessage(err)
	updated, updateErr := setVerifiedService(ctx, db, service, result.Healthy)
	if updateErr != nil {
		result.Healthy = false
		result.Message = topologyCheckMessage(errors.Join(err, updateErr))
		a.finishTopologyCheck(result)
		return ServerService{}, errors.Join(err, updateErr)
	}
	a.finishTopologyCheck(result)
	a.emitTopologyServiceResult(updated)
	if err != nil {
		return updated, err
	}
	return updated, nil
}

func (a *App) RunServerServiceHealthcheck(projectID, serverID, serviceID string) (TopologyHealthcheckResult, error) {
	ctx, finish, err := a.projectServerManager().beginOperation(a.context(), serverID)
	if err != nil {
		return TopologyHealthcheckResult{}, err
	}
	defer finish()
	db, server, err := a.loadOwnedServer(ctx, projectID, serverID)
	if err != nil {
		return TopologyHealthcheckResult{}, err
	}
	service, err := loadServerService(ctx, db, serverID, serviceID)
	if err != nil {
		return TopologyHealthcheckResult{}, err
	}
	provider, err := a.topologyServerProvider(server)
	if err != nil {
		return TopologyHealthcheckResult{}, err
	}
	if err = requireRunningServer(ctx, server, provider); err != nil {
		return TopologyHealthcheckResult{}, err
	}
	if _, err = topologyEndpointURL(service); err != nil {
		return TopologyHealthcheckResult{}, err
	}

	result := a.beginTopologyCheck(serverID, serviceID, "")
	result.StatusCode, result.DurationMS, err = checkServerTopologyHTTP(ctx, provider, server, service.HealthcheckURL)
	result.Healthy = err == nil
	result.Message = topologyCheckMessage(err)
	updated, updateErr := setVerifiedService(ctx, db, service, result.Healthy)
	if updateErr != nil {
		result.Healthy = false
		result.Message = topologyCheckMessage(errors.Join(err, updateErr))
	}
	a.finishTopologyCheck(result)
	if updateErr == nil {
		a.emitTopologyServiceResult(updated)
	}
	return result, errors.Join(err, updateErr)
}

func (a *App) VerifyServerConnection(projectID, serverID, connectionID string) (ServerConnection, error) {
	ctx, finish, err := a.projectServerManager().beginOperation(a.context(), serverID)
	if err != nil {
		return ServerConnection{}, err
	}
	defer finish()
	db, server, err := a.loadOwnedServer(ctx, projectID, serverID)
	if err != nil {
		return ServerConnection{}, err
	}
	provider, err := a.topologyServerProvider(server)
	if err != nil {
		return ServerConnection{}, err
	}
	if err = requireRunningServer(ctx, server, provider); err != nil {
		return ServerConnection{}, err
	}
	connection, err := loadServerConnection(ctx, db, serverID, connectionID)
	if err != nil {
		return ServerConnection{}, err
	}
	if connection.SourceServiceID == nil || connection.TargetServiceID == nil {
		return ServerConnection{}, errors.New("the connection needs a source and target")
	}
	if _, err = loadServerService(ctx, db, serverID, *connection.SourceServiceID); err != nil {
		return ServerConnection{}, err
	}
	target, err := loadServerService(ctx, db, serverID, *connection.TargetServiceID)
	if err != nil {
		return ServerConnection{}, err
	}

	result := a.beginTopologyCheck(serverID, "", connectionID)
	if (connection.Protocol == "http" || connection.Protocol == "https") && target.HealthcheckURL != "" {
		result.StatusCode, result.DurationMS, err = checkServerTopologyHTTP(ctx, provider, server, target.HealthcheckURL)
	} else if connection.Protocol == "udp" {
		err = errors.New("the UDP check requires provider inspection")
	} else {
		port := target.Port
		if connection.Port != nil {
			port = connection.Port
		}
		if port == nil {
			err = errors.New("the connection does not declare a verifiable port")
		} else {
			result.DurationMS, err = checkServerTopologyTCP(ctx, provider, server, target.Host, *port)
		}
	}
	result.Healthy = err == nil
	result.Message = topologyCheckMessage(err)
	updated, updateErr := setVerifiedConnection(ctx, db, connection, result.Healthy)
	if updateErr != nil {
		result.Healthy = false
		result.Message = topologyCheckMessage(errors.Join(err, updateErr))
		a.finishTopologyCheck(result)
		return ServerConnection{}, errors.Join(err, updateErr)
	}
	a.finishTopologyCheck(result)
	a.emitTopologyConnectionResult(updated)
	if err != nil {
		return updated, err
	}
	return updated, nil
}

func (a *App) beginTopologyCheck(serverID, serviceID, connectionID string) TopologyHealthcheckResult {
	sequence, _ := newUUID()
	if sequence == "" {
		sequence = fmt.Sprintf("check-%d", time.Now().UnixNano())
	}
	result := TopologyHealthcheckResult{
		SequenceID: sequence, ServerID: serverID, ServiceID: serviceID,
		ConnectionID: connectionID, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Message: "checking",
	}
	a.emitAgentEvent("server.topology.healthcheck.pulse", result)
	return result
}

func (a *App) finishTopologyCheck(result TopologyHealthcheckResult) {
	if db, err := a.database.Pool(context.Background()); err == nil {
		level := "error"
		if result.Healthy {
			level = "info"
		}
		a.projectServerManager().logEvent(context.Background(), db, result.ServerID, "health", level, result.Message)
	}
	a.emitAgentEvent("server.topology.healthcheck.result", result)
}

func (a *App) emitTopologyServiceResult(service ServerService) {
	a.emitAgentEvent("server.topology.service."+service.Status, service)
}

func (a *App) emitTopologyConnectionResult(connection ServerConnection) {
	a.emitAgentEvent("server.topology.connection."+connection.Status, connection)
}

func checkTopologyHTTP(parent context.Context, rawURL string) (int, int64, error) {
	if err := validateLocalAppURL(rawURL); err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithTimeout(parent, topologyCheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, 0, err
	}
	client := &http.Client{
		Timeout: topologyCheckTimeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			return validateLocalAppURL(request.URL.String())
		},
	}
	started := time.Now()
	response, err := client.Do(request)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		return 0, duration, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return response.StatusCode, duration, fmt.Errorf("healthcheck HTTP %d", response.StatusCode)
	}
	return response.StatusCode, duration, nil
}

func checkTopologyTCP(parent context.Context, host string, port int) (int64, error) {
	if !validTopologyHost(host) && strings.TrimSpace(host) != "" {
		return 0, errors.New("the host is not valid")
	}
	ctx, cancel := context.WithTimeout(parent, topologyCheckTimeout)
	defer cancel()
	endpoint := net.JoinHostPort(topologyDialHost(host), strconv.Itoa(port))
	started := time.Now()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint)
	duration := time.Since(started).Milliseconds()
	if err == nil {
		err = connection.Close()
	}
	return duration, err
}

func (a *App) topologyServerProvider(server Server) (ServerProvider, error) {
	provider := a.projectServerManager().providers[server.Provider]
	if provider == nil {
		return nil, errors.New("the server provider is not valid")
	}
	return provider, nil
}

func checkServerTopologyHTTP(ctx context.Context, provider ServerProvider, server Server, rawURL string) (int, int64, error) {
	if server.Provider != "wsl" {
		return checkTopologyHTTP(ctx, rawURL)
	}
	if err := validateLocalAppURL(rawURL); err != nil {
		return 0, 0, err
	}
	parsed, _ := url.Parse(rawURL)
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		port = "80"
		if parsed.Scheme == "https" {
			port = "443"
		}
	}
	started := time.Now()
	var script string
	if parsed.Scheme == "http" {
		path := parsed.RequestURI()
		if path == "" {
			path = "/"
		}
		request := fmt.Sprintf("GET %s HTTP/1.0\r\nHost: %s\r\nConnection: close\r\n\r\n", path, parsed.Host)
		script = "HOST=" + posixShellQuote(host) + " PORT=" + posixShellQuote(port) +
			"; exec 3<>\"/dev/tcp/$HOST/$PORT\"; printf %s " + posixShellQuote(request) +
			" >&3; IFS=' ' read -r _ code _ <&3; case \"$code\" in 2*|3*) printf %s \"$code\";; *) exit 22;; esac"
	} else {
		script = "command -v curl >/dev/null || { echo 'curl is not installed' >&2; exit 127; }; " +
			"curl --fail --silent --show-error --location --max-time 3 --proto '=http,https' --proto-redir '=http,https' " +
			"--output /dev/null --write-out '%{http_code}' " + posixShellQuote(rawURL)
	}
	checkContext, cancel := context.WithTimeout(ctx, topologyCheckTimeout)
	defer cancel()
	result, err := provider.Exec(checkContext, server, "bash -lc "+posixShellQuote(script))
	duration := time.Since(started).Milliseconds()
	if err != nil || result.ExitCode != 0 {
		return 0, duration, errors.Join(err, fmt.Errorf("internal healthcheck exited with code %d", result.ExitCode))
	}
	statusCode, err := strconv.Atoi(strings.TrimSpace(result.Output))
	if err != nil || statusCode < 200 || statusCode >= 400 {
		return statusCode, duration, fmt.Errorf("healthcheck HTTP %d", statusCode)
	}
	return statusCode, duration, nil
}

func checkServerTopologyTCP(ctx context.Context, provider ServerProvider, server Server, host string, port int) (int64, error) {
	if server.Provider != "wsl" {
		return checkTopologyTCP(ctx, host, port)
	}
	if !validTopologyHost(host) && strings.TrimSpace(host) != "" {
		return 0, errors.New("the host is not valid")
	}
	script := "HOST=" + posixShellQuote(topologyDialHost(host)) + " PORT=" + strconv.Itoa(port) +
		"; exec 3<>\"/dev/tcp/$HOST/$PORT\""
	started := time.Now()
	checkContext, cancel := context.WithTimeout(ctx, topologyCheckTimeout)
	defer cancel()
	result, err := provider.Exec(checkContext, server, "bash -lc "+posixShellQuote(script))
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("TCP check exited with code %d", result.ExitCode)
	}
	return time.Since(started).Milliseconds(), err
}

func posixShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func requireRunningServer(ctx context.Context, server Server, provider ServerProvider) error {
	if server.Status != "running" && server.Status != "degraded" {
		return errors.New("the server must be running to verify its topology")
	}
	health, err := provider.CheckHealth(ctx, server)
	if err != nil {
		return fmt.Errorf("could not check the server process: %w", err)
	}
	if !health.Healthy {
		return errors.New("the server process is not active")
	}
	return nil
}

func topologyCheckMessage(err error) string {
	if err == nil {
		return "verified"
	}
	message := err.Error()
	if len(message) > 512 {
		return message[:512]
	}
	return message
}

func setVerifiedService(ctx context.Context, db *sql.DB, service ServerService, healthy bool) (ServerService, error) {
	status, source := "failed", service.Source
	if healthy {
		status, source = "healthy", "verified"
	}
	item, err := scanServerService(db.QueryRowContext(ctx, `UPDATE server_services
SET status = ?, source = ? WHERE id = ? AND server_id = ? RETURNING `+serverServiceColumns,
		status, source, service.ID, service.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return ServerService{}, errors.New("the service no longer exists")
	}
	return item, err
}

func setVerifiedConnection(ctx context.Context, db *sql.DB, connection ServerConnection, healthy bool) (ServerConnection, error) {
	status, source := "failed", connection.Source
	if healthy {
		status, source = "healthy", "verified"
	}
	item, err := scanServerConnection(db.QueryRowContext(ctx, `UPDATE server_connections
SET status = ?, source = ? WHERE id = ? AND server_id = ? RETURNING `+serverConnectionColumns,
		status, source, connection.ID, connection.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return ServerConnection{}, errors.New("the connection no longer exists")
	}
	return item, err
}

func loadServerService(ctx context.Context, db *sql.DB, serverID, serviceID string) (ServerService, error) {
	item, err := scanServerService(db.QueryRowContext(ctx, `SELECT `+serverServiceColumns+`
FROM server_services WHERE id = ? AND server_id = ?`, serviceID, serverID))
	if errors.Is(err, sql.ErrNoRows) {
		return ServerService{}, errors.New("the service does not belong to the server")
	}
	return item, err
}

func loadServerConnection(ctx context.Context, db *sql.DB, serverID, connectionID string) (ServerConnection, error) {
	item, err := scanServerConnection(db.QueryRowContext(ctx, `SELECT `+serverConnectionColumns+`
FROM server_connections WHERE id = ? AND server_id = ?`, connectionID, serverID))
	if errors.Is(err, sql.ErrNoRows) {
		return ServerConnection{}, errors.New("the connection does not belong to the server")
	}
	return item, err
}
