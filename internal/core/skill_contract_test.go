package core

import (
	"os"
	"strings"
	"testing"
)

func TestSeizenExperimentGuardSkillContract(t *testing.T) {
	contents, err := os.ReadFile("../../skills/seizen-experiments/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.HasPrefix(text, "---\nname: seizen-experiments\ndescription:") || strings.Count(text, "\n---\n") != 1 {
		t.Fatalf("skill frontmatter is not minimal and valid: %q", text)
	}
	for _, tool := range []string{
		"seizen_experiment_analyze_change", "seizen_experiment_suggest", "seizen_experiment_create",
		"seizen_experiment_checkpoint", "seizen_experiment_prepare_integration", "seizen_experiment_integrate",
	} {
		if !strings.Contains(text, tool) {
			t.Fatalf("skill does not reference %s", tool)
		}
	}
	metadata, err := os.ReadFile("../../skills/seizen-experiments/agents/openai.yaml")
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(metadata)
	for _, expected := range []string{
		`display_name: "Seizen Experiment Guard"`,
		`short_description: "Detects risky changes before editing"`,
		`default_prompt: "Use $seizen-experiments`,
	} {
		if !strings.Contains(yaml, expected) {
			t.Fatalf("skill metadata is missing %q: %s", expected, yaml)
		}
	}
}
