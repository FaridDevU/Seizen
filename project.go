package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type ProjectSource string

const (
	ProjectCreated  ProjectSource = "created"
	ProjectImported ProjectSource = "imported"
	ProjectGit      ProjectSource = "git"
)

type Project struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Path         string        `json:"path"`
	Source       ProjectSource `json:"source"`
	GitRemote    *string       `json:"gitRemote"`
	Branch       *string       `json:"branch"`
	Favorite     bool          `json:"favorite"`
	Archived     bool          `json:"archived"`
	CreatedAt    string        `json:"createdAt"`
	UpdatedAt    string        `json:"updatedAt"`
	GroupID      *string       `json:"groupId"`
	GroupTitle   *string       `json:"groupTitle"`
	VariantLabel *string       `json:"variantLabel"`
}

type FSProjectInfo struct {
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	GitRemote  *string `json:"gitRemote"`
	Branch     *string `json:"branch"`
	ModifiedAt int64   `json:"modifiedAt"`
}

const projectColumns = `id, name, path, source, git_remote, branch, favorite, archived,
created_at, updated_at, group_id, group_title, variant_label`

const projectNow = `strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`

type rowScanner interface {
	Scan(dest ...any) error
}

type projectQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (a *App) ListProjects(search, filter string) ([]Project, error) {
	where := "archived = FALSE"
	switch filter {
	case "", "all":
	case "favorites":
		where += " AND favorite = TRUE"
	case "archived":
		where = "archived = TRUE"
	default:
		return nil, fmt.Errorf("project filter is not valid: %s", filter)
	}

	ctx := a.context()
	pool, err := a.database.Pool(ctx)
	if err != nil {
		return nil, err
	}
	term := "%" + strings.TrimSpace(search) + "%"
	rows, err := pool.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects
WHERE `+where+` AND (name LIKE ? COLLATE NOCASE OR path LIKE ? COLLATE NOCASE OR COALESCE(git_remote, '') LIKE ? COLLATE NOCASE)
ORDER BY favorite DESC, updated_at DESC, LOWER(name)`, term, term, term)
	if err != nil {
		return nil, fmt.Errorf("could not load the projects: %w", err)
	}
	defer rows.Close()

	projects := make([]Project, 0)
	for rows.Next() {
		project, scanErr := scanProject(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("could not read a project: %w", scanErr)
		}
		projects = append(projects, project)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("could not load the projects: %w", err)
	}
	return projects, nil
}

func (a *App) CreateProject(name string) (Project, error) {
	root, err := a.GetProjectRoot()
	if err != nil {
		return Project{}, err
	}
	info, err := createProjectDirectory(root, name)
	if err != nil {
		return Project{}, err
	}
	project, err := a.saveProject(info, ProjectCreated)
	if err != nil {
		return Project{}, fmt.Errorf("the folder was created at %s, but it could not be saved to the library; you can import it later: %w", info.Path, err)
	}
	return project, nil
}

func (a *App) ImportProjects(paths []string) ([]Project, error) {
	if len(paths) == 0 {
		return []Project{}, nil
	}
	infos := make([]FSProjectInfo, len(paths))
	for index, path := range paths {
		info, err := inspectProjectPath(path)
		if err != nil {
			return nil, err
		}
		infos[index] = info
	}

	ctx := a.context()
	pool, err := a.database.Pool(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("could not start the import: %w", err)
	}
	defer tx.Rollback() // Safe after Commit.

	projects := make([]Project, 0, len(infos))
	for _, info := range infos {
		project, saveErr := upsertProject(ctx, tx, info, ProjectImported)
		if saveErr != nil {
			return nil, fmt.Errorf("could not import %s: %w", info.Path, saveErr)
		}
		projects = append(projects, project)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("could not complete the import: %w", err)
	}
	return projects, nil
}

func (a *App) CloneRepository(url string) (Project, error) {
	root, err := a.GetProjectRoot()
	if err != nil {
		return Project{}, err
	}
	info, err := cloneRepository(a.context(), url, root)
	if err != nil {
		return Project{}, err
	}
	project, err := a.saveProject(info, ProjectGit)
	if err != nil {
		return Project{}, fmt.Errorf("the repository was cloned at %s, but it could not be saved to the library; you can import it later: %w", info.Path, err)
	}
	return project, nil
}

func (a *App) RenameProject(id, path, newName string) (Project, error) {
	info, previousName, err := renameProjectDirectory(path, newName)
	if err != nil {
		return Project{}, err
	}

	ctx := a.context()
	pool, databaseErr := a.database.Pool(ctx)
	if databaseErr == nil {
		project, updateErr := scanProject(pool.QueryRowContext(ctx, `UPDATE projects
SET name = ?, path = ?, git_remote = ?, branch = ?, updated_at = `+projectNow+`
WHERE id = ?
RETURNING `+projectColumns, info.Name, info.Path, info.GitRemote, info.Branch, id))
		if updateErr == nil {
			return project, nil
		}
		if errors.Is(updateErr, sql.ErrNoRows) {
			databaseErr = errors.New("the project was not found")
		} else {
			databaseErr = updateErr
		}
	}

	_, _, rollbackErr := renameProjectDirectory(info.Path, previousName)
	if rollbackErr != nil {
		return Project{}, fmt.Errorf("could not update the library or restore the name: %v · %v", databaseErr, rollbackErr)
	}
	return Project{}, fmt.Errorf("could not update the library; the previous name was restored: %w", databaseErr)
}

func (a *App) DeleteProject(id, path string) error {
	return a.deleteProject(id, path, true)
}

// RemoveProjectFromLibrary removes the project from the library without touching its folder on disk.
func (a *App) RemoveProjectFromLibrary(id, path string) error {
	return a.deleteProject(id, path, false)
}

func (a *App) deleteProject(id, path string, removeFiles bool) error {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	// Read before opening the transaction: the pool has a single connection,
	// so any pool query while the transaction is open deadlocks.
	var root string
	if removeFiles {
		if root, err = a.GetProjectRoot(); err != nil {
			return err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not start the deletion: %w", err)
	}
	defer tx.Rollback()

	var storedPath, source string
	var groupID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT path, source, group_id FROM projects WHERE id = ?`, id).Scan(&storedPath, &source, &groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("the project was not found")
	}
	if err != nil {
		return fmt.Errorf("could not check the project: %w", err)
	}
	if !sameRequestedPath(storedPath, path) {
		return errors.New("the given path does not match the project's saved path")
	}
	if removeFiles && ProjectSource(source) == ProjectImported {
		return errors.New("an imported project cannot be permanently deleted")
	}
	var serverCount, activeAppCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE project_id = ?`, id).Scan(&serverCount); err != nil {
		return fmt.Errorf("could not check the project's servers: %w", err)
	}
	if serverCount != 0 {
		return errors.New("delete the project's servers before deleting it")
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE project_id = ?
AND status IN ('starting', 'running', 'testing', 'stopping')`, id).Scan(&activeAppCount); err != nil {
		return fmt.Errorf("could not check the project's Apps: %w", err)
	}
	if activeAppCount != 0 {
		return errors.New("stop the project's Apps before deleting it")
	}

	var projectPath, quarantine string
	if removeFiles {
		projectPath, err = managedProjectPath(storedPath, root)
		if err != nil {
			return err
		}
		quarantine, err = deletionQuarantine(root)
		if err != nil {
			return err
		}
	}
	// If the row deletion fails after the folder has been moved, it must be restored.
	fail := func(cause error) error {
		if removeFiles {
			return rollbackProjectDeletion(tx, quarantine, projectPath, cause)
		}
		return cause
	}

	a.ensureAgentTokenStore().RevokeProject(id)
	if terminals := a.currentTerminalManager(); terminals != nil {
		terminals.stopProject(id)
	}
	if removeFiles {
		if err = os.Rename(projectPath, quarantine); err != nil {
			return fmt.Errorf("could not prepare the folder for deletion: %w", err)
		}
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ? AND path = ?
AND NOT EXISTS (SELECT 1 FROM servers WHERE project_id = ?)`, id, storedPath, id)
	if err != nil {
		return fail(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fail(err)
	}
	if affected != 1 {
		return fail(errors.New("the project row changed during deletion"))
	}
	if groupID.Valid {
		_, err = tx.ExecContext(ctx, `UPDATE projects
SET group_id = NULL, group_title = NULL, variant_label = NULL, updated_at = `+projectNow+`
WHERE group_id = ? AND (SELECT COUNT(*) FROM projects WHERE group_id = ?) = 1`, groupID.String, groupID.String)
		if err != nil {
			return fail(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	if removeFiles {
		if err = os.RemoveAll(quarantine); err != nil {
			return fmt.Errorf("the project left the library, but the temporary folder %s could not be deleted: %w", quarantine, err)
		}
	}
	if err = a.removeProjectWorkspaceBackground(id); err != nil {
		return fmt.Errorf("the project was deleted, but its background could not be deleted: %w", err)
	}
	if err = a.removeProjectWorkspacePhotos(id); err != nil {
		return fmt.Errorf("the project was deleted, but its photos could not be deleted: %w", err)
	}
	return nil
}

func sameRequestedPath(storedPath, requestedPath string) bool {
	stored, storedErr := filepath.Abs(storedPath)
	requested, requestedErr := filepath.Abs(requestedPath)
	return storedErr == nil && requestedErr == nil && samePath(displayPath(stored), displayPath(requested))
}

func managedProjectPath(storedPath, root string) (string, error) {
	absolute, err := filepath.Abs(storedPath)
	if err != nil {
		return "", fmt.Errorf("the project's saved path is not valid: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("could not open the project's folder: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("a project cannot be deleted through a symbolic link")
	}
	if !info.IsDir() {
		return "", errors.New("the project's saved path is not a folder")
	}

	projectPath, err := canonicalPath(absolute)
	if err != nil {
		return "", fmt.Errorf("could not check the project's folder: %w", err)
	}
	root, err = canonicalPath(root)
	if err != nil {
		return "", fmt.Errorf("could not check the managed folder: %w", err)
	}
	if !samePath(displayPath(absolute), displayPath(projectPath)) {
		return "", errors.New("a project that goes through a symbolic link cannot be deleted")
	}
	if !samePath(filepath.Dir(projectPath), root) {
		return "", errors.New("only folders created directly inside Seizen's projects folder can be deleted")
	}
	return projectPath, nil
}

func deletionQuarantine(root string) (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("could not reserve a temporary folder: %w", err)
	}
	path := filepath.Join(root, ".seizen-delete-"+id)
	if err = ensureAvailable(path); err != nil {
		return "", fmt.Errorf("could not reserve a temporary folder: %w", err)
	}
	return path, nil
}

func rollbackProjectDeletion(tx *sql.Tx, quarantine, original string, cause error) error {
	_ = tx.Rollback()
	if err := os.Rename(quarantine, original); err != nil {
		return fmt.Errorf("could not delete the project (%v) or restore its folder: %w", cause, err)
	}
	return fmt.Errorf("could not delete the project; its folder was restored: %w", cause)
}

func (a *App) SetFavorite(id string, favorite bool) (Project, error) {
	return a.setProjectFlag(id, "favorite", favorite)
}

func (a *App) SetArchived(id string, archived bool) (Project, error) {
	return a.setProjectFlag(id, "archived", archived)
}

func (a *App) OpenProject(path string) error {
	path, err := existingDirectory(path)
	if err != nil {
		return err
	}
	if err = openFolder(path); err != nil {
		return fmt.Errorf("could not open the project: %w", err)
	}
	return nil
}

func (a *App) saveProject(info FSProjectInfo, source ProjectSource) (Project, error) {
	ctx := a.context()
	pool, err := a.database.Pool(ctx)
	if err != nil {
		return Project{}, err
	}
	return upsertProject(ctx, pool, info, source)
}

func (a *App) setProjectFlag(id, column string, value bool) (Project, error) {
	ctx := a.context()
	pool, err := a.database.Pool(ctx)
	if err != nil {
		return Project{}, err
	}
	project, err := scanProject(pool.QueryRowContext(ctx, `UPDATE projects SET `+column+` = ?, updated_at = `+projectNow+`
WHERE id = ? RETURNING `+projectColumns, value, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, errors.New("the project was not found")
	}
	if err != nil {
		return Project{}, fmt.Errorf("could not update the project: %w", err)
	}
	return project, nil
}

func upsertProject(ctx context.Context, database projectQuerier, info FSProjectInfo, source ProjectSource) (Project, error) {
	if source != ProjectCreated && source != ProjectImported && source != ProjectGit {
		return Project{}, errors.New("project source is not valid")
	}
	id, err := newUUID()
	if err != nil {
		return Project{}, fmt.Errorf("could not generate the identifier: %w", err)
	}
	return scanProject(database.QueryRowContext(ctx, `INSERT INTO projects (
id, name, path, source, git_remote, branch, favorite, archived,
created_at, updated_at, group_id, group_title, variant_label
) VALUES (?, ?, ?, ?, ?, ?, FALSE, FALSE, `+projectNow+`, `+projectNow+`, NULL, NULL, NULL)
ON CONFLICT (path) DO UPDATE SET
name = EXCLUDED.name,
git_remote = COALESCE(EXCLUDED.git_remote, projects.git_remote),
branch = COALESCE(EXCLUDED.branch, projects.branch),
archived = FALSE,
updated_at = EXCLUDED.updated_at
RETURNING `+projectColumns, id, info.Name, info.Path, string(source), info.GitRemote, info.Branch))
}

func scanProject(row rowScanner) (Project, error) {
	var project Project
	var source string
	err := row.Scan(
		&project.ID,
		&project.Name,
		&project.Path,
		&source,
		&project.GitRemote,
		&project.Branch,
		&project.Favorite,
		&project.Archived,
		&project.CreatedAt,
		&project.UpdatedAt,
		&project.GroupID,
		&project.GroupTitle,
		&project.VariantLabel,
	)
	project.Source = ProjectSource(source)
	return project, err
}

func createProjectDirectory(parent, name string) (FSProjectInfo, error) {
	if err := validateProjectName(name); err != nil {
		return FSProjectInfo{}, err
	}
	parent, err := existingDirectory(parent)
	if err != nil {
		return FSProjectInfo{}, err
	}
	target := filepath.Join(parent, name)
	if err = ensureAvailable(target); err != nil {
		return FSProjectInfo{}, err
	}
	if err = os.Mkdir(target, 0o755); err != nil {
		return FSProjectInfo{}, fmt.Errorf("could not create the project: %w", err)
	}
	return inspectProjectPath(target)
}

func renameProjectDirectory(path, newName string) (FSProjectInfo, string, error) {
	if err := validateProjectName(newName); err != nil {
		return FSProjectInfo{}, "", err
	}
	source, err := existingDirectory(path)
	if err != nil {
		return FSProjectInfo{}, "", err
	}
	previousName := filepath.Base(source)
	if previousName == newName {
		info, inspectErr := inspectProjectPath(source)
		return info, previousName, inspectErr
	}
	parent := filepath.Dir(source)
	if parent == source {
		return FSProjectInfo{}, "", errors.New("a system root cannot be renamed")
	}
	target := filepath.Join(parent, newName)

	if runtime.GOOS == "windows" && strings.EqualFold(previousName, newName) {
		if err = renameCaseOnly(source, target); err != nil {
			return FSProjectInfo{}, "", err
		}
	} else {
		if err = ensureAvailable(target); err != nil {
			return FSProjectInfo{}, "", err
		}
		if err = os.Rename(source, target); err != nil {
			return FSProjectInfo{}, "", fmt.Errorf("could not rename the project: %w", err)
		}
	}
	info, err := inspectProjectPath(target)
	return info, previousName, err
}

func renameCaseOnly(source, target string) error {
	parent := filepath.Dir(source)
	var temporary string
	for attempt := 0; attempt < 100; attempt++ {
		candidate := filepath.Join(parent, fmt.Sprintf(".seizen-rename-%d-%d", os.Getpid(), attempt))
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			temporary = candidate
			break
		}
	}
	if temporary == "" {
		return errors.New("could not reserve a temporary name")
	}
	if err := os.Rename(source, temporary); err != nil {
		return fmt.Errorf("could not prepare the rename: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Rename(temporary, source)
		return fmt.Errorf("could not rename the project: %w", err)
	}
	return nil
}

func inspectProjectPath(path string) (FSProjectInfo, error) {
	path, err := existingDirectory(path)
	if err != nil {
		return FSProjectInfo{}, err
	}
	metadata, err := os.Stat(path)
	if err != nil {
		return FSProjectInfo{}, fmt.Errorf("could not read the project: %w", err)
	}
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return FSProjectInfo{}, errors.New("the project must have a folder name")
	}

	isGitRoot := false
	if root, ok := gitValue(path, "rev-parse", "--show-toplevel"); ok {
		if resolvedRoot, resolveErr := canonicalPath(root); resolveErr == nil {
			isGitRoot = samePath(resolvedRoot, path)
		}
	}
	var remote, branch *string
	if isGitRoot {
		if value, ok := gitValue(path, "config", "--get", "remote.origin.url"); ok {
			remote = &value
		}
		if value, ok := gitValue(path, "branch", "--show-current"); ok {
			branch = &value
		}
	}

	return FSProjectInfo{
		Name:       name,
		Path:       displayPath(path),
		GitRemote:  remote,
		Branch:     branch,
		ModifiedAt: metadata.ModTime().UnixMilli(),
	}, nil
}

func cloneRepository(ctx context.Context, url, parent string) (FSProjectInfo, error) {
	repositoryName, err := githubRepositoryName(url)
	if err != nil {
		return FSProjectInfo{}, err
	}
	if err = validateProjectName(repositoryName); err != nil {
		return FSProjectInfo{}, err
	}
	parent, err = existingDirectory(parent)
	if err != nil {
		return FSProjectInfo{}, err
	}
	target := filepath.Join(parent, repositoryName)
	if err = ensureAvailable(target); err != nil {
		return FSProjectInfo{}, err
	}
	if err = os.Mkdir(target, 0o755); err != nil {
		return FSProjectInfo{}, fmt.Errorf("could not reserve the repository's folder: %w", err)
	}

	command := exec.CommandContext(ctx, "git", "clone", "--", url, target)
	hideWindow(command)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		_ = os.Remove(target) // Only removes the reservation when Git left it empty.
		return FSProjectInfo{}, gitError("Git could not clone the repository", commandErr, output)
	}
	return inspectProjectPath(target)
}

func gitValue(path string, arguments ...string) (string, bool) {
	arguments = append([]string{"-C", path}, arguments...)
	command := exec.Command("git", arguments...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	hideWindow(command)
	output, err := command.Output()
	if err != nil || !utf8.Valid(output) {
		return "", false
	}
	value := strings.TrimSpace(string(output))
	return value, value != ""
}

func gitError(message string, err error, output []byte) error {
	detail := []rune(strings.TrimSpace(string(output)))
	if len(detail) > 500 {
		detail = detail[:500]
	}
	if len(detail) == 0 {
		return fmt.Errorf("%s: %w", message, err)
	}
	return fmt.Errorf("%s: %s", message, string(detail))
}

func existingDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return "", errors.New("the path is not valid")
	}
	path, err := canonicalPath(path)
	if err != nil {
		return "", fmt.Errorf("the folder does not exist or is not accessible: %w", err)
	}
	metadata, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("the folder does not exist or is not accessible: %w", err)
	}
	if !metadata.IsDir() {
		return "", errors.New("the path must point to a folder")
	}
	return path, nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func ensureAvailable(path string) error {
	_, err := os.Lstat(path)
	if err == nil {
		return errors.New("a file or folder with that name already exists")
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("could not check whether the destination folder exists: %w", err)
}

func validateProjectName(name string) error {
	invalid := name == "" ||
		len(utf16.Encode([]rune(name))) > 255 ||
		name == "." || name == ".." ||
		strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".")
	if !invalid {
		for _, character := range name {
			if character <= 0x1f || strings.ContainsRune(`<>:"/\|?*`, character) {
				invalid = true
				break
			}
		}
	}
	if invalid {
		return errors.New("the name contains characters not allowed on Windows")
	}

	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL"
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		reserved = stem[3] >= '1' && stem[3] <= '9'
	}
	if reserved {
		return errors.New("that name is reserved by Windows")
	}
	return nil
}

func githubRepositoryName(url string) (string, error) {
	if strings.TrimSpace(url) != url || strings.ContainsAny(url, "?#") {
		return "", errors.New("the repository URL is not valid")
	}
	lowercase := strings.ToLower(url)
	var path string
	switch {
	case strings.HasPrefix(lowercase, "https://github.com/"):
		path = url[len("https://github.com/"):]
	case strings.HasPrefix(lowercase, "git@github.com:"):
		path = url[len("git@github.com:"):]
	default:
		return "", errors.New("only HTTPS or git@ GitHub repositories are supported")
	}
	segments := strings.Split(path, "/")
	if len(segments) != 2 || !validGitHubSegment(segments[0]) {
		return "", errors.New("the URL must identify a GitHub repository")
	}
	repository := segments[1]
	if strings.HasSuffix(strings.ToLower(repository), ".git") {
		repository = repository[:len(repository)-4]
	}
	if !validGitHubSegment(repository) {
		return "", errors.New("the URL must identify a GitHub repository")
	}
	return repository, nil
}

func validGitHubSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for _, character := range []byte(segment) {
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') &&
			character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func displayPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func openFolder(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer.exe", path)
	case "darwin":
		command = exec.Command("open", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	hideWindow(command)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
