package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeDiscoveryFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAppDiscoveryFindsViteWithoutPersistingIt(t *testing.T) {
	app, project := newAppServerTestApp(t)
	writeDiscoveryFile(t, project.Path, "package.json", `{
  "name": "web-main",
  "scripts": {"dev": "vite --port 5180", "test": "vitest run"},
  "devDependencies": {"vite": "latest"}
}`)
	writeDiscoveryFile(t, project.Path, "vite.config.ts", `export default {}`)

	events := make([]string, 0, 2)
	service := newAppDiscoveryService(app.database, func(name string, _ any) { events = append(events, name) })
	candidates, err := service.Discover(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %+v", candidates)
	}
	candidate := candidates[0]
	if candidate.Framework != "Vite" || candidate.Kind != "web" || candidate.StartCommand != "npm run dev" ||
		len(candidate.ExpectedPorts) != 1 || candidate.ExpectedPorts[0] != 5180 ||
		candidate.SuggestedHealthcheck != "http://localhost:5180" || candidate.Confidence < .9 {
		t.Fatalf("unexpected Vite candidate: %+v", candidate)
	}
	if len(events) != 2 || events[0] != "app.discovery.started" || events[1] != "app.discovery.completed" {
		t.Fatalf("unexpected discovery events: %v", events)
	}
	apps, err := app.ListApps(project.ID)
	if err != nil || len(apps) != 0 {
		t.Fatalf("discovery persisted an App: %+v, %v", apps, err)
	}
}

func TestAppDiscoveryKeepsMonorepoAppsSeparate(t *testing.T) {
	app, project := newAppServerTestApp(t)
	writeDiscoveryFile(t, project.Path, "apps/web/package.json", `{
  "name":"web-main", "scripts":{"dev":"vite"}, "dependencies":{"vite":"latest"}
}`)
	writeDiscoveryFile(t, project.Path, "apps/admin/package.json", `{
  "name":"admin-panel", "scripts":{"dev":"next dev"}, "dependencies":{"next":"latest"}
}`)
	writeDiscoveryFile(t, project.Path, "services/api/package.json", `{
  "name":"api", "scripts":{"start":"node server.js --port 4100"}, "dependencies":{"express":"latest"}
}`)

	candidates, err := app.DiscoverApps(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected three separate candidates, got %+v", candidates)
	}
	byName := map[string]AppCandidate{}
	for _, candidate := range candidates {
		byName[candidate.Name] = candidate
	}
	if byName["web-main"].Framework != "Vite" || byName["admin-panel"].Framework != "Next.js" ||
		byName["api"].Framework != "Node.js" || byName["api"].ExpectedPorts[0] != 4100 {
		t.Fatalf("monorepo candidates were mixed: %+v", candidates)
	}
	if samePath(byName["web-main"].WorkingDirectory, byName["api"].WorkingDirectory) {
		t.Fatal("separate packages share a working directory")
	}
}

func TestAppDiscoveryReturnsEmptyForUnknownProjectContents(t *testing.T) {
	app, project := newAppServerTestApp(t)
	writeDiscoveryFile(t, project.Path, "notes.txt", "nothing executable here")
	candidates, err := app.DiscoverApps(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no candidate, got %+v", candidates)
	}
	if _, err = app.DiscoverApps("another-project"); err == nil {
		t.Fatal("expected an unknown project to be rejected")
	}
}

func TestAppDiscoveryRecognizesSupportedManifestFamilies(t *testing.T) {
	app, project := newAppServerTestApp(t)
	writeDiscoveryFile(t, project.Path, "django/manage.py", "#!/usr/bin/env python")
	writeDiscoveryFile(t, project.Path, "python/pyproject.toml", "[project]\ndependencies = ['fastapi', 'uvicorn']")
	writeDiscoveryFile(t, project.Path, "rust/Cargo.toml", "[package]\nname = \"worker\"\n[dependencies]\naxum = \"1\"")
	writeDiscoveryFile(t, project.Path, "go/go.mod", "module example.com/api\nrequire github.com/gin-gonic/gin v1.0.0")
	writeDiscoveryFile(t, project.Path, "containers/compose.yaml", "services:\n  web:\n    ports:\n      - \"8088:80\"")
	writeDiscoveryFile(t, project.Path, "image/Dockerfile", "FROM scratch")
	writeDiscoveryFile(t, project.Path, ".devcontainer/devcontainer.json", `{ "name": "dev" }`)

	candidates, err := app.DiscoverApps(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	frameworks := map[string]bool{}
	for _, candidate := range candidates {
		frameworks[candidate.Framework] = true
	}
	for _, framework := range []string{"Django", "FastAPI", "Rust web", "Go web", "Docker Compose", "Docker"} {
		if !frameworks[framework] {
			t.Fatalf("missing %s candidate in %+v", framework, candidates)
		}
	}
}

func TestAppDiscoveryReadsREADMEOnlyAsFallback(t *testing.T) {
	app, project := newAppServerTestApp(t)
	writeDiscoveryFile(t, project.Path, "README.md", "Development\n\nnpm run dev -- --port 4321\n")
	candidates, err := app.DiscoverApps(project.ID)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("expected README candidate, got %+v, %v", candidates, err)
	}
	if candidates[0].StartCommand != "npm run dev -- --port 4321" || candidates[0].ExpectedPorts[0] != 4321 {
		t.Fatalf("unexpected README candidate: %+v", candidates[0])
	}
}
