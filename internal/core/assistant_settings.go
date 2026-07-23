package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	assistantConfigSetting = "assistant_config"
	// Pre-multi-key setting; read once to seed the config so a saved key survives.
	assistantLegacyKeySetting = "assistant_api_key"
	assistantDefaultModel     = "claude-opus-4-8"
)

type assistantStoredKey struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Key   string `json:"key"`
}

// Provider values: "api" (Anthropic API key), "claude-cli" (Claude Code
// subscription via the real CLI), "codex-cli" (ChatGPT subscription via Codex).
type assistantStoredConfig struct {
	Provider    string               `json:"provider"`
	Keys        []assistantStoredKey `json:"keys"`
	ActiveKeyID string               `json:"activeKeyId"`
	// Model per provider: the API model ids and the CLI aliases are different worlds.
	Models map[string]string `json:"models"`
	// Model is the pre-provider-era field, migrated into Models["api"] on read.
	Model string `json:"model,omitempty"`
	// ClaudeOAuthToken is the long-lived subscription token from the in-app
	// sign-in (claude setup-token); "" means sign in via the shared profile.
	ClaudeOAuthToken string `json:"claudeOauthToken,omitempty"`
}

func (config assistantStoredConfig) provider() string {
	if config.Provider == "" {
		return "api"
	}
	return config.Provider
}

func (config assistantStoredConfig) modelFor(provider string) string {
	if model := config.Models[provider]; model != "" {
		return model
	}
	if provider == "api" {
		if config.Model != "" {
			return config.Model
		}
		return assistantDefaultModel
	}
	return "" // CLI providers default to the account's own default model
}

// AssistantKeyInfo is what the UI sees: the key itself never leaves Go.
type AssistantKeyInfo struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Masked string `json:"masked"`
	Active bool   `json:"active"`
}

type AssistantCLIStatus struct {
	Installed bool   `json:"installed"`
	Note      string `json:"note,omitempty"`
}

type AssistantSettingsView struct {
	Provider  string             `json:"provider"`
	Keys      []AssistantKeyInfo `json:"keys"`
	Model     string             `json:"model"`
	ClaudeCLI AssistantCLIStatus `json:"claudeCli"`
	CodexCLI  AssistantCLIStatus `json:"codexCli"`
}

type AssistantModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func cliStatusNote(signedIn bool) string {
	if signedIn {
		return "connected"
	}
	return "not connected yet"
}

func maskAssistantKey(key string) string {
	if len(key) <= 8 {
		return "••••"
	}
	return key[:7] + "…" + key[len(key)-4:]
}

func (d *Database) assistantConfig(ctx context.Context) (assistantStoredConfig, error) {
	db, err := d.Pool(ctx)
	if err != nil {
		return assistantStoredConfig{}, err
	}
	var raw string
	err = db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, assistantConfigSetting).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return assistantStoredConfig{}, err
	}
	var config assistantStoredConfig
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			return assistantStoredConfig{}, fmt.Errorf("corrupt assistant config: %w", err)
		}
	}
	if len(config.Keys) == 0 {
		// Seed from the single-key era so nobody pastes their key twice.
		var legacy string
		if err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, assistantLegacyKeySetting).Scan(&legacy); err == nil && legacy != "" {
			config.Keys = []assistantStoredKey{{ID: "legacy", Label: "Default", Key: legacy}}
			config.ActiveKeyID = "legacy"
		}
	}
	return config, nil
}

func (d *Database) saveAssistantConfig(ctx context.Context, config assistantStoredConfig) error {
	db, err := d.Pool(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, assistantConfigSetting, string(raw))
	if err != nil {
		return fmt.Errorf("could not save the assistant settings: %w", err)
	}
	return nil
}

func assistantView(config assistantStoredConfig) AssistantSettingsView {
	provider := config.provider()
	view := AssistantSettingsView{
		Provider:  provider,
		Keys:      []AssistantKeyInfo{},
		Model:     config.modelFor(provider),
		ClaudeCLI: assistantCLIStatus("claude"),
		CodexCLI:  assistantCLIStatus("codex"),
	}
	if config.ClaudeOAuthToken != "" {
		view.ClaudeCLI = AssistantCLIStatus{Installed: true, Note: cliStatusNote(true)}
	}
	for _, key := range config.Keys {
		view.Keys = append(view.Keys, AssistantKeyInfo{
			ID:     key.ID,
			Label:  key.Label,
			Masked: maskAssistantKey(key.Key),
			Active: key.ID == config.ActiveKeyID,
		})
	}
	return view
}

// activeAssistantKey returns the key AskAssistant should use, or "" when unconfigured.
func (config assistantStoredConfig) activeKey() string {
	for _, key := range config.Keys {
		if key.ID == config.ActiveKeyID {
			return key.Key
		}
	}
	return ""
}

func (a *App) GetAssistantSettings() (AssistantSettingsView, error) {
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return AssistantSettingsView{}, err
	}
	return assistantView(config), nil
}

func (a *App) AddAssistantKey(label, key string) (AssistantSettingsView, error) {
	label = strings.TrimSpace(label)
	key = strings.TrimSpace(key)
	if key == "" {
		return AssistantSettingsView{}, errors.New("the API key is empty")
	}
	if label == "" {
		label = fmt.Sprintf("Key %s", time.Now().Format("Jan 2"))
	}
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return AssistantSettingsView{}, err
	}
	entry := assistantStoredKey{
		ID:    fmt.Sprintf("key-%d", time.Now().UnixNano()),
		Label: label,
		Key:   key,
	}
	config.Keys = append(config.Keys, entry)
	// A freshly added key becomes the active one; that's why you add it.
	config.ActiveKeyID = entry.ID
	if err := a.database.saveAssistantConfig(a.context(), config); err != nil {
		return AssistantSettingsView{}, err
	}
	return assistantView(config), nil
}

func (a *App) RemoveAssistantKey(id string) (AssistantSettingsView, error) {
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return AssistantSettingsView{}, err
	}
	kept := config.Keys[:0]
	for _, key := range config.Keys {
		if key.ID != id {
			kept = append(kept, key)
		}
	}
	config.Keys = kept
	if config.ActiveKeyID == id {
		config.ActiveKeyID = ""
		if len(config.Keys) > 0 {
			config.ActiveKeyID = config.Keys[0].ID
		}
	}
	if err := a.database.saveAssistantConfig(a.context(), config); err != nil {
		return AssistantSettingsView{}, err
	}
	return assistantView(config), nil
}

func (a *App) SelectAssistantKey(id string) (AssistantSettingsView, error) {
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return AssistantSettingsView{}, err
	}
	found := false
	for _, key := range config.Keys {
		if key.ID == id {
			found = true
			break
		}
	}
	if !found {
		return AssistantSettingsView{}, errors.New("unknown API key")
	}
	config.ActiveKeyID = id
	if err := a.database.saveAssistantConfig(a.context(), config); err != nil {
		return AssistantSettingsView{}, err
	}
	return assistantView(config), nil
}

func (a *App) SetAssistantModel(model string) (AssistantSettingsView, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return AssistantSettingsView{}, errors.New("the model is empty")
	}
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return AssistantSettingsView{}, err
	}
	if config.Models == nil {
		config.Models = map[string]string{}
	}
	config.Models[config.provider()] = model
	if err := a.database.saveAssistantConfig(a.context(), config); err != nil {
		return AssistantSettingsView{}, err
	}
	return assistantView(config), nil
}

func (a *App) SetAssistantProvider(provider string) (AssistantSettingsView, error) {
	if provider != "api" && provider != "claude-cli" && provider != "codex-cli" {
		return AssistantSettingsView{}, errors.New("unknown provider")
	}
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return AssistantSettingsView{}, err
	}
	config.Provider = provider
	if err := a.database.saveAssistantConfig(a.context(), config); err != nil {
		return AssistantSettingsView{}, err
	}
	return assistantView(config), nil
}

// saveClaudeOAuthToken stores the long-lived token minted by the hidden
// `claude setup-token` sign-in; headless runs pass it as CLAUDE_CODE_OAUTH_TOKEN.
func (a *App) saveClaudeOAuthToken(token string) error {
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return err
	}
	config.ClaudeOAuthToken = token
	return a.database.saveAssistantConfig(a.context(), config)
}

// ListAssistantModels reports what the active provider can use: the Models API
// for keys, the CLI's own local model cache for subscriptions.
func (a *App) ListAssistantModels() ([]AssistantModel, error) {
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return nil, err
	}
	switch config.provider() {
	case "claude-cli":
		return assistantClaudeCLIModels()
	case "codex-cli":
		return assistantCodexCLIModels()
	}
	key := config.activeKey()
	if key == "" {
		return nil, errors.New("no active API key")
	}
	ctx, cancel := context.WithTimeout(a.context(), 30*time.Second)
	defer cancel()
	client := anthropic.NewClient(option.WithAPIKey(key))
	models := []AssistantModel{}
	iter := client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	for iter.Next() {
		model := iter.Current()
		models = append(models, AssistantModel{ID: model.ID, Name: model.DisplayName})
	}
	if err := iter.Err(); err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
			return nil, errors.New("the API key was rejected")
		}
		return nil, fmt.Errorf("could not list models: %w", err)
	}
	return models, nil
}
