package core

import (
	"strings"
	"testing"
)

func TestParseClaudeHeadlessResult(t *testing.T) {
	output := `{"type":"system","subtype":"init","session_id":"s1"}
{"type":"assistant","message":{"content":[{"type":"text","text":"thinking"}]}}
{"type":"result","result":"{\"text\":\"done\",\"actions\":[]}","is_error":false,"session_id":"s1"}`
	text, session, err := parseClaudeHeadlessResult(output)
	if err != nil || !strings.Contains(text, "done") {
		t.Fatalf("want done, got %q err %v", text, err)
	}
	if session != "s1" {
		t.Fatalf("want session s1, got %q", session)
	}
	if _, _, err := parseClaudeHeadlessResult(`{"type":"result","result":"boom","is_error":true}`); err == nil {
		t.Error("is_error should fail")
	}
	if _, _, err := parseClaudeHeadlessResult("not json"); err == nil {
		t.Error("missing result should fail")
	}
}

func TestParseCodexHeadlessResult(t *testing.T) {
	output := `{"type":"thread.started","thread_id":"t1"}
{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"{\"text\":\"hola\"}"}}
{"type":"turn.completed","usage":{}}`
	text, thread, err := parseCodexHeadlessResult(output)
	if err != nil || !strings.Contains(text, "hola") {
		t.Fatalf("want hola, got %q err %v", text, err)
	}
	if thread != "t1" {
		t.Fatalf("want thread t1, got %q", thread)
	}
	if _, _, err := parseCodexHeadlessResult(`{"type":"turn.failed","error":{"message":"nope"}}`); err == nil {
		t.Error("turn.failed should fail")
	}
}

func TestParseAssistantJSONReply(t *testing.T) {
	reply, err := parseAssistantJSONReply("```json\n{\"text\":\"listo\",\"actions\":[{\"name\":\"open_section\",\"input\":{\"section\":\"folders\"}}]}\n```")
	if err != nil || reply.Text != "listo" || len(reply.Actions) != 1 || reply.Actions[0].Name != "open_section" {
		t.Fatalf("fenced JSON not parsed: %+v err %v", reply, err)
	}
	plain, err := parseAssistantJSONReply("just words, no JSON")
	if err != nil || plain.Text != "just words, no JSON" || len(plain.Actions) != 0 {
		t.Fatalf("plain text should become a no-action reply: %+v", plain)
	}
}

func TestParseModelCaches(t *testing.T) {
	claude := `{"additionalModelOptionsCache":[
		{"value":"fable [1m]","label":"Fable"},
		{"value":"nope","label":"Off","disabled":true}]}`
	models := parseClaudeModelCache(claude)
	if len(models) != 1 || models[0].ID != "fable" || models[0].Name != "Fable" {
		t.Fatalf("claude cache: %+v", models)
	}

	codex := `{"models":[
		{"slug":"gpt-5-codex","display_name":"GPT-5 Codex","visibility":"list","priority":2},
		{"slug":"codex-auto-review","visibility":"hide","priority":0},
		{"slug":"gpt-5","display_name":"GPT-5","visibility":"list","priority":1}]}`
	options := parseCodexModelCache(codex)
	if len(options) != 2 || options[0].ID != "gpt-5" || options[1].ID != "gpt-5-codex" {
		t.Fatalf("codex cache order/filter: %+v", options)
	}
}
