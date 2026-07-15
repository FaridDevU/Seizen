package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

const agentTokenLifetime = 8 * time.Hour

var appAgentPermissions = []string{
	"seizen_project_context",
	"seizen_app_list",
	"seizen_app_create",
	"seizen_app_configure",
	"seizen_app_run",
	"seizen_app_stop",
	"seizen_app_restart",
	"seizen_app_status",
	"seizen_app_set_preview",
	"seizen_app_run_tests",
	"seizen_app_get_logs",
	"seizen_app_capture_preview",
	"seizen_app_get_console_errors",
	"seizen_app_smoke_test",
	"seizen_app_test_route",
	"seizen_app_discover",
	"seizen_app_mount",
	"seizen_app_wait_ready",
	"seizen_app_attach_running",
	"seizen_app_get_runtime_diagnostics",
	"seizen_server_list",
	"seizen_server_create_draft",
	"seizen_server_start",
	"seizen_server_stop",
	"seizen_server_restart",
	"seizen_server_status",
	"seizen_server_exec",
	"seizen_server_register_service",
	"seizen_server_update_service",
	"seizen_server_register_connection",
	"seizen_server_update_connection",
	"seizen_server_healthcheck",
	"seizen_server_restart_test",
	"seizen_server_get_logs",
	"seizen_server_get_stats",
	"seizen_server_list_topology",
	"seizen_server_publish_report",
	"seizen_server_export_reproducible_config",
	"seizen_experiment_analyze_change",
	"seizen_experiment_suggest",
	"seizen_experiment_create",
	"seizen_experiment_list",
	"seizen_experiment_select",
	"seizen_experiment_status",
	"seizen_experiment_checkpoint",
	"seizen_experiment_compare",
	"seizen_experiment_prepare_integration",
	"seizen_experiment_request_integration",
	"seizen_experiment_integrate",
	"seizen_experiment_discard",
	"seizen_experiment_archive",
	"seizen_experiment_restore",
}

type AgentTokenScope struct {
	SessionID    string    `json:"sessionId"`
	ProjectID    string    `json:"projectId"`
	ExperimentID string    `json:"experimentId,omitempty"`
	SpaceID      string    `json:"spaceId"`
	AppID        string    `json:"appId,omitempty"`
	Permissions  []string  `json:"permissions"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type agentTokenRecord struct {
	AgentTokenScope
	permissions map[string]struct{}
}

type agentTokenStore struct {
	mu     sync.Mutex
	tokens map[[sha256.Size]byte]*agentTokenRecord
	random io.Reader
	now    func() time.Time
}

func newAgentTokenStore() *agentTokenStore {
	return &agentTokenStore{
		tokens: make(map[[sha256.Size]byte]*agentTokenRecord),
		random: rand.Reader,
		now:    time.Now,
	}
}

func (store *agentTokenStore) Issue(scope AgentTokenScope, lifetime time.Duration) (string, error) {
	if strings.TrimSpace(scope.SessionID) == "" || strings.TrimSpace(scope.ProjectID) == "" {
		return "", errors.New("the session and project are required")
	}
	if len(scope.Permissions) == 0 {
		return "", errors.New("the token needs at least one permission")
	}
	spaceID, err := normalizeProjectSpaceID(scope.SpaceID)
	if err != nil {
		return "", err
	}
	scope.SpaceID = spaceID
	if lifetime <= 0 || lifetime > agentTokenLifetime {
		lifetime = agentTokenLifetime
	}
	secretBytes := make([]byte, 32)
	if _, err := io.ReadFull(store.random, secretBytes); err != nil {
		return "", errors.New("could not create the agent's temporary token")
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	hash := sha256.Sum256([]byte(secret))
	permissions := make(map[string]struct{}, len(scope.Permissions))
	clean := make([]string, 0, len(scope.Permissions))
	for _, permission := range scope.Permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" {
			continue
		}
		if _, exists := permissions[permission]; !exists {
			permissions[permission] = struct{}{}
			clean = append(clean, permission)
		}
	}
	if len(clean) == 0 {
		return "", errors.New("the token needs at least one permission")
	}
	scope.Permissions = clean
	scope.ExpiresAt = store.now().UTC().Add(lifetime)
	store.mu.Lock()
	store.tokens[hash] = &agentTokenRecord{AgentTokenScope: scope, permissions: permissions}
	store.mu.Unlock()
	return secret, nil
}

func (store *agentTokenStore) Authorize(secret, permission string) (AgentTokenScope, error) {
	if secret == "" || permission == "" {
		return AgentTokenScope{}, errors.New("invalid agent token")
	}
	hash := sha256.Sum256([]byte(secret))
	store.mu.Lock()
	defer store.mu.Unlock()
	record := store.tokens[hash]
	if record == nil {
		return AgentTokenScope{}, errors.New("invalid agent token")
	}
	if !store.now().Before(record.ExpiresAt) {
		delete(store.tokens, hash)
		return AgentTokenScope{}, errors.New("the agent token expired")
	}
	if _, granted := record.permissions[permission]; !granted {
		return AgentTokenScope{}, errors.New("the token does not allow this tool")
	}
	return cloneAgentTokenScope(record.AgentTokenScope), nil
}

func (store *agentTokenStore) BindApp(secret, appID string) error {
	if strings.TrimSpace(appID) == "" {
		return errors.New("the App is required")
	}
	hash := sha256.Sum256([]byte(secret))
	store.mu.Lock()
	defer store.mu.Unlock()
	record := store.tokens[hash]
	if record == nil || !store.now().Before(record.ExpiresAt) {
		delete(store.tokens, hash)
		return errors.New("invalid agent token")
	}
	if record.AppID != "" && record.AppID != appID {
		return errors.New("the token is already limited to another App")
	}
	record.AppID = appID
	return nil
}

func (store *agentTokenStore) Revoke(secret string) {
	hash := sha256.Sum256([]byte(secret))
	store.mu.Lock()
	delete(store.tokens, hash)
	store.mu.Unlock()
}

func (store *agentTokenStore) RevokeSession(sessionID string) {
	store.mu.Lock()
	for hash, record := range store.tokens {
		if record.SessionID == sessionID {
			delete(store.tokens, hash)
		}
	}
	store.mu.Unlock()
}

func (store *agentTokenStore) RevokeProject(projectID string) {
	store.mu.Lock()
	for hash, record := range store.tokens {
		if record.ProjectID == projectID {
			delete(store.tokens, hash)
		}
	}
	store.mu.Unlock()
}

func (store *agentTokenStore) RevokeExperiment(projectID, experimentID string) {
	store.mu.Lock()
	for hash, record := range store.tokens {
		if record.ProjectID == projectID && record.ExperimentID == experimentID {
			delete(store.tokens, hash)
		}
	}
	store.mu.Unlock()
}

func (store *agentTokenStore) RevokeAll() {
	store.mu.Lock()
	clear(store.tokens)
	store.mu.Unlock()
}

func cloneAgentTokenScope(scope AgentTokenScope) AgentTokenScope {
	scope.Permissions = append([]string(nil), scope.Permissions...)
	return scope
}
