package core

// In-app sign-in for the subscription providers: the real CLI runs hidden (no
// visible terminal) and progress streams to the Settings modal as
// "assistant:login" events. The user only ever sees the elegant in-app window
// and their browser.

import (
	"errors"
	"regexp"
	"strings"
)

// AssistantLoginEvent stages: "browser" (URL ready, continue there), "done", "error".
type AssistantLoginEvent struct {
	Provider  string `json:"provider"`
	Stage     string `json:"stage"`
	URL       string `json:"url,omitempty"`
	NeedsCode bool   `json:"needsCode,omitempty"`
	Message   string `json:"message,omitempty"`
}

var (
	assistantANSISequences = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\r`)
	// OSC 8 hyperlinks carry the sign-in URL unwrapped, before the terminal folds it.
	assistantOSC8URL     = regexp.MustCompile(`\x1b\]8;[^;]*;(https://[^\x1b\x07]+)`)
	assistantPlainURL    = regexp.MustCompile(`https://[^\s"'<>\x60\x1b\x07]+`)
	assistantClaudeToken = regexp.MustCompile(`sk-ant-oat[0-9A-Za-z]*-[A-Za-z0-9_-]{20,}`)
)

func stripAssistantANSI(raw string) string {
	return assistantANSISequences.ReplaceAllString(raw, "")
}

// assistantLoginURL pulls the sign-in URL out of raw terminal output.
func assistantLoginURL(raw string) string {
	if match := assistantOSC8URL.FindStringSubmatch(raw); match != nil {
		return match[1]
	}
	return assistantPlainURL.FindString(stripAssistantANSI(raw))
}

func (a *App) emitAssistantLogin(event AssistantLoginEvent) {
	if a.emitEvent == nil {
		return
	}
	a.emitEvent(a.context(), "assistant:login", event)
}

// StartAssistantLogin begins a hidden sign-in for a subscription provider.
// Progress arrives as assistant:login events; nothing visible is spawned.
func (a *App) StartAssistantLogin(provider string) error {
	if provider != "claude-cli" && provider != "codex-cli" {
		return errors.New("only subscription providers need a login")
	}
	return startAssistantLogin(a, strings.TrimSuffix(provider, "-cli"))
}

// SubmitAssistantLoginCode forwards the code pasted in the modal to the hidden CLI.
func (a *App) SubmitAssistantLoginCode(code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("the code is empty")
	}
	return submitAssistantLoginCode(code)
}

func (a *App) CancelAssistantLogin() error {
	return cancelAssistantLogin()
}
