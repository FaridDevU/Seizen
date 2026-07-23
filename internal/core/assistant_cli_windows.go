//go:build windows

package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// assistantAgentPaths mirrors the managed-terminal layout so the headless
// assistant shares profiles (and logins) with the project agent terminals.
func assistantAgentPaths() (claudeExe, codexExe, home, codexBin, claudeBin, codexProfile, claudeProfile string, err error) {
	configBase, err := os.UserConfigDir()
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	localBase, err := os.UserCacheDir()
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	localRoot := filepath.Join(localBase, "Seizen")
	home = filepath.Join(localRoot, "home")
	codexBin = filepath.Join(localRoot, "tools", "codex", "bin")
	claudeBin = filepath.Join(home, ".local", "bin")
	codexProfile = filepath.Join(configBase, "Seizen", "profiles", "codex")
	claudeProfile = filepath.Join(configBase, "Seizen", "profiles", "claude")
	claudeExe = filepath.Join(claudeBin, "claude.exe")
	codexExe = filepath.Join(codexBin, "codex.exe")
	return
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func claudeStateHasAccount(path string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(raw), `"oauthAccount"`)
}

// claudeProfileSignedIn: .claude.json exists as soon as the CLI ever launched;
// only an oauthAccount entry inside it (or stored credentials) means a login.
func claudeProfileSignedIn(profile string) bool {
	return fileExists(filepath.Join(profile, ".credentials.json")) ||
		claudeStateHasAccount(filepath.Join(profile, ".claude.json"))
}

func assistantCLIStatus(agent string) AssistantCLIStatus {
	claudeExe, codexExe, _, _, _, codexProfile, claudeProfile, err := assistantAgentPaths()
	if err != nil {
		return AssistantCLIStatus{}
	}
	if agent == "codex" {
		signedIn := fileExists(filepath.Join(codexProfile, "auth.json"))
		return AssistantCLIStatus{
			Installed: fileExists(codexExe) || signedIn,
			Note:      cliStatusNote(signedIn),
		}
	}
	signedIn := claudeProfileSignedIn(claudeProfile)
	return AssistantCLIStatus{
		Installed: fileExists(claudeExe) || signedIn,
		Note:      cliStatusNote(signedIn),
	}
}

// Curated base families plus whatever the account's local cache adds.
func assistantClaudeCLIModels() ([]AssistantModel, error) {
	_, _, _, _, _, _, claudeProfile, err := assistantAgentPaths()
	if err != nil {
		return nil, err
	}
	models := []AssistantModel{
		{ID: "", Name: "Account default"},
		{ID: "opus", Name: "Claude Opus"},
		{ID: "sonnet", Name: "Claude Sonnet"},
		{ID: "haiku", Name: "Claude Haiku"},
	}
	raw, readErr := os.ReadFile(filepath.Join(claudeProfile, ".claude.json"))
	if readErr == nil {
		for _, extra := range parseClaudeModelCache(string(raw)) {
			duplicate := false
			for _, existing := range models {
				if existing.ID == extra.ID {
					duplicate = true
					break
				}
			}
			if !duplicate {
				models = append(models, extra)
			}
		}
	}
	return models, nil
}

func assistantCodexCLIModels() ([]AssistantModel, error) {
	_, _, _, _, _, codexProfile, _, err := assistantAgentPaths()
	if err != nil {
		return nil, err
	}
	models := []AssistantModel{{ID: "", Name: "Account default"}}
	raw, readErr := os.ReadFile(filepath.Join(codexProfile, "models_cache.json"))
	if readErr == nil {
		models = append(models, parseCodexModelCache(string(raw))...)
	}
	return models, nil
}

const createNoWindow = 0x08000000

// runAssistantCLI performs one headless conversation turn on the subscription
// CLI and returns the model's raw text answer plus the session id to resume the
// chat later. The process dies after every turn: the CLI's own session files
// are the chat's memory, so nothing stays running in the background. token is
// the long-lived Claude subscription token from the in-app sign-in ("" for
// codex or profile logins).
func runAssistantCLI(ctx context.Context, agent, environment, model, system, prompt, token, session string) (string, string, error) {
	claudeExe, codexExe, home, codexBin, claudeBin, codexProfile, claudeProfile, err := assistantAgentPaths()
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	hostExe := claudeExe
	if agent == "codex" {
		hostExe = codexExe
	}

	if fileExists(hostExe) {
		var cmd *exec.Cmd
		if agent == "codex" {
			// Codex has no system-prompt flag in exec mode: fold it into the prompt.
			args := []string{"exec"}
			if session != "" {
				args = append(args, "resume", session)
			}
			args = append(args, "--json", "--skip-git-repo-check", "--color", "never", "-C", home, "-s", "read-only")
			if model != "" {
				args = append(args, "-m", model)
			}
			args = append(args, system+"\n\n"+prompt)
			cmd = exec.CommandContext(ctx, hostExe, args...)
		} else {
			args := []string{"-p", "--output-format", "stream-json", "--verbose",
				"--max-turns", "1", "--disallowedTools", assistantConversationalDisallowed,
				"--append-system-prompt", system}
			if session != "" {
				args = append(args, "--resume", session)
			}
			if model != "" {
				args = append(args, "--model", model)
			}
			cmd = exec.CommandContext(ctx, hostExe, args...)
			cmd.Stdin = strings.NewReader(prompt)
		}
		cmd.Dir = home
		cmd.Env = managedAgentEnvironment(os.Environ(), home, codexBin, claudeBin, codexProfile, claudeProfile)
		if agent == "claude" && token != "" {
			cmd.Env = append(cmd.Env, "CLAUDE_CODE_OAUTH_TOKEN="+token)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		return runAndParse(agent, cmd)
	}

	// No host binary: run inside the configured WSL distribution, where project
	// agent terminals installed the CLI. The Windows profile dir is reused via /mnt.
	if environment == "" || environment == "cmd" {
		environment = defaultAgentEnvironment
	}
	definition, ok := wslDistributionByID(environment)
	if !ok {
		return "", "", fmt.Errorf("%s is not installed: open a %s terminal in a project once to set it up", agent, agent)
	}
	wslPath, err := systemWSLPath()
	if err != nil {
		return "", "", err
	}
	codexProfileWSL, err := windowsPathToWSL(codexProfile)
	if err != nil {
		return "", "", err
	}
	claudeProfileWSL, err := windowsPathToWSL(claudeProfile)
	if err != nil {
		return "", "", err
	}
	script := "export HOME=/root; export PATH=/root/.local/bin:/usr/local/bin:$PATH" +
		"; export CODEX_HOME=" + shellLiteral(codexProfileWSL) +
		"; export CLAUDE_CONFIG_DIR=" + shellLiteral(claudeProfileWSL) +
		"; command -v " + agent + " >/dev/null 2>&1 || exit 86; "
	if agent == "claude" && token != "" {
		script += "export CLAUDE_CODE_OAUTH_TOKEN=" + shellLiteral(token) + "; "
	}
	var stdin string
	if agent == "codex" {
		arguments := "exec"
		if session != "" {
			arguments += " resume " + shellLiteral(session)
		}
		arguments += " --json --skip-git-repo-check --color never -s read-only"
		if model != "" {
			arguments += " -m " + shellLiteral(model)
		}
		script += "exec codex " + arguments + " " + shellLiteral(system+"\n\n"+prompt) + " < /dev/null"
	} else {
		arguments := "-p --output-format stream-json --verbose --max-turns 1" +
			" --disallowedTools " + shellLiteral(assistantConversationalDisallowed) +
			" --append-system-prompt " + shellLiteral(system)
		if session != "" {
			arguments += " --resume " + shellLiteral(session)
		}
		if model != "" {
			arguments += " --model " + shellLiteral(model)
		}
		script += "exec claude " + arguments
		stdin = prompt
	}
	cmd := exec.CommandContext(ctx, wslPath, "--distribution", definition.RuntimeName,
		"--user", "root", "--exec", "/bin/sh", "-lc", script)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	text, newSession, err := runAndParse(agent, cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 86 {
		return "", "", fmt.Errorf("%s is not installed: open a %s terminal in a project once to set it up", agent, agent)
	}
	return text, newSession, err
}

func runAndParse(agent string, cmd *exec.Cmd) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	output := stdout.String()
	var text, session string
	var parseErr error
	if agent == "codex" {
		text, session, parseErr = parseCodexHeadlessResult(output)
	} else {
		text, session, parseErr = parseClaudeHeadlessResult(output)
	}
	if parseErr == nil {
		return text, session, nil
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = runErr.Error()
		}
		if len(detail) > 400 {
			detail = detail[:400]
		}
		return "", "", fmt.Errorf("%s run failed: %s", agent, detail)
	}
	return "", "", parseErr
}
