package core

import "testing"

func TestValidateProjectName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "CON", "con.txt", "LPT9", "bad/name", "bad."} {
		if validateProjectName(name) == nil {
			t.Errorf("expected %q to be rejected", name)
		}
	}
	for _, name := range []string{"Store", "seizen-app", "Project v2"} {
		if err := validateProjectName(name); err != nil {
			t.Errorf("expected %q to be accepted: %v", name, err)
		}
	}
}

func TestGitHubRepositoryName(t *testing.T) {
	for url, expected := range map[string]string{
		"https://github.com/openai/codex.git": "codex",
		"git@github.com:openai/codex.git":     "codex",
	} {
		name, err := githubRepositoryName(url)
		if err != nil || name != expected {
			t.Errorf("%q: expected %q, got %q, %v", url, expected, name, err)
		}
	}
	for _, url := range []string{
		"https://example.com/openai/codex.git",
		"https://github.com/openai/codex.git?x=1",
		"https://github.com/openai/too/many",
	} {
		if _, err := githubRepositoryName(url); err == nil {
			t.Errorf("expected %q to be rejected", url)
		}
	}
}
