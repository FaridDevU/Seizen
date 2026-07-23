package core

import (
	"strings"
	"testing"
)

func TestAssistantSystemPrompt(t *testing.T) {
	prompt := assistantSystemPrompt([]Project{{Name: "Pagina Papa"}, {Name: "Seizen"}})
	for _, want := range []string{"- Pagina Papa\n", "- Seizen\n", "open_project"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if !strings.Contains(assistantSystemPrompt(nil), "(none yet)") {
		t.Error("empty project list should say (none yet)")
	}
}

func TestAssistantViewMasksKeys(t *testing.T) {
	config := assistantStoredConfig{
		Keys:        []assistantStoredKey{{ID: "a", Label: "Work", Key: "sk-ant-api03-secretsecret1234"}},
		ActiveKeyID: "a",
	}
	view := assistantView(config)
	if view.Model != assistantDefaultModel {
		t.Errorf("empty model should default, got %q", view.Model)
	}
	masked := view.Keys[0].Masked
	if strings.Contains(masked, "secretsecret") {
		t.Errorf("mask leaks the key: %q", masked)
	}
	if !strings.HasSuffix(masked, "1234") || !view.Keys[0].Active {
		t.Errorf("mask should keep the tail and mark active: %+v", view.Keys[0])
	}
	if config.activeKey() != "sk-ant-api03-secretsecret1234" {
		t.Error("activeKey should return the stored key")
	}
}
