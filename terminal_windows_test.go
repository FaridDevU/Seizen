//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsTerminalCommandsUseRealShellsAndProjectDirectory(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "Project")
	cmd, err := projectTerminalCommand("cmd", projectPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.dir != projectPath {
		t.Fatalf("expected cwd %q, got %q", projectPath, cmd.dir)
	}
	if got := strings.Join(cmd.args, " "); !strings.EqualFold(got, "cmd.exe /D /Q") {
		t.Fatalf("unexpected CMD arguments %q", got)
	}

	wslPath, err := exec.LookPath("wsl.exe")
	if err != nil {
		return
	}
	wsl, err := projectTerminalCommand("wsl", projectPath, "Seizen-Ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(wsl.path, wslPath) || wsl.dir != projectPath {
		t.Fatalf("unexpected WSL command %#v", wsl)
	}
	if got := strings.Join(wsl.args, " "); !strings.Contains(got, "--distribution Seizen-Ubuntu --user root --cd "+projectPath+" --exec /bin/sh -l") {
		t.Fatalf("expected the selected WSL shell in the project directory, got %q", got)
	}
}

func TestWindowsManagedAgentCommandsStayInsideSeizen(t *testing.T) {
	base := t.TempDir()
	configBase := filepath.Join(base, "Roaming")
	localBase := filepath.Join(base, "Local")
	t.Setenv("APPDATA", configBase)
	t.Setenv("LOCALAPPDATA", localBase)
	projectPath := filepath.Join(base, "Project")

	codex, err := projectTerminalCommand("codex", projectPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Base(codex.path), "powershell.exe") || codex.dir != projectPath {
		t.Fatalf("unexpected Codex bootstrap command %#v", codex)
	}
	if got := strings.Join(codex.args, " "); !strings.Contains(got, "https://chatgpt.com/codex/install.ps1") {
		t.Fatalf("Codex bootstrap does not use the official installer: %q", got)
	}

	home := filepath.Join(localBase, "Seizen", "home")
	codexBin := filepath.Join(localBase, "Seizen", "tools", "codex", "bin")
	claudeBin := filepath.Join(home, ".local", "bin")
	codexProfile := filepath.Join(configBase, "Seizen", "profiles", "codex")
	claudeProfile := filepath.Join(configBase, "Seizen", "profiles", "claude")
	for name, want := range map[string]string{
		"HOME":              home,
		"USERPROFILE":       home,
		"CODEX_HOME":        codexProfile,
		"CLAUDE_CONFIG_DIR": claudeProfile,
	} {
		if got := environmentValue(codex.env, name); !strings.EqualFold(got, want) {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := environmentValue(codex.env, "PATH"); !strings.HasPrefix(strings.ToLower(got), strings.ToLower(codexBin+string(os.PathListSeparator)+claudeBin)) {
		t.Fatalf("managed tools are not first on PATH: %q", got)
	}

	configPath := filepath.Join(codexProfile, "config.toml")
	if contents, err := os.ReadFile(configPath); err != nil || !strings.Contains(string(contents), `cli_auth_credentials_store = "file"`) {
		t.Fatalf("Codex profile was not prepared: %q, %v", contents, err)
	}
	if err = os.WriteFile(configPath, []byte("custom = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	claude, err := projectTerminalCommand("claude", projectPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(claude.args, " "); !strings.Contains(got, "https://claude.ai/install.ps1") {
		t.Fatalf("Claude bootstrap does not use the official installer: %q", got)
	}
	if contents, err := os.ReadFile(configPath); err != nil || string(contents) != "custom = true\n" {
		t.Fatalf("existing Codex profile was overwritten: %q, %v", contents, err)
	}

	codexExecutable := filepath.Join(codexBin, "codex.exe")
	claudeExecutable := filepath.Join(claudeBin, "claude.exe")
	for _, path := range []string{codexExecutable, claudeExecutable} {
		if err = os.WriteFile(path, []byte("test"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for agent, want := range map[string]string{"codex": codexExecutable, "claude": claudeExecutable} {
		command, commandErr := projectTerminalCommand(agent, projectPath, "")
		if commandErr != nil {
			t.Fatal(commandErr)
		}
		if !strings.EqualFold(command.path, want) || command.dir != projectPath || len(command.args) != 1 {
			t.Fatalf("unexpected managed %s command %#v", agent, command)
		}
	}
}

func TestWindowsTerminalJobLimits(t *testing.T) {
	information := windowsTerminalJobLimitInformation()
	wantFlags := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY)
	if information.BasicLimitInformation.LimitFlags != wantFlags {
		t.Fatalf("unexpected Job Object flags %#x", information.BasicLimitInformation.LimitFlags)
	}
	if information.BasicLimitInformation.ActiveProcessLimit != 64 {
		t.Fatalf("unexpected active process limit %d", information.BasicLimitInformation.ActiveProcessLimit)
	}
	if information.JobMemoryLimit != 2*1024*1024*1024 {
		t.Fatalf("unexpected job memory limit %d", information.JobMemoryLimit)
	}
}

func TestWindowsCMDTerminalIsARealTTY(t *testing.T) {
	if os.Getenv("SEIZEN_CONPTY_HELPER") == "tty" {
		var inputMode, outputMode uint32
		inputErr := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &inputMode)
		outputErr := windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &outputMode)
		_, _ = fmt.Fprintf(os.Stdout, "SEIZEN_TTY_INPUT=%t OUTPUT=%t\n", inputErr == nil, outputErr == nil)
		os.Exit(0)
	}

	manager, events, output := startWindowsTerminalTest(t, "cmd-tty", "cmd", t.TempDir())
	if err := manager.write("cmd-tty", windowsTerminalHelperCommand("tty", "TestWindowsCMDTerminalIsARealTTY")+"\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalOutput(t, events, output, "SEIZEN_TTY_INPUT=true OUTPUT=true", 10*time.Second)
	if err := manager.write("cmd-tty", "set SEIZEN_CMD_STATE=STILL_RUNNING\recho SEIZEN_CMD_%SEIZEN_CMD_STATE%\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalOutput(t, events, output, "SEIZEN_CMD_STILL_RUNNING", 5*time.Second)
	if err := manager.write("cmd-tty", "exit\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalExit(t, events, output, 10*time.Second)
}

func TestWindowsCMDTerminalResizesConPTY(t *testing.T) {
	if os.Getenv("SEIZEN_CONPTY_HELPER") == "size" {
		var info windows.ConsoleScreenBufferInfo
		if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "SEIZEN_SIZE_ERROR=%v\n", err)
			os.Exit(2)
		}
		width := int(info.Window.Right-info.Window.Left) + 1
		height := int(info.Window.Bottom-info.Window.Top) + 1
		_, _ = fmt.Fprintf(os.Stdout, "SEIZEN_SIZE=%dx%d\n", width, height)
		os.Exit(0)
	}

	manager, events, output := startWindowsTerminalTest(t, "cmd-resize", "cmd", t.TempDir())
	if err := manager.resize("cmd-resize", 100, 40); err != nil {
		t.Fatal(err)
	}
	if err := manager.write("cmd-resize", windowsTerminalHelperCommand("size", "TestWindowsCMDTerminalResizesConPTY")+"\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalOutput(t, events, output, "SEIZEN_SIZE=100x40", 10*time.Second)
	if err := manager.write("cmd-resize", "exit\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalExit(t, events, output, 10*time.Second)
}

func TestWindowsCMDTerminalCtrlCReachesForegroundProcess(t *testing.T) {
	if os.Getenv("SEIZEN_CONPTY_HELPER") == "ctrl-c" {
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)
		defer signal.Stop(interrupt)
		_, _ = fmt.Fprintln(os.Stdout, "SEIZEN_CTRL_C_READY")
		select {
		case <-interrupt:
			_, _ = fmt.Fprintln(os.Stdout, "SEIZEN_CTRL_C_CAUGHT")
			os.Exit(0)
		case <-time.After(8 * time.Second):
			_, _ = fmt.Fprintln(os.Stdout, "SEIZEN_CTRL_C_TIMEOUT")
			os.Exit(2)
		}
	}

	manager, events, output := startWindowsTerminalTest(t, "cmd-ctrl-c", "cmd", t.TempDir())
	if err := manager.write("cmd-ctrl-c", windowsTerminalHelperCommand("ctrl-c", "TestWindowsCMDTerminalCtrlCReachesForegroundProcess")+"\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalOutput(t, events, output, "SEIZEN_CTRL_C_READY", 10*time.Second)
	if err := manager.writeBytes("cmd-ctrl-c", []byte{3}); err != nil {
		t.Fatal(err)
	}
	waitForTerminalOutput(t, events, output, "SEIZEN_CTRL_C_CAUGHT", 10*time.Second)
	if err := manager.write("cmd-ctrl-c", "set SEIZEN_CTRL_STATE=AFTER_CTRL_C\recho SEIZEN_%SEIZEN_CTRL_STATE%\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalOutput(t, events, output, "SEIZEN_AFTER_CTRL_C", 5*time.Second)
	if err := manager.write("cmd-ctrl-c", "exit\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalExit(t, events, output, 10*time.Second)
}

func TestWindowsWSLTerminalIsTTYAndUsesProjectDirectory(t *testing.T) {
	wslPath, err := exec.LookPath("wsl.exe")
	if err != nil {
		t.Skip("WSL is not installed")
	}
	installed, err := installedWSLRuntimeNames(context.Background())
	if err != nil {
		t.Skipf("WSL distributions cannot be queried: %v", err)
	}
	runtimeName := ""
	for _, definition := range wslDistributionDefinitions {
		if _, ok := installed[strings.ToLower(definition.RuntimeName)]; ok {
			runtimeName = definition.RuntimeName
			break
		}
	}
	if runtimeName == "" {
		t.Skip("WSL has no managed Seizen distribution")
	}
	projectPath := t.TempDir()
	probeContext, cancelProbe := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelProbe()
	probe := exec.CommandContext(probeContext, wslPath, "--distribution", runtimeName, "--cd", projectPath, "--exec", "sh", "-c", "pwd")
	hideWindow(probe)
	expectedOutput, err := probe.Output()
	if err != nil {
		t.Skipf("WSL has no usable distribution: %v", err)
	}
	expectedDirectory := strings.TrimSpace(string(expectedOutput))
	if expectedDirectory == "" {
		t.Skip("WSL did not resolve the project directory")
	}

	manager, events, output := startWindowsTerminalTest(t, "wsl-tty", "wsl", projectPath, runtimeName)
	command := `sh -c 'test -t 0 && test -t 1 && printf "SEIZEN_WSL_%s\n" TTY_OK'`
	if err := manager.write("wsl-tty", command+"\r"); err != nil {
		t.Fatal(err)
	}
	if err := manager.write("wsl-tty", "pwd\r"); err != nil {
		t.Fatal(err)
	}
	if err := manager.write("wsl-tty", "exit\r"); err != nil {
		t.Fatal(err)
	}
	waitForTerminalExit(t, events, output, 30*time.Second)
	got := output.String()
	if !strings.Contains(got, "SEIZEN_WSL_TTY_OK") {
		t.Fatalf("WSL did not receive a real TTY: %q", got)
	}
	if !strings.Contains(got, expectedDirectory) {
		t.Fatalf("WSL terminal cwd mismatch: expected %q in %q", expectedDirectory, got)
	}
}

func TestWindowsTerminalJobKillsChildTree(t *testing.T) {
	switch os.Getenv("SEIZEN_JOB_HELPER") {
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestWindowsTerminalJobKillsChildTree$")
		child.Env = append(os.Environ(), "SEIZEN_JOB_HELPER=child")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprintf(os.Stdout, "SEIZEN_CHILD_PID=%d\n", child.Process.Pid)
		_ = child.Wait()
		os.Exit(0)
	case "child":
		for {
			time.Sleep(time.Second)
		}
	}

	events := make(chan terminalTestEvent, 64)
	manager := newTerminalManager(func(name string, payload any) {
		events <- terminalTestEvent{name: name, payload: payload}
	})
	command := &terminalCommand{
		path: os.Args[0],
		args: []string{os.Args[0], "-test.run=^TestWindowsTerminalJobKillsChildTree$"},
		env:  append(os.Environ(), "SEIZEN_JOB_HELPER=parent"),
	}
	if err := manager.start("tree", command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.stopAll)

	var output strings.Builder
	var childPID int
	deadline := time.After(10 * time.Second)
	for childPID == 0 {
		select {
		case event := <-events:
			if event.name == terminalOutputEvent {
				output.WriteString(event.payload.(TerminalOutputEvent).Data)
				childPID = terminalMarkerPID(output.String(), "SEIZEN_CHILD_PID=")
			}
		case <-deadline:
			t.Fatalf("timed out waiting for child PID; output=%q", output.String())
		}
	}
	if err := manager.stop("tree"); err != nil {
		t.Fatal(err)
	}

	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childPID))
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, 2000)
	if err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("child process %d survived Job Object close: wait=%#x, %v", childPID, result, err)
	}
}

func startWindowsTerminalTest(t *testing.T, id, shell, projectPath string, runtimes ...string) (*terminalManager, <-chan terminalTestEvent, *strings.Builder) {
	t.Helper()
	events := make(chan terminalTestEvent, 256)
	manager := newTerminalManager(func(name string, payload any) {
		events <- terminalTestEvent{name: name, payload: payload}
	})
	wslRuntime := ""
	if shell == "wsl" {
		wslRuntime = "Seizen-Ubuntu"
		if len(runtimes) > 0 {
			wslRuntime = runtimes[0]
		}
	}
	command, err := projectTerminalCommand(shell, projectPath, wslRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.start(id, command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.stopAll)
	return manager, events, &strings.Builder{}
}

func windowsTerminalHelperCommand(mode, testName string) string {
	return fmt.Sprintf(`set "SEIZEN_CONPTY_HELPER=%s"&"%s" -test.run=^%s$`, mode, os.Args[0], testName)
}

func waitForTerminalOutput(t *testing.T, events <-chan terminalTestEvent, output *strings.Builder, marker string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for !strings.Contains(output.String(), marker) {
		select {
		case event := <-events:
			switch event.name {
			case terminalOutputEvent:
				output.WriteString(event.payload.(TerminalOutputEvent).Data)
			case terminalExitEvent:
				exit := event.payload.(TerminalExitEvent)
				t.Fatalf("terminal exited before %q: %s; output=%q", marker, exit.Error, output.String())
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q; output=%q", marker, output.String())
		}
	}
}

func waitForTerminalExit(t *testing.T, events <-chan terminalTestEvent, output *strings.Builder, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-events:
			switch event.name {
			case terminalOutputEvent:
				output.WriteString(event.payload.(TerminalOutputEvent).Data)
			case terminalExitEvent:
				exit := event.payload.(TerminalExitEvent)
				if exit.Error != "" {
					t.Fatalf("terminal exited with an error: %s; output=%q", exit.Error, output.String())
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for terminal exit; output=%q", output.String())
		}
	}
}

func terminalMarkerPID(output, marker string) int {
	index := strings.Index(output, marker)
	if index < 0 {
		return 0
	}
	start := index + len(marker)
	end := start
	for end < len(output) && output[end] >= '0' && output[end] <= '9' {
		end++
	}
	if end == start {
		return 0
	}
	pid, _ := strconv.Atoi(output[start:end])
	return pid
}
