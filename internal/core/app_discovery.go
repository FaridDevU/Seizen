package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type AppCandidate struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Kind                 string   `json:"kind"`
	WorkingDirectory     string   `json:"workingDirectory"`
	StartCommand         string   `json:"startCommand"`
	TestCommand          string   `json:"testCommand"`
	Executable           string   `json:"executable"`
	ExpectedPorts        []int    `json:"expectedPorts"`
	SuggestedHealthcheck string   `json:"suggestedHealthcheck"`
	Framework            string   `json:"framework"`
	Confidence           float64  `json:"confidence"`
	Reason               string   `json:"reason"`
	SourceFiles          []string `json:"sourceFiles"`
}

type AppDiscoveryService struct {
	database *Database
	emit     func(string, any)
}

type discoveryDirectory struct {
	path    string
	files   map[string]string
	sources map[string]string
}

type packageManifest struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

var (
	discoveryPortPattern   = regexp.MustCompile(`(?i)(?:--port(?:=|\s+)|\bPORT\s*=\s*|localhost:|127\.0\.0\.1:)(\d{2,5})`)
	composePortPattern     = regexp.MustCompile(`(?m)["']?(\d{2,5}):\d{2,5}["']?`)
	pythonScriptPattern    = regexp.MustCompile(`(?ms)^\[project\.scripts\]\s+([A-Za-z0-9_.-]+)\s*=`)
	rustPackageNamePattern = regexp.MustCompile(`(?m)^name\s*=\s*["']([^"']+)["']`)
	readmeCommandPattern   = regexp.MustCompile(`(?im)^\s*(?:\$\s*)?((?:npm|pnpm|yarn)\s+(?:run\s+)?(?:dev|start|serve)\b[^\r\n]*)`)
)

func newAppDiscoveryService(database *Database, emit func(string, any)) *AppDiscoveryService {
	return &AppDiscoveryService{database: database, emit: emit}
}

func (a *App) DiscoverApps(projectID string) ([]AppCandidate, error) {
	service := newAppDiscoveryService(a.database, func(name string, payload any) {
		a.emitAgentEvent(name, payload)
	})
	return service.Discover(a.context(), projectID)
}

func (service *AppDiscoveryService) Discover(ctx context.Context, projectID string) ([]AppCandidate, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("project is required")
	}
	db, err := service.database.Pool(ctx)
	if err != nil {
		return nil, err
	}
	var projectPath, projectName string
	if err = db.QueryRowContext(ctx, `SELECT path, name FROM projects WHERE id = ?`, projectID).Scan(&projectPath, &projectName); err != nil {
		return nil, errors.New("the project was not found")
	}
	root, err := existingDirectory(projectPath)
	if err != nil {
		return nil, fmt.Errorf("could not inspect the project: %w", err)
	}
	service.emitEvent("app.discovery.started", map[string]any{"projectId": projectID})
	directories, err := collectDiscoveryFiles(root)
	if err != nil {
		return nil, fmt.Errorf("could not analyze the project: %w", err)
	}
	candidates := make([]AppCandidate, 0)
	for _, directory := range directories {
		candidates = append(candidates, candidatesFromDirectory(root, projectName, directory)...)
	}
	candidates = normalizeCandidates(root, candidates)
	service.emitEvent("app.discovery.completed", map[string]any{
		"projectId":  projectID,
		"count":      len(candidates),
		"candidates": candidates,
	})
	return candidates, nil
}

func (service *AppDiscoveryService) emitEvent(name string, payload any) {
	if service.emit != nil {
		service.emit(name, payload)
	}
}

func collectDiscoveryFiles(root string) ([]discoveryDirectory, error) {
	byDirectory := map[string]*discoveryDirectory{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && shouldSkipDiscoveryDirectory(entry.Name(), relative) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !isDiscoveryFile(entry.Name(), relative) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 512*1024 {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		directoryPath := filepath.Dir(path)
		directory := byDirectory[directoryPath]
		if directory == nil {
			directory = &discoveryDirectory{path: directoryPath, files: map[string]string{}, sources: map[string]string{}}
			byDirectory[directoryPath] = directory
		}
		name := strings.ToLower(filepath.Base(path))
		if name == "devcontainer.json" && filepath.Base(directoryPath) == ".devcontainer" {
			directoryPath = filepath.Dir(directoryPath)
			directory = byDirectory[directoryPath]
			if directory == nil {
				directory = &discoveryDirectory{path: directoryPath, files: map[string]string{}, sources: map[string]string{}}
				byDirectory[directoryPath] = directory
			}
		}
		directory.files[name] = string(contents)
		directory.sources[name] = filepath.ToSlash(relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	directories := make([]discoveryDirectory, 0, len(byDirectory))
	for _, directory := range byDirectory {
		directories = append(directories, *directory)
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].path < directories[j].path })
	return directories, nil
}

func shouldSkipDiscoveryDirectory(name, relative string) bool {
	switch strings.ToLower(name) {
	case ".git", ".idea", ".vscode", "node_modules", "vendor", "target", "dist", "build", "coverage", ".next", ".venv", "venv", "__pycache__":
		return true
	}
	return strings.Count(relative, string(filepath.Separator)) >= 6
}

func isDiscoveryFile(name, relative string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "package.json", "angular.json", "pyproject.toml", "requirements.txt", "manage.py",
		"cargo.toml", "go.mod", "dockerfile", "compose.yaml", "compose.yml",
		"docker-compose.yml", "docker-compose.yaml", "devcontainer.json", "readme", "readme.md", "readme.txt":
		return true
	}
	return strings.HasPrefix(lower, "vite.config.") || strings.HasPrefix(lower, "next.config.") ||
		(strings.EqualFold(filepath.Base(filepath.Dir(relative)), ".devcontainer") && lower == "devcontainer.json")
}

func candidatesFromDirectory(root, projectName string, directory discoveryDirectory) []AppCandidate {
	candidates := make([]AppCandidate, 0, 2)
	_, hasPackage := directory.files["package.json"]
	if raw, ok := directory.files["package.json"]; ok {
		if candidate, valid := candidateFromPackage(root, projectName, directory, raw); valid {
			candidates = append(candidates, candidate)
		}
	}
	if !hasPackage {
		switch {
		case hasFilePrefix(directory.files, "vite.config."):
			candidates = append(candidates, newCandidate(root, projectName, directory, "Vite", "web", "npm run dev", "", "", []int{5173}, .65, "a Vite configuration was detected; package.json still needs review"))
		case hasFilePrefix(directory.files, "next.config."):
			candidates = append(candidates, newCandidate(root, projectName, directory, "Next.js", "web", "npm run dev", "", "", []int{3000}, .65, "a Next.js configuration was detected; package.json still needs review"))
		case directory.files["angular.json"] != "":
			candidates = append(candidates, newCandidate(root, projectName, directory, "Angular", "web", "npx ng serve", "", "", []int{4200}, .65, "angular.json identifies an Angular workspace"))
		}
	}
	if _, ok := directory.files["manage.py"]; ok {
		candidates = append(candidates, newCandidate(root, projectName, directory, "Django", "web", "python manage.py runserver", "python manage.py test", "", []int{8000}, .98, "manage.py identifies a Django application"))
	} else if raw := directory.files["pyproject.toml"] + "\n" + directory.files["requirements.txt"]; strings.TrimSpace(raw) != "" {
		if candidate, valid := candidateFromPython(root, projectName, directory, raw); valid {
			candidates = append(candidates, candidate)
		}
	}
	if raw, ok := directory.files["cargo.toml"]; ok {
		candidates = append(candidates, candidateFromCargo(root, projectName, directory, raw))
	}
	if raw, ok := directory.files["go.mod"]; ok {
		candidates = append(candidates, candidateFromGo(root, projectName, directory, raw))
	}
	if len(candidates) == 0 {
		if raw := firstFile(directory.files, "compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"); raw != "" {
			ports := portsFromText(raw)
			candidates = append(candidates, newCandidate(root, projectName, directory, "Docker Compose", "web", "", "", "", ports, .40, "A compose file was detected; review its command before mounting it"))
		} else if _, ok := directory.files["dockerfile"]; ok {
			candidates = append(candidates, newCandidate(root, projectName, directory, "Docker", "desktop", "", "", "", nil, .30, "A Dockerfile was detected, but it is not safe to infer how to run it"))
		} else if raw := firstFile(directory.files, "readme.md", "readme", "readme.txt"); raw != "" {
			if command := readmeStartCommand(raw); command != "" {
				candidates = append(candidates, newCandidate(root, projectName, directory, "Project", "web", command, "", "", portsFromText(raw), .45, "The README documents a development command"))
			}
		}
	}
	return candidates
}

func candidateFromPackage(root, projectName string, directory discoveryDirectory, raw string) (AppCandidate, bool) {
	var manifest packageManifest
	if json.Unmarshal([]byte(raw), &manifest) != nil {
		return AppCandidate{}, false
	}
	dependencies := map[string]string{}
	for name, version := range manifest.Dependencies {
		dependencies[strings.ToLower(name)] = version
	}
	for name, version := range manifest.DevDependencies {
		dependencies[strings.ToLower(name)] = version
	}
	scriptText := ""
	for _, script := range manifest.Scripts {
		scriptText += " " + strings.ToLower(script)
	}
	framework, kind, port, confidence := "Node.js", "web", 0, .65
	switch {
	case dependencies["electron"] != "" || strings.Contains(scriptText, "electron"):
		framework, kind, confidence = "Electron", "desktop", .94
	case dependencies["@tauri-apps/api"] != "" || dependencies["@tauri-apps/cli"] != "" || strings.Contains(scriptText, "tauri"):
		framework, kind, confidence = "Tauri", "desktop", .94
	case dependencies["react-native"] != "":
		framework, kind, confidence = "React Native", "desktop", .72
	case hasFilePrefix(directory.files, "vite.config.") || dependencies["vite"] != "" || strings.Contains(scriptText, "vite"):
		framework, port, confidence = "Vite", 5173, .98
	case hasFilePrefix(directory.files, "next.config.") || dependencies["next"] != "" || strings.Contains(scriptText, "next dev") || strings.Contains(scriptText, "next start"):
		framework, port, confidence = "Next.js", 3000, .98
	case directory.files["angular.json"] != "" || dependencies["@angular/core"] != "" || strings.Contains(scriptText, "ng serve"):
		framework, port, confidence = "Angular", 4200, .98
	}
	startName, startScript := preferredPackageScript(manifest.Scripts, "dev", "start", "serve", "preview")
	if startName == "" {
		return AppCandidate{}, false
	}
	if detected := firstPort(startScript); detected != 0 {
		port = detected
	}
	ports := []int{}
	if port != 0 {
		ports = append(ports, port)
	}
	testCommand := ""
	if script := strings.TrimSpace(manifest.Scripts["test"]); script != "" && !strings.Contains(strings.ToLower(script), "no test specified") {
		testCommand = "npm run test"
	}
	name := strings.TrimSpace(manifest.Name)
	if name == "" {
		name = candidateName(projectName, directory.path, root)
	}
	candidate := newCandidate(root, name, directory, framework, kind, "npm run "+startName, testCommand, "", ports, confidence, "package.json contains a "+startName+" script for "+framework)
	candidate.SourceFiles = appendSourceFiles(root, directory, candidate.SourceFiles)
	return candidate, true
}

func candidateFromPython(root, projectName string, directory discoveryDirectory, raw string) (AppCandidate, bool) {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "fastapi") || strings.Contains(lower, "uvicorn"):
		return newCandidate(root, projectName, directory, "FastAPI", "web", "python -m uvicorn main:app --reload", "python -m pytest", "", []int{8000}, .78, "the dependencies include FastAPI or Uvicorn"), true
	case strings.Contains(lower, "flask"):
		return newCandidate(root, projectName, directory, "Flask", "web", "python -m flask --app app run", "python -m pytest", "", []int{5000}, .75, "the dependencies include Flask"), true
	case strings.Contains(lower, "streamlit"):
		return newCandidate(root, projectName, directory, "Streamlit", "web", "python -m streamlit run app.py", "python -m pytest", "", []int{8501}, .75, "the dependencies include Streamlit"), true
	}
	if match := pythonScriptPattern.FindStringSubmatch(raw); len(match) == 2 {
		return newCandidate(root, projectName, directory, "Python", "desktop", match[1], "python -m pytest", "", nil, .55, "pyproject.toml declares an executable command"), true
	}
	return AppCandidate{}, false
}

func candidateFromCargo(root, projectName string, directory discoveryDirectory, raw string) AppCandidate {
	name := candidateName(projectName, directory.path, root)
	if match := rustPackageNamePattern.FindStringSubmatch(raw); len(match) == 2 {
		name = match[1]
	}
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "tauri") {
		return newCandidate(root, name, directory, "Tauri", "desktop", "cargo tauri dev", "cargo test", "", nil, .85, "Cargo.toml includes Tauri")
	}
	if strings.Contains(lower, "actix-web") || strings.Contains(lower, "axum") || strings.Contains(lower, "rocket") {
		return newCandidate(root, name, directory, "Rust web", "web", "cargo run", "cargo test", "", []int{8080}, .68, "Cargo.toml includes a web framework")
	}
	return newCandidate(root, name, directory, "Rust", "desktop", "cargo run", "cargo test", "", nil, .58, "Cargo.toml defines a Rust binary")
}

func candidateFromGo(root, projectName string, directory discoveryDirectory, raw string) AppCandidate {
	name := candidateName(projectName, directory.path, root)
	fields := strings.Fields(raw)
	if len(fields) >= 2 && fields[0] == "module" {
		name = filepath.Base(fields[1])
	}
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "gin-gonic/gin") || strings.Contains(lower, "labstack/echo") || strings.Contains(lower, "gofiber/fiber") {
		return newCandidate(root, name, directory, "Go web", "web", "go run .", "go test ./...", "", []int{8080}, .68, "go.mod includes an HTTP framework")
	}
	return newCandidate(root, name, directory, "Go", "desktop", "go run .", "go test ./...", "", nil, .52, "go.mod defines a Go application")
}

func newCandidate(root, name string, directory discoveryDirectory, framework, kind, start, test, executable string, ports []int, confidence float64, reason string) AppCandidate {
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(directory.path)
	}
	candidate := AppCandidate{
		Name: name, Kind: kind, WorkingDirectory: displayPath(directory.path), StartCommand: start,
		TestCommand: test, Executable: executable, ExpectedPorts: uniquePorts(ports), Framework: framework,
		Confidence: confidence, Reason: reason, SourceFiles: sourceFiles(root, directory),
	}
	if len(candidate.ExpectedPorts) != 0 {
		candidate.SuggestedHealthcheck = fmt.Sprintf("http://localhost:%d", candidate.ExpectedPorts[0])
	}
	hash := sha256.Sum256([]byte(strings.ToLower(filepath.ToSlash(directory.path) + "|" + framework + "|" + start)))
	candidate.ID = fmt.Sprintf("candidate-%x", hash[:8])
	return candidate
}

func normalizeCandidates(root string, candidates []AppCandidate) []AppCandidate {
	seen := map[string]bool{}
	result := make([]AppCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		resolved, err := existingDirectory(candidate.WorkingDirectory)
		if err != nil || !pathInside(root, resolved) || seen[candidate.ID] {
			continue
		}
		seen[candidate.ID] = true
		candidate.WorkingDirectory = displayPath(resolved)
		candidate.SourceFiles = uniqueStrings(candidate.SourceFiles)
		result = append(result, candidate)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Confidence != result[j].Confidence {
			return result[i].Confidence > result[j].Confidence
		}
		return strings.ToLower(result[i].WorkingDirectory+result[i].Name) < strings.ToLower(result[j].WorkingDirectory+result[j].Name)
	})
	return result
}

func preferredPackageScript(scripts map[string]string, names ...string) (string, string) {
	for _, name := range names {
		if script := strings.TrimSpace(scripts[name]); script != "" {
			return name, script
		}
	}
	return "", ""
}

func candidateName(projectName, directory, root string) string {
	if samePath(directory, root) {
		return projectName
	}
	return filepath.Base(directory)
}

func portsFromText(raw string) []int {
	ports := make([]int, 0)
	for _, match := range discoveryPortPattern.FindAllStringSubmatch(raw, -1) {
		if port, err := strconv.Atoi(match[1]); err == nil {
			ports = append(ports, port)
		}
	}
	for _, match := range composePortPattern.FindAllStringSubmatch(raw, -1) {
		if port, err := strconv.Atoi(match[1]); err == nil {
			ports = append(ports, port)
		}
	}
	return uniquePorts(ports)
}

func firstPort(raw string) int {
	ports := portsFromText(raw)
	if len(ports) == 0 {
		return 0
	}
	return ports[0]
}

func uniquePorts(ports []int) []int {
	seen := map[int]bool{}
	result := make([]int, 0, len(ports))
	for _, port := range ports {
		if port > 0 && port <= 65535 && !seen[port] {
			seen[port] = true
			result = append(result, port)
		}
	}
	sort.Ints(result)
	return result
}

func sourceFiles(root string, directory discoveryDirectory) []string {
	result := make([]string, 0, len(directory.sources))
	for _, path := range directory.sources {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func appendSourceFiles(root string, directory discoveryDirectory, existing []string) []string {
	return uniqueStrings(append(existing, sourceFiles(root, directory)...))
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func hasFilePrefix(files map[string]string, prefix string) bool {
	for name := range files {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func firstFile(files map[string]string, names ...string) string {
	for _, name := range names {
		if raw := files[name]; raw != "" {
			return raw
		}
	}
	return ""
}

func readmeStartCommand(raw string) string {
	if match := readmeCommandPattern.FindStringSubmatch(raw); len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}
