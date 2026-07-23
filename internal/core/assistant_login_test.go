package core

import "testing"

// Trimmed from a real `claude setup-token` capture under ConPTY: the URL shows
// up both as an OSC 8 hyperlink and as wrapped plain text.
const claudeSetupTokenCapture = "\x1b[2mOpening browser to sign in\x1b[0m\r\n" +
	"Browser didn't open? Use the url below to sign in (c to copy)\r\n" +
	"\x1b]8;id=u-1hpn0z1;https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a&state=WvxACo\x1b\\" +
	"https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c25\r\n0a&state=WvxACo\x1b]8;;\x1b\\\r\n" +
	"Paste code here if prompted >"

func TestAssistantLoginURLPrefersOSC8(t *testing.T) {
	url := assistantLoginURL(claudeSetupTokenCapture)
	want := "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a&state=WvxACo"
	if url != want {
		t.Fatalf("got %q, want %q", url, want)
	}
}

func TestAssistantLoginURLPlainFallback(t *testing.T) {
	raw := "Starting local login server\r\nOpen this: https://auth.openai.com/authorize?client=codex more text"
	if url := assistantLoginURL(raw); url != "https://auth.openai.com/authorize?client=codex" {
		t.Fatalf("got %q", url)
	}
	if assistantLoginURL("no url yet") != "" {
		t.Fatal("expected empty for no URL")
	}
}

func TestAssistantClaudeTokenDetection(t *testing.T) {
	out := "Success!\r\nsk-ant-oat01-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abc\r\n"
	if got := assistantClaudeToken.FindString(out); got != "sk-ant-oat01-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abc" {
		t.Fatalf("got %q", got)
	}
	// The URL contains no token; short look-alikes must not match.
	if assistantClaudeToken.FindString(claudeSetupTokenCapture) != "" {
		t.Fatal("false token match in capture")
	}
}

func TestStripAssistantANSI(t *testing.T) {
	if got := stripAssistantANSI("\x1b[31mred\x1b[0m \x1b]8;;https://x\x1b\\link\r\n"); got != "red link\n" {
		t.Fatalf("got %q", got)
	}
}
