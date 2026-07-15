//go:build windows

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedAgentsReceiveEphemeralBridgeWithoutPersistingToken(t *testing.T) {
	base := t.TempDir()
	configBase := filepath.Join(base, "Roaming")
	localBase := filepath.Join(base, "Local")
	t.Setenv("APPDATA", configBase)
	t.Setenv("LOCALAPPDATA", localBase)
	home := filepath.Join(localBase, "Seizen", "home")
	codex := filepath.Join(localBase, "Seizen", "tools", "codex", "bin", "codex.exe")
	claude := filepath.Join(home, ".local", "bin", "claude.exe")
	for _, executable := range []string{codex, claude} {
		if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(executable, []byte("test"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bridge := agentTerminalBridgeConfig{URL: "http://127.0.0.1:43123", Token: "temporary-secret-token"}
	codexCommand, err := projectAgentTerminalCommand("codex", base, bridge)
	if err != nil {
		t.Fatal(err)
	}
	if environmentValue(codexCommand.env, agentBridgeURLEnv) != bridge.URL || environmentValue(codexCommand.env, agentBridgeTokenEnv) != bridge.Token {
		t.Fatal("Codex did not receive the temporary bridge environment")
	}
	profile, err := os.ReadFile(filepath.Join(configBase, "Seizen", "profiles", "codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(profile), "--seizen-agent-bridge") || strings.Contains(string(profile), bridge.Token) {
		t.Fatalf("unsafe Codex MCP profile: %s", profile)
	}
	for _, variable := range []string{agentBridgeURLEnv, agentBridgeTokenEnv, "WSLENV"} {
		if !strings.Contains(string(profile), variable) {
			t.Fatalf("Codex MCP profile does not forward %s: %s", variable, profile)
		}
	}
	claudeCommand, err := projectAgentTerminalCommand("claude", base, bridge)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(claudeCommand.args, " ")
	configuration, readErr := os.ReadFile(filepath.Join(configBase, "Seizen", "profiles", "claude", "seizen-mcp.json"))
	if !strings.Contains(joined, "--mcp-config") || !strings.Contains(joined, "seizen-mcp.json") || strings.Contains(joined, "mcpServers") ||
		readErr != nil || !strings.Contains(string(configuration), "--seizen-agent-bridge") || strings.Contains(string(configuration), bridge.Token) {
		t.Fatalf("unsafe Claude MCP arguments: %s", joined)
	}
}

func TestManagedAgentsUseSelectedWSLPermissionsAndProjectProfile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(base, "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(base, "Local"))
	bridge := agentTerminalBridgeConfig{
		URL: "http://127.0.0.1:43123", Token: "temporary-secret-token",
		ProjectID: "project-one", Environment: "debian", Unrestricted: true,
	}
	command, err := projectAgentTerminalCommand("codex", base, bridge)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command.args, " ")
	for _, expected := range []string{
		"--distribution Seizen-Debian", "--user root", "--dangerously-bypass-approvals-and-sandbox",
		"profiles/projects/project-one/codex", "npm install --global @openai/codex", agentBridgeURLEnv,
	} {
		if !strings.Contains(strings.ReplaceAll(joined, `\`, "/"), expected) {
			t.Fatalf("WSL command is missing %q: %s", expected, joined)
		}
	}
	profile := filepath.Join(base, "Roaming", "Seizen", "profiles", "projects", "project-one", "codex", "config.toml")
	contents, err := os.ReadFile(profile)
	if err != nil || strings.Contains(string(contents), bridge.Token) || !strings.Contains(string(contents), "/mnt/") ||
		!strings.Contains(string(contents), `env_vars = ["SEIZEN_BRIDGE_URL", "SEIZEN_BRIDGE_TOKEN", "WSLENV"]`) {
		t.Fatalf("unsafe WSL Codex profile: %q, %v", contents, err)
	}

	command, err = projectAgentTerminalCommand("claude", base, bridge)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(command.args, " ")
	if !strings.Contains(joined, "--dangerously-skip-permissions") || !strings.Contains(joined, "curl -fsSL https://claude.ai/install.sh | bash") || !strings.Contains(joined, "seizen-mcp.json") {
		t.Fatalf("unexpected Claude WSL launch: %s", joined)
	}
}

func TestCodexMCPProfilePreservesSettingsAddedInsideBridgeMarkers(t *testing.T) {
	profile := t.TempDir()
	config := filepath.Join(profile, "config.toml")
	previous := `cli_auth_credentials_store = "file"

# BEGIN SEIZEN AGENT BRIDGE
[mcp_servers.seizen]
command = "old.exe"
args = ["--seizen-agent-bridge"]

[projects."C:/project"]
trust_level = "trusted"
# END SEIZEN AGENT BRIDGE
`
	if err := os.WriteFile(config, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureCodexMCPProfile(profile, "new.exe"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(config)
	if err != nil || !strings.Contains(string(contents), `[projects."C:/project"]`) ||
		!strings.Contains(string(contents), `trust_level = "trusted"`) || strings.Contains(string(contents), `command = "old.exe"`) {
		t.Fatalf("Codex settings were not preserved: %s, %v", contents, err)
	}
}
