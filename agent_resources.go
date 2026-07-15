package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	codexEnvironmentSetting     = "agent_codex_environment"
	claudeEnvironmentSetting    = "agent_claude_environment"
	opencodeEnvironmentSetting  = "agent_opencode_environment"
	codexUnrestrictedSetting    = "agent_codex_unrestricted"
	claudeUnrestrictedSetting   = "agent_claude_unrestricted"
	opencodeUnrestrictedSetting = "agent_opencode_unrestricted"
	sharedExtensionsSetting     = "agent_shared_extensions"
	defaultAgentEnvironment     = "debian"
)

type AgentResourceSettings struct {
	CodexEnvironment     string `json:"codexEnvironment"`
	ClaudeEnvironment    string `json:"claudeEnvironment"`
	OpencodeEnvironment  string `json:"opencodeEnvironment"`
	CodexUnrestricted    bool   `json:"codexUnrestricted"`
	ClaudeUnrestricted   bool   `json:"claudeUnrestricted"`
	OpencodeUnrestricted bool   `json:"opencodeUnrestricted"`
	SharedExtensions     bool   `json:"sharedExtensions"`
}

func defaultAgentResourceSettings() AgentResourceSettings {
	return AgentResourceSettings{
		CodexEnvironment:    defaultAgentEnvironment,
		ClaudeEnvironment:   defaultAgentEnvironment,
		OpencodeEnvironment: defaultAgentEnvironment,
	}
}

func (a *App) GetAgentResourceSettings() (AgentResourceSettings, error) {
	return a.database.AgentResourceSettings(a.context())
}

func (a *App) SetAgentResourceSettings(settings AgentResourceSettings) (AgentResourceSettings, error) {
	return a.database.SetAgentResourceSettings(a.context(), settings)
}

func (d *Database) AgentResourceSettings(ctx context.Context) (AgentResourceSettings, error) {
	db, err := d.Pool(ctx)
	if err != nil {
		return AgentResourceSettings{}, err
	}
	settings := defaultAgentResourceSettings()
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN (?, ?, ?, ?, ?, ?, ?)`,
		codexEnvironmentSetting, claudeEnvironmentSetting, opencodeEnvironmentSetting,
		codexUnrestrictedSetting, claudeUnrestrictedSetting, opencodeUnrestrictedSetting,
		sharedExtensionsSetting)
	if err != nil {
		return AgentResourceSettings{}, fmt.Errorf("could not load agent settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return AgentResourceSettings{}, err
		}
		switch key {
		case codexEnvironmentSetting:
			settings.CodexEnvironment = value
		case claudeEnvironmentSetting:
			settings.ClaudeEnvironment = value
		case opencodeEnvironmentSetting:
			settings.OpencodeEnvironment = value
		case codexUnrestrictedSetting:
			settings.CodexUnrestricted = value == "1"
		case claudeUnrestrictedSetting:
			settings.ClaudeUnrestricted = value == "1"
		case opencodeUnrestrictedSetting:
			settings.OpencodeUnrestricted = value == "1"
		case sharedExtensionsSetting:
			settings.SharedExtensions = value == "1"
		}
	}
	if err = rows.Err(); err != nil {
		return AgentResourceSettings{}, err
	}
	if err = validateAgentResourceSettings(settings); err != nil {
		return AgentResourceSettings{}, fmt.Errorf("the saved agent settings are not valid: %w", err)
	}
	return settings, nil
}

func (d *Database) SetAgentResourceSettings(ctx context.Context, settings AgentResourceSettings) (AgentResourceSettings, error) {
	settings.CodexEnvironment = strings.TrimSpace(settings.CodexEnvironment)
	settings.ClaudeEnvironment = strings.TrimSpace(settings.ClaudeEnvironment)
	settings.OpencodeEnvironment = strings.TrimSpace(settings.OpencodeEnvironment)
	if err := validateAgentResourceSettings(settings); err != nil {
		return AgentResourceSettings{}, err
	}
	db, err := d.Pool(ctx)
	if err != nil {
		return AgentResourceSettings{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AgentResourceSettings{}, err
	}
	defer tx.Rollback()
	values := [][2]string{
		{codexEnvironmentSetting, settings.CodexEnvironment},
		{claudeEnvironmentSetting, settings.ClaudeEnvironment},
		{opencodeEnvironmentSetting, settings.OpencodeEnvironment},
		{codexUnrestrictedSetting, boolSetting(settings.CodexUnrestricted)},
		{claudeUnrestrictedSetting, boolSetting(settings.ClaudeUnrestricted)},
		{opencodeUnrestrictedSetting, boolSetting(settings.OpencodeUnrestricted)},
		{sharedExtensionsSetting, boolSetting(settings.SharedExtensions)},
	}
	for _, item := range values {
		if _, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, item[0], item[1]); err != nil {
			return AgentResourceSettings{}, fmt.Errorf("could not save agent settings: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return AgentResourceSettings{}, fmt.Errorf("could not save agent settings: %w", err)
	}
	return settings, nil
}

func validateAgentResourceSettings(settings AgentResourceSettings) error {
	for _, environment := range []string{settings.CodexEnvironment, settings.ClaudeEnvironment} {
		if environment == "cmd" {
			continue
		}
		if _, ok := wslDistributionByID(environment); !ok {
			return errors.New("the agent environment is not valid")
		}
	}
	// OpenCode has no managed Windows installer: WSL only.
	if _, ok := wslDistributionByID(settings.OpencodeEnvironment); !ok {
		return errors.New("the agent environment is not valid")
	}
	return nil
}

func boolSetting(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func agentEnvironment(settings AgentResourceSettings, agent string) (string, bool) {
	if agent == "codex" {
		return settings.CodexEnvironment, settings.CodexUnrestricted
	}
	if agent == "opencode" {
		return settings.OpencodeEnvironment, settings.OpencodeUnrestricted
	}
	if settings.ClaudeUnrestricted {
		// --dangerously-skip-permissions cannot run as root, and Seizen's WSL
		// sessions log in as root: with permissions skipped, Claude runs through CMD.
		return "cmd", true
	}
	return settings.ClaudeEnvironment, settings.ClaudeUnrestricted
}
