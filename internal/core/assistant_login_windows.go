//go:build windows

package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/conpty"
)

// One sign-in at a time; starting a new one cancels the previous.
var assistantLoginState struct {
	mu     sync.Mutex
	proc   *os.Process
	claude *claudeLoginRun // nil for codex sign-ins
}

const assistantLoginTimeout = 10 * time.Minute

func startAssistantLogin(a *App, agent string) error {
	claudeExe, codexExe, home, codexBin, claudeBin, codexProfile, claudeProfile, err := assistantAgentPaths()
	if err != nil {
		return err
	}
	_ = cancelAssistantLogin()
	env := managedAgentEnvironment(os.Environ(), home, codexBin, claudeBin, codexProfile, claudeProfile)
	if agent == "codex" {
		if !fileExists(codexExe) {
			return errors.New("Codex is not installed yet: open a Codex terminal in any project once to set it up")
		}
		return startCodexLogin(a, codexExe, home, env)
	}
	if !fileExists(claudeExe) {
		return errors.New("Claude Code is not installed yet: open a Claude terminal in any project once to set it up")
	}
	if claudeProfileSignedIn(claudeProfile) {
		// An earlier attempt already finished in the browser: nothing to
		// drive, just confirm so the modal never waits for a code.
		a.emitAssistantLogin(AssistantLoginEvent{Provider: "claude-cli", Stage: "done"})
		return nil
	}
	return startClaudeLogin(a, claudeExe, home, env, claudeProfile)
}

// assistantLoginWriter accumulates CLI output and fires notify on every chunk.
type assistantLoginWriter struct {
	mu     sync.Mutex
	all    strings.Builder
	notify func(all string)
}

func (w *assistantLoginWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.all.Write(p)
	all := w.all.String()
	w.mu.Unlock()
	w.notify(all)
	return len(p), nil
}

func (w *assistantLoginWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.all.String()
}

// startCodexLogin runs `codex login` hidden: it opens the user's browser and
// exits by itself once the localhost callback lands.
func startCodexLogin(a *App, exe, dir string, env []string) error {
	urlSent := false
	var urlMu sync.Mutex
	output := &assistantLoginWriter{}
	output.notify = func(all string) {
		urlMu.Lock()
		defer urlMu.Unlock()
		if urlSent {
			return
		}
		if url := assistantLoginURL(all); url != "" {
			urlSent = true
			a.emitAssistantLogin(AssistantLoginEvent{Provider: "codex-cli", Stage: "browser", URL: url})
		}
	}
	cmd := exec.Command(exe, "login")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start the Codex sign-in: %w", err)
	}
	setAssistantLoginRun(nil, cmd.Process)
	go func() {
		defer clearAssistantLoginRun(cmd.Process)
		err := waitWithTimeout(cmd.Process, func() error { return cmd.Wait() })
		if err != nil {
			a.emitAssistantLogin(AssistantLoginEvent{
				Provider: "codex-cli", Stage: "error",
				Message: assistantLoginFailure("the Codex sign-in did not finish", output.String()),
			})
			return
		}
		a.emitAssistantLogin(AssistantLoginEvent{Provider: "codex-cli", Stage: "done"})
	}()
	return nil
}

// claudeLoginRun is one hidden `claude setup-token` session. The pump owns the
// buffer; submits record their offset so rejections are scoped per attempt.
type claudeLoginRun struct {
	pty  *conpty.ConPty
	proc *os.Process

	mu           sync.Mutex
	all          strings.Builder
	submitOffset int  // buffer length when the last code was written
	handled      bool // this attempt's rejection was already reported
}

// startClaudeLogin drives `claude setup-token` on a hidden pseudo console: the
// TUI never shows anywhere, we lift the OAuth URL from its output, and the code
// the user pastes in the modal is written back to it. The long-lived token it
// prints is stored so headless runs use the subscription.
func startClaudeLogin(a *App, exe, dir string, env []string, profile string) error {
	// 400 columns so the URL never line-wraps in the pseudo terminal.
	pty, err := conpty.New(400, 40, 0)
	if err != nil {
		return fmt.Errorf("could not open a hidden console: %w", err)
	}
	attr := &syscall.ProcAttr{Dir: dir, Env: env}
	pid, _, err := pty.Spawn(exe, []string{exe, "setup-token"}, attr)
	if err != nil {
		pty.Close()
		return fmt.Errorf("could not start the Claude sign-in: %w", err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		pty.Close()
		return err
	}
	run := &claudeLoginRun{pty: pty, proc: proc}
	setAssistantLoginRun(run, proc)
	go claudeLoginPump(a, run, profile)
	return nil
}

func claudeLoginPump(a *App, run *claudeLoginRun, profile string) {
	defer clearAssistantLoginRun(run.proc)
	timeout := time.AfterFunc(assistantLoginTimeout, func() { _ = run.proc.Kill() })
	defer timeout.Stop()

	urlSent := false
	token := ""
	succeeded := false
	buf := make([]byte, 4096)
	for {
		n, err := run.pty.Read(buf)
		if n > 0 {
			run.mu.Lock()
			run.all.Write(buf[:n])
			all := run.all.String()
			offset := run.submitOffset
			handled := run.handled
			run.mu.Unlock()

			if !urlSent {
				if url := assistantLoginURL(all); url != "" {
					urlSent = true
					a.emitAssistantLogin(AssistantLoginEvent{
						Provider: "claude-cli", Stage: "browser", URL: url, NeedsCode: true,
					})
				}
			}
			// The token only prints after a valid code: that's success.
			if token = assistantClaudeToken.FindString(all); token != "" {
				break
			}
			if offset > 0 && offset <= len(all) {
				attempt := stripAssistantANSI(all[offset:])
				// Some versions confirm without printing the token.
				if strings.Contains(strings.ToLower(attempt), "success") {
					succeeded = true
					break
				}
				// A bad code parks the TUI on "Press Enter to retry" forever:
				// reset it to the paste prompt and tell the modal.
				if !handled && (strings.Contains(attempt, "OAuth error") ||
					strings.Contains(attempt, "Press Enter to retry")) {
					run.mu.Lock()
					run.handled = true
					run.mu.Unlock()
					_, _ = run.pty.Write([]byte("\r"))
					a.emitAssistantLogin(AssistantLoginEvent{
						Provider: "claude-cli", Stage: "browser", NeedsCode: true,
						Message: "code-rejected",
					})
				}
			}
		}
		if err != nil {
			break
		}
	}
	// A success without a printed token persists credentials instead; give the
	// CLI a moment to write them before tearing the console down.
	if token == "" && succeeded {
		for wait := 0; wait < 20 && !claudeProfileSignedIn(profile); wait++ {
			time.Sleep(250 * time.Millisecond)
		}
		run.mu.Lock()
		token = assistantClaudeToken.FindString(run.all.String())
		run.mu.Unlock()
	}
	_ = run.proc.Kill()
	run.pty.Close()

	if token == "" && !claudeProfileSignedIn(profile) {
		run.mu.Lock()
		output := run.all.String()
		run.mu.Unlock()
		a.emitAssistantLogin(AssistantLoginEvent{
			Provider: "claude-cli", Stage: "error",
			Message: assistantLoginFailure("the Claude sign-in did not finish", output),
		})
		return
	}
	if token != "" {
		if err := a.saveClaudeOAuthToken(token); err != nil {
			a.emitAssistantLogin(AssistantLoginEvent{
				Provider: "claude-cli", Stage: "error",
				Message: "could not save the sign-in: " + err.Error(),
			})
			return
		}
	}
	a.emitAssistantLogin(AssistantLoginEvent{Provider: "claude-cli", Stage: "done"})
}

// assistantLoginFailure trims hidden-terminal noise into a short human message.
func assistantLoginFailure(prefix, raw string) string {
	detail := strings.TrimSpace(stripAssistantANSI(raw))
	if len(detail) > 200 {
		detail = "…" + detail[len(detail)-200:]
	}
	if detail == "" {
		return prefix
	}
	return prefix + ": " + detail
}

func waitWithTimeout(proc *os.Process, wait func() error) error {
	timeout := time.AfterFunc(assistantLoginTimeout, func() { _ = proc.Kill() })
	defer timeout.Stop()
	return wait()
}

func setAssistantLoginRun(run *claudeLoginRun, proc *os.Process) {
	assistantLoginState.mu.Lock()
	defer assistantLoginState.mu.Unlock()
	assistantLoginState.claude = run
	assistantLoginState.proc = proc
}

// clearAssistantLoginRun forgets the run, unless a newer one already replaced it.
func clearAssistantLoginRun(proc *os.Process) {
	assistantLoginState.mu.Lock()
	defer assistantLoginState.mu.Unlock()
	if assistantLoginState.proc == proc {
		assistantLoginState.claude = nil
		assistantLoginState.proc = nil
	}
}

func submitAssistantLoginCode(code string) error {
	assistantLoginState.mu.Lock()
	run := assistantLoginState.claude
	assistantLoginState.mu.Unlock()
	if run == nil {
		return errors.New("no sign-in is waiting for a code")
	}
	run.mu.Lock()
	run.submitOffset = run.all.Len()
	run.handled = false
	run.mu.Unlock()
	_, err := run.pty.Write([]byte(code + "\r"))
	return err
}

func cancelAssistantLogin() error {
	assistantLoginState.mu.Lock()
	defer assistantLoginState.mu.Unlock()
	if assistantLoginState.proc != nil {
		_ = assistantLoginState.proc.Kill()
	}
	if assistantLoginState.claude != nil {
		_ = assistantLoginState.claude.pty.Close()
	}
	assistantLoginState.proc = nil
	assistantLoginState.claude = nil
	return nil
}
