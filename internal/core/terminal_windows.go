//go:build windows

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/x/conpty"
	"golang.org/x/sys/windows"
)

var createPseudoConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole")

const (
	terminalJobActiveProcessLimit = 64
	terminalJobMemoryLimit        = uintptr(2 * 1024 * 1024 * 1024)
)

type windowsTerminalJob struct {
	handle windows.Handle
	once   sync.Once
	err    error
}

type windowsTerminalBackend struct {
	pty             *conpty.ConPty
	output          *os.File
	process         *os.Process
	job             *windowsTerminalJob
	outputCloseOnce sync.Once
	closeOnce       sync.Once
	closeErr        error
}

func projectTerminalCommand(shell, projectPath, wslRuntime string) (*terminalCommand, error) {
	switch shell {
	case "cmd":
		path, err := exec.LookPath("cmd.exe")
		if err != nil {
			return nil, errors.New("cmd.exe is not available")
		}
		return &terminalCommand{
			path: path,
			args: []string{filepath.Base(path), "/D", "/Q"},
			dir:  projectPath,
		}, nil
	case "wsl":
		definition, ok := wslDistributionByRuntime(wslRuntime)
		if !ok {
			return nil, errors.New("the WSL distribution is not valid")
		}
		path, err := systemWSLPath()
		if err != nil {
			return nil, errors.New("WSL is not installed or wsl.exe is not available")
		}
		return &terminalCommand{
			path: path,
			args: []string{filepath.Base(path), "--distribution", definition.RuntimeName, "--user", "root", "--cd", projectPath, "--exec", "/bin/sh", "-l"},
			dir:  projectPath,
		}, nil
	case "codex", "claude", "opencode":
		return managedAgentCommand(shell, projectPath, nil)
	default:
		return nil, errors.New("the terminal only supports cmd, wsl, codex, claude, or opencode")
	}
}

func projectAgentTerminalCommand(agent, projectPath string, bridge agentTerminalBridgeConfig) (*terminalCommand, error) {
	return managedAgentCommand(agent, projectPath, &bridge)
}

func managedAgentCommand(agent, projectPath string, bridge *agentTerminalBridgeConfig) (*terminalCommand, error) {
	if bridge != nil && bridge.Environment != "" && bridge.Environment != "cmd" {
		return managedWSLAgentCommand(agent, projectPath, *bridge)
	}
	if agent == "opencode" {
		// ponytail: no managed Windows installer for OpenCode; WSL covers
		// the default case and avoids duplicating the native install flow.
		return nil, errors.New("OpenCode runs inside a WSL environment; configure it in Resources > AI Agents")
	}
	return managedWindowsAgentCommand(agent, projectPath, bridge)
}

func managedWindowsAgentCommand(agent, projectPath string, bridge *agentTerminalBridgeConfig) (*terminalCommand, error) {
	configBase, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("could not find Seizen's configuration folder: %w", err)
	}
	localBase, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("could not find Seizen's local folder: %w", err)
	}

	configRoot, err := agentProfileRoot(filepath.Join(configBase, "Seizen", "profiles"), bridge)
	if err != nil {
		return nil, err
	}
	localRoot := filepath.Join(localBase, "Seizen")
	home := filepath.Join(localRoot, "home")
	codexBin := filepath.Join(localRoot, "tools", "codex", "bin")
	codexInstallHome := filepath.Join(localRoot, "tools", "codex", "install")
	claudeBin := filepath.Join(home, ".local", "bin")
	codexProfile := filepath.Join(configRoot, "codex")
	claudeProfile := filepath.Join(configRoot, "claude")
	for _, path := range []string{home, codexBin, codexInstallHome, claudeBin, codexProfile, claudeProfile} {
		if err = os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("could not prepare the environment for %s: %w", agent, err)
		}
	}
	if err = ensureCodexProfile(codexProfile); err != nil {
		return nil, err
	}

	environment := managedAgentEnvironment(os.Environ(), home, codexBin, claudeBin, codexProfile, claudeProfile)
	launchArgs := []string{}
	if bridge != nil {
		if bridge.URL == "" || bridge.Token == "" {
			return nil, errors.New("the agent's temporary bridge is not configured")
		}
		seizenExecutable, executableErr := os.Executable()
		if executableErr != nil {
			return nil, fmt.Errorf("could not locate Seizen to configure MCP: %w", executableErr)
		}
		if bridge.Unrestricted {
			if agent == "codex" {
				launchArgs = append(launchArgs, "--dangerously-bypass-approvals-and-sandbox")
			} else {
				launchArgs = append(launchArgs, "--dangerously-skip-permissions")
			}
		}
		if agent == "codex" {
			if err = ensureCodexMCPProfile(codexProfile, seizenExecutable); err != nil {
				return nil, err
			}
		} else {
			configuration, configurationErr := writeClaudeMCPConfig(claudeProfile, seizenExecutable)
			if configurationErr != nil {
				return nil, configurationErr
			}
			launchArgs = append(launchArgs, "--mcp-config", configuration)
		}
		environment = replaceWindowsEnvironment(environment, [][2]string{
			{agentBridgeURLEnv, bridge.URL},
			{agentBridgeTokenEnv, bridge.Token},
		})
		if bridge.Task != "" {
			// The CLIs take an initial prompt as their positional argument.
			launchArgs = append(launchArgs, bridge.Task)
		}
	}
	executable := filepath.Join(codexBin, "codex.exe")
	installURL := "https://chatgpt.com/codex/install.ps1"
	if agent == "claude" {
		executable = filepath.Join(claudeBin, "claude.exe")
		installURL = "https://claude.ai/install.ps1"
	}
	if _, err = os.Stat(executable); err == nil {
		return &terminalCommand{path: executable, args: append([]string{filepath.Base(executable)}, launchArgs...), dir: projectPath, env: environment}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("could not check the installation of %s: %w", agent, err)
	}

	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		return nil, errors.New("PowerShell is not available to install the tool")
	}
	// ponytail: simultaneous first launches may race; retrying the failed panel is enough for a desktop app.
	script := managedAgentInstallScript(agent, installURL, executable, codexBin, codexInstallHome, codexProfile, launchArgs)
	return &terminalCommand{
		path: powershell,
		args: []string{filepath.Base(powershell), "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script},
		dir:  projectPath,
		env:  environment,
	}, nil
}

func managedWSLAgentCommand(agent, projectPath string, bridge agentTerminalBridgeConfig) (*terminalCommand, error) {
	definition, ok := wslDistributionByID(bridge.Environment)
	if !ok {
		return nil, errors.New("the agent's WSL distribution is not valid")
	}
	wslPath, err := systemWSLPath()
	if err != nil {
		return nil, err
	}
	configBase, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("could not find Seizen's configuration folder: %w", err)
	}
	profileRoot, err := agentProfileRoot(filepath.Join(configBase, "Seizen", "profiles"), &bridge)
	if err != nil {
		return nil, err
	}
	codexProfile := filepath.Join(profileRoot, "codex")
	claudeProfile := filepath.Join(profileRoot, "claude")
	opencodeProfile := filepath.Join(profileRoot, "opencode")
	for _, path := range []string{codexProfile, claudeProfile, opencodeProfile} {
		if err = os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("could not prepare the agent's profile: %w", err)
		}
	}
	if err = ensureCodexProfile(codexProfile); err != nil {
		return nil, err
	}
	seizenExecutable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("could not locate Seizen to configure MCP: %w", err)
	}
	seizenWSL, err := windowsPathToWSL(seizenExecutable)
	if err != nil {
		return nil, err
	}
	codexWSL, err := windowsPathToWSL(codexProfile)
	if err != nil {
		return nil, err
	}
	claudeWSL, err := windowsPathToWSL(claudeProfile)
	if err != nil {
		return nil, err
	}
	launchArgs := make([]string, 0, 3)
	if bridge.Unrestricted {
		if agent == "codex" {
			launchArgs = append(launchArgs, "--dangerously-bypass-approvals-and-sandbox")
		} else if agent == "claude" {
			launchArgs = append(launchArgs, "--dangerously-skip-permissions")
		}
		// opencode has no CLI flag: permissions go in opencode.json.
	}
	opencodeConfigWSL := ""
	if agent == "codex" {
		if err = ensureCodexMCPProfile(codexProfile, seizenWSL); err != nil {
			return nil, err
		}
	} else if agent == "claude" {
		configuration, configurationErr := writeClaudeMCPConfig(claudeProfile, seizenWSL)
		if configurationErr != nil {
			return nil, configurationErr
		}
		configuration, configurationErr = windowsPathToWSL(configuration)
		if configurationErr != nil {
			return nil, configurationErr
		}
		launchArgs = append(launchArgs, "--mcp-config", configuration)
	} else if agent == "opencode" {
		configuration, configurationErr := writeOpenCodeMCPConfig(opencodeProfile, seizenWSL, bridge.Unrestricted)
		if configurationErr != nil {
			return nil, configurationErr
		}
		opencodeConfigWSL, configurationErr = windowsPathToWSL(configuration)
		if configurationErr != nil {
			return nil, configurationErr
		}
	} else {
		return nil, errors.New("the agent must be codex, claude, or opencode")
	}
	if bridge.Task != "" {
		// The CLIs take an initial prompt as their positional argument.
		launchArgs = append(launchArgs, bridge.Task)
	}
	script := managedWSLAgentScript(agent, codexWSL, claudeWSL, opencodeConfigWSL, bridge, launchArgs)
	return &terminalCommand{
		path: wslPath,
		args: []string{filepath.Base(wslPath), "--distribution", definition.RuntimeName,
			"--user", "root", "--cd", projectPath, "--exec", "/bin/sh", "-lc", script},
		dir: projectPath,
	}, nil
}

func agentProfileRoot(root string, bridge *agentTerminalBridgeConfig) (string, error) {
	if bridge == nil || bridge.SharedExtensions || bridge.ProjectID == "" {
		return root, nil
	}
	for _, character := range bridge.ProjectID {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return "", errors.New("the project cannot be used as an agent profile")
		}
	}
	return filepath.Join(root, "projects", bridge.ProjectID), nil
}

func windowsPathToWSL(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("the Windows path is not valid: %w", err)
	}
	volume := filepath.VolumeName(absolute)
	if len(volume) != 2 || volume[1] != ':' {
		return "", errors.New("WSL only supports managed paths on a local drive")
	}
	rest := strings.TrimPrefix(absolute, volume)
	return "/mnt/" + strings.ToLower(volume[:1]) + strings.ReplaceAll(rest, `\`, "/"), nil
}

func managedWSLAgentScript(agent, codexProfile, claudeProfile, opencodeConfig string, bridge agentTerminalBridgeConfig, launchArgs []string) string {
	packages := "ca-certificates curl"
	install := `curl -fsSL https://claude.ai/install.sh | bash`
	if agent == "codex" {
		packages += " nodejs npm"
		install = `npm install --global @openai/codex`
	} else if agent == "opencode" {
		packages += " nodejs npm"
		install = `npm install --global opencode-ai`
	}
	prerequisites := "if command -v apt-get >/dev/null 2>&1; then apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y " + packages +
		"; elif command -v dnf >/dev/null 2>&1; then dnf install -y " + packages +
		"; elif command -v pacman >/dev/null 2>&1; then pacman -Sy --noconfirm " + packages +
		`; else echo 'Unsupported distribution for installing the agent.' >&2; exit 1; fi`
	arguments := ""
	for _, argument := range launchArgs {
		arguments += " " + shellLiteral(argument)
	}
	exports := "set -e; export HOME=/root; export PATH=/root/.local/bin:/usr/local/bin:$PATH" +
		"; export CODEX_HOME=" + shellLiteral(codexProfile) +
		"; export CLAUDE_CONFIG_DIR=" + shellLiteral(claudeProfile)
	if opencodeConfig != "" {
		exports += "; export OPENCODE_CONFIG=" + shellLiteral(opencodeConfig)
	}
	return exports +
		"; export " + agentBridgeURLEnv + "=" + shellLiteral(bridge.URL) +
		"; export " + agentBridgeTokenEnv + "=" + shellLiteral(bridge.Token) +
		`; export WSLENV="${WSLENV:+$WSLENV:}` + agentBridgeURLEnv + `:` + agentBridgeTokenEnv + `"` +
		"; if ! command -v " + agent + " >/dev/null 2>&1; then " + prerequisites + "; " + install + "; fi" +
		"; exec " + agent + arguments
}

func shellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func ensureCodexMCPProfile(path, executable string) error {
	configPath := filepath.Join(path, "config.toml")
	contents, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("could not read the Codex profile: %w", err)
	}
	const begin = "# BEGIN SEIZEN AGENT BRIDGE"
	const end = "# END SEIZEN AGENT BRIDGE"
	lines := strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
	kept := lines[:0]
	inBridge := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == begin || trimmed == end {
			continue
		}
		if trimmed == "[mcp_servers.seizen]" {
			inBridge = true
			continue
		}
		if inBridge && !strings.HasPrefix(trimmed, "[") {
			continue
		}
		inBridge = false
		kept = append(kept, line)
	}
	text := strings.Join(kept, "\n")
	block := begin + "\n[mcp_servers.seizen]\ncommand = " + strconv.Quote(executable) +
		"\nargs = [\"--seizen-agent-bridge\"]\nenv_vars = [" + strconv.Quote(agentBridgeURLEnv) + ", " +
		strconv.Quote(agentBridgeTokenEnv) + ", \"WSLENV\"]\n" + end + "\n"
	text = strings.TrimRight(text, "\r\n") + "\n\n" + block
	if err = os.WriteFile(configPath, []byte(text), 0o600); err != nil {
		return fmt.Errorf("could not register the MCP bridge in Codex: %w", err)
	}
	return nil
}

func writeClaudeMCPConfig(path, executable string) (string, error) {
	configPath := filepath.Join(path, "seizen-mcp.json")
	contents, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"seizen": map[string]any{"command": executable, "args": []string{"--seizen-agent-bridge"}},
	}})
	if err != nil {
		return "", errors.New("could not configure MCP for Claude")
	}
	if err = os.WriteFile(configPath, contents, 0o600); err != nil {
		return "", fmt.Errorf("could not save Claude's MCP configuration: %w", err)
	}
	return configPath, nil
}

// writeOpenCodeMCPConfig writes OpenCode's configuration with Seizen's
// local MCP bridge and the permissions chosen in Resources (OpenCode has no
// CLI flag for this); it is passed through the OPENCODE_CONFIG variable.
func writeOpenCodeMCPConfig(path, executable string, unrestricted bool) (string, error) {
	configPath := filepath.Join(path, "opencode.json")
	permission := "ask"
	if unrestricted {
		permission = "allow"
	}
	contents, err := json.Marshal(map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"permission": map[string]any{
			"edit":     permission,
			"bash":     permission,
			"webfetch": permission,
		},
		"mcp": map[string]any{
			"seizen": map[string]any{
				"type":    "local",
				"command": []string{executable, "--seizen-agent-bridge"},
				"enabled": true,
			},
		},
	})
	if err != nil {
		return "", errors.New("could not configure MCP for OpenCode")
	}
	if err = os.WriteFile(configPath, contents, 0o600); err != nil {
		return "", fmt.Errorf("could not save OpenCode's MCP configuration: %w", err)
	}
	return configPath, nil
}

func ensureCodexProfile(path string) error {
	file, err := os.OpenFile(filepath.Join(path, "config.toml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not prepare the Codex profile: %w", err)
	}
	if _, err = file.WriteString("cli_auth_credentials_store = \"file\"\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("could not configure the Codex profile: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("could not save the Codex profile: %w", err)
	}
	return nil
}

func managedAgentEnvironment(base []string, home, codexBin, claudeBin, codexProfile, claudeProfile string) []string {
	path := environmentValue(base, "PATH")
	managedPath := codexBin + string(os.PathListSeparator) + claudeBin
	if path != "" {
		managedPath += string(os.PathListSeparator) + path
	}
	return replaceWindowsEnvironment(base, [][2]string{
		{"HOME", home},
		{"USERPROFILE", home},
		{"CODEX_HOME", codexProfile},
		{"CLAUDE_CONFIG_DIR", claudeProfile},
		{"PATH", managedPath},
	})
}

func environmentValue(environment []string, name string) string {
	for _, item := range environment {
		key, value, found := strings.Cut(item, "=")
		if found && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func replaceWindowsEnvironment(base []string, overrides [][2]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	used := make([]bool, len(overrides))
	for _, item := range base {
		key, _, found := strings.Cut(item, "=")
		matched := false
		if found {
			for index, override := range overrides {
				if strings.EqualFold(key, override[0]) {
					if !used[index] {
						result = append(result, override[0]+"="+override[1])
						used[index] = true
					}
					matched = true
					break
				}
			}
		}
		if !matched {
			result = append(result, item)
		}
	}
	for index, override := range overrides {
		if !used[index] {
			result = append(result, override[0]+"="+override[1])
		}
	}
	return result
}

func managedAgentInstallScript(agent, installURL, executable, codexBin, codexInstallHome, codexProfile string, launchArgs []string) string {
	setup := ""
	reset := ""
	if agent == "codex" {
		setup = "$env:CODEX_NON_INTERACTIVE='1'; $env:CODEX_INSTALL_DIR=" + powershellLiteral(codexBin) + "; $env:CODEX_HOME=" + powershellLiteral(codexInstallHome) + "; "
		reset = "$env:CODEX_HOME=" + powershellLiteral(codexProfile) + "; Remove-Item Env:CODEX_INSTALL_DIR -ErrorAction SilentlyContinue; Remove-Item Env:CODEX_NON_INTERACTIVE -ErrorAction SilentlyContinue; "
	}
	launch := "& " + powershellLiteral(executable)
	for _, argument := range launchArgs {
		launch += " " + powershellLiteral(argument)
	}
	return "$ErrorActionPreference='Stop'; Write-Host " + powershellLiteral("Preparing "+agent+" inside Seizen...") + "; " +
		"$previousUserPath=[Environment]::GetEnvironmentVariable('Path','User'); try { " + setup +
		"& (Join-Path $PSHOME 'powershell.exe') -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command " +
		powershellLiteral("Invoke-RestMethod -Uri '"+installURL+"' | Invoke-Expression") +
		"; if ($LASTEXITCODE -ne 0) { throw " + powershellLiteral("Could not install "+agent) + " } } finally { " +
		"[Environment]::SetEnvironmentVariable('Path',$previousUserPath,'User') }; " + reset +
		"if (-not (Test-Path -LiteralPath " + powershellLiteral(executable) + ")) { throw " + powershellLiteral("The "+agent+" installer did not create the expected executable") + " }; " +
		launch + "; exit $LASTEXITCODE"
}

func powershellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func startTerminalBackend(command *terminalCommand, columns, rows int) (terminalBackend, error) {
	if command == nil || command.path == "" || len(command.args) == 0 {
		return nil, errors.New("the terminal command is not valid")
	}
	if err := validateTerminalSize(columns, rows); err != nil {
		return nil, err
	}
	if err := createPseudoConsole.Find(); err != nil {
		return nil, errors.New("ConPTY requires Windows 10 version 1809 or later")
	}

	pty, err := conpty.New(columns, rows, 0)
	if err != nil {
		return nil, fmt.Errorf("could not create the ConPTY terminal: %w", err)
	}
	output, err := duplicateTerminalOutput(pty.OutPipeReadFd())
	if err != nil {
		_ = pty.Close()
		return nil, err
	}

	attributes := &syscall.ProcAttr{
		Dir: command.dir,
		Env: command.env,
		Sys: &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED},
	}
	pid, spawnedHandle, err := pty.Spawn(command.path, command.args, attributes)
	if err != nil {
		_ = output.Close()
		_ = pty.Close()
		return nil, fmt.Errorf("could not start the terminal: %w", err)
	}
	rawProcessHandle := windows.Handle(spawnedHandle)
	rawHandleOpen := true
	defer func() {
		if rawHandleOpen {
			_ = windows.CloseHandle(rawProcessHandle)
		}
	}()

	process, err := os.FindProcess(pid)
	if err != nil {
		terminateErr := windows.TerminateProcess(rawProcessHandle, 1)
		_ = output.Close()
		_ = pty.Close()
		if terminateErr != nil {
			return nil, fmt.Errorf("could not open the started terminal (%v) or terminate it: %w", err, terminateErr)
		}
		return nil, fmt.Errorf("could not open the started terminal: %w", err)
	}

	job, err := attachTerminalProcess(process)
	if err != nil {
		cleanupErr := cleanupFailedTerminalStart(process, nil, pty, output)
		if cleanupErr != nil {
			return nil, fmt.Errorf("could not isolate the terminal (%v) or clean it up: %w", err, cleanupErr)
		}
		return nil, fmt.Errorf("could not isolate the terminal in a Job Object; the process was cleaned up with a fallback: %w", err)
	}
	if err = windows.CloseHandle(rawProcessHandle); err != nil {
		cleanupErr := cleanupFailedTerminalStart(process, job, pty, output)
		if cleanupErr != nil {
			return nil, fmt.Errorf("could not release the initial HANDLE (%v) or clean up the terminal: %w", err, cleanupErr)
		}
		return nil, fmt.Errorf("could not release the terminal's initial HANDLE: %w", err)
	}
	rawHandleOpen = false

	if err = releaseTerminalProcess(process); err != nil {
		cleanupErr := cleanupFailedTerminalStart(process, job, pty, output)
		if cleanupErr != nil {
			return nil, fmt.Errorf("could not resume the terminal (%v) or clean it up: %w", err, cleanupErr)
		}
		return nil, fmt.Errorf("could not resume the terminal: %w", err)
	}

	return &windowsTerminalBackend{
		pty:     pty,
		output:  output,
		process: process,
		job:     job,
	}, nil
}

func duplicateTerminalOutput(handle uintptr) (*os.File, error) {
	currentProcess := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(
		currentProcess,
		windows.Handle(handle),
		currentProcess,
		&duplicate,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return nil, fmt.Errorf("could not prepare ConPTY output: %w", err)
	}
	return os.NewFile(uintptr(duplicate), "seizen-conpty-output"), nil
}

func cleanupFailedTerminalStart(process *os.Process, job *windowsTerminalJob, pty *conpty.ConPty, output *os.File) error {
	_ = output.Close()
	var jobErr, fallbackErr error
	terminated := false
	if job != nil {
		jobErr = job.close()
		terminated = jobErr == nil
	}
	if !terminated {
		fallbackErr = terminateUnmanagedTerminalProcess(process)
		terminated = fallbackErr == nil
		if fallbackErr != nil {
			killErr := process.Kill()
			terminated = killErr == nil || errors.Is(killErr, os.ErrProcessDone) || errors.Is(killErr, syscall.EINVAL)
			if !terminated {
				fallbackErr = errors.Join(fallbackErr, killErr)
			}
		}
	}
	ptyErr := pty.Close()
	if terminated {
		_, _ = process.Wait()
	}
	return errors.Join(jobErr, fallbackErr, ptyErr)
}

func (backend *windowsTerminalBackend) Read(data []byte) (int, error) {
	count, err := backend.output.Read(data)
	if err != nil {
		backend.outputCloseOnce.Do(func() { _ = backend.output.Close() })
	}
	return count, err
}

func (backend *windowsTerminalBackend) Write(data []byte) (int, error) {
	return backend.pty.Write(data)
}

func (backend *windowsTerminalBackend) resize(columns, rows int) error {
	return backend.pty.Resize(columns, rows)
}

func (backend *windowsTerminalBackend) wait() error {
	state, err := backend.process.Wait()
	if err != nil {
		return err
	}
	if !state.Success() {
		return &exec.ExitError{ProcessState: state}
	}
	return nil
}

func (backend *windowsTerminalBackend) close() error {
	backend.closeOnce.Do(func() {
		jobErr := backend.job.close()
		var treeErr error
		if jobErr != nil {
			fallbackErr := terminateUnmanagedTerminalProcess(backend.process)
			if fallbackErr != nil {
				treeErr = fmt.Errorf("closing the Job Object failed (%v) and the fallback did not guarantee the tree: %w", jobErr, fallbackErr)
			} else {
				treeErr = fmt.Errorf("closing the Job Object failed and taskkill /T was used as a fallback: %w", jobErr)
			}
		}
		backend.closeErr = errors.Join(treeErr, backend.pty.Close())
	})
	return backend.closeErr
}

func releaseTerminalProcess(process *os.Process) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("could not enumerate the suspended thread: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err = windows.Thread32First(snapshot, &entry); err != nil {
		return fmt.Errorf("could not read the suspended thread: %w", err)
	}
	for {
		if entry.OwnerProcessID == uint32(process.Pid) {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return fmt.Errorf("could not open the suspended thread: %w", openErr)
			}
			previous, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			if resumeErr != nil {
				return fmt.Errorf("could not resume the terminal: %w", resumeErr)
			}
			if previous == 0 {
				return errors.New("the terminal was not suspended before assigning it to the Job Object")
			}
			return nil
		}
		if err = windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return errors.New("the terminal's suspended thread was not found")
			}
			return fmt.Errorf("could not continue enumerating the suspended thread: %w", err)
		}
	}
}

func attachTerminalProcess(process *os.Process) (*windowsTerminalJob, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create the Job Object: %w", err)
	}
	closeJob := true
	defer func() {
		if closeJob {
			_ = windows.CloseHandle(job)
		}
	}()

	information := windowsTerminalJobLimitInformation()
	if _, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		return nil, fmt.Errorf("could not configure the Job Object: %w", err)
	}

	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(process.Pid),
	)
	if err != nil {
		return nil, fmt.Errorf("could not open the process to assign it to the Job Object: %w", err)
	}
	defer windows.CloseHandle(processHandle)
	if err = windows.AssignProcessToJobObject(job, processHandle); err != nil {
		return nil, fmt.Errorf("could not assign the process to the Job Object: %w", err)
	}
	closeJob = false
	return &windowsTerminalJob{handle: job}, nil
}

func windowsTerminalJobLimitInformation() windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
	var information windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	information.BasicLimitInformation.ActiveProcessLimit = terminalJobActiveProcessLimit
	information.JobMemoryLimit = terminalJobMemoryLimit
	return information
}

func (job *windowsTerminalJob) close() error {
	job.once.Do(func() {
		job.err = windows.CloseHandle(job.handle)
	})
	return job.err
}

func terminateUnmanagedTerminalProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(process.Pid))
	hideWindow(command)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	killErr := process.Kill()
	if killErr == nil {
		return fmt.Errorf("taskkill /T failed (%s); only the root process could be terminated: %w", output, err)
	}
	if errors.Is(killErr, os.ErrProcessDone) || errors.Is(killErr, syscall.EINVAL) {
		return fmt.Errorf("taskkill /T failed (%s); the root process already exited and its tree could not be verified: %w", output, err)
	}
	return fmt.Errorf("taskkill /T failed (%s) and the root process could not be terminated either (%v): %w", output, killErr, err)
}
