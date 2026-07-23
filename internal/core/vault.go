package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Projects live in a protected vault inside Seizen's own data folder rather than in a
// user-chosen location. Adding a project copies its files into the vault, so moving or
// deleting the original folder in Explorer can never break the project. Each project
// folder is guarded against accidental deletion (see protectFolder); the app lifts the
// guard on purpose when it renames or deletes a project.

const projectImportProgressEvent = "project.import.progress"

// largeImportThreshold is the copied size above which the UI confirms before importing,
// since a verbatim copy of node_modules/.git can be large and slow.
const largeImportThreshold = 2 << 30 // 2 GiB

// ImportProgress is emitted while a folder is copied into the vault so the UI can show
// a live indicator instead of freezing on a large tree.
type ImportProgress struct {
	Name  string `json:"name"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
	Done  bool   `json:"done"`
}

// ImportEstimate reports how much a folder would copy into the vault, and whether the UI
// should confirm first.
type ImportEstimate struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
	Large bool  `json:"large"`
}

// EstimateImport measures a folder before importing so the UI can warn about a large copy.
func (a *App) EstimateImport(path string) (ImportEstimate, error) {
	resolved, err := existingDirectory(path)
	if err != nil {
		return ImportEstimate{}, err
	}
	bytes, files, large := estimateFolder(resolved, largeImportThreshold)
	return ImportEstimate{Bytes: bytes, Files: files, Large: large}, nil
}

// estimateFolder sums regular-file bytes, stopping early once threshold is crossed — it
// only needs the yes/no answer, not an exact size, so a huge tree isn't fully walked.
func estimateFolder(root string, threshold int64) (int64, int, bool) {
	var bytes int64
	var files int
	large := false
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() || entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		bytes += info.Size()
		files++
		if bytes >= threshold {
			large = true
			return filepath.SkipAll
		}
		return nil
	})
	return bytes, files, large
}

// healVault re-asserts the delete guards on the vault and every project inside it, and
// clears leftovers from a crashed import or delete. Guards persist on disk, so this only
// matters if one was removed, but re-asserting on launch keeps the vault trustworthy.
func (a *App) healVault() {
	root, err := a.vaultRoot()
	if err != nil {
		return
	}
	_ = protectVaultRoot(root)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(root, name)
		if strings.HasPrefix(name, ".seizen-import-") || strings.HasPrefix(name, ".seizen-delete-") {
			_ = os.RemoveAll(path) // Orphan from a crashed import/delete.
			continue
		}
		_ = protectFolder(path)
	}
}

// vaultRoot returns the folder that holds every project's files. It sits next to the
// database in app storage and is never user-relocatable.
func (a *App) vaultRoot() (string, error) {
	databasePath, err := a.database.databasePath()
	if err != nil {
		return "", fmt.Errorf("could not find Seizen's data folder: %w", err)
	}
	root := filepath.Join(filepath.Dir(databasePath), "vault")
	if err = os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("could not create the vault: %w", err)
	}
	return filepath.Abs(root)
}

// importIntoVault copies an external folder verbatim into the vault under a unique name,
// emitting progress, then returns the managed copy. The original folder is never touched.
func (a *App) importIntoVault(ctx context.Context, source string) (FSProjectInfo, error) {
	resolved, err := existingDirectory(source)
	if err != nil {
		return FSProjectInfo{}, err
	}
	root, err := a.vaultRoot()
	if err != nil {
		return FSProjectInfo{}, err
	}
	if within(root, resolved) {
		return FSProjectInfo{}, errors.New("this folder is already stored securely in Seizen")
	}
	target, err := uniqueVaultTarget(root, filepath.Base(resolved))
	if err != nil {
		return FSProjectInfo{}, err
	}
	name := filepath.Base(target)

	id, err := newUUID()
	if err != nil {
		return FSProjectInfo{}, err
	}
	temp := filepath.Join(root, ".seizen-import-"+id)
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(temp) // temp is unprotected, so this always succeeds.
		}
	}()

	if err = copyTree(ctx, resolved, temp, func(files int, bytes int64) {
		a.emitImportProgress(ImportProgress{Name: name, Files: files, Bytes: bytes})
	}); err != nil {
		return FSProjectInfo{}, fmt.Errorf("could not copy the project into the vault: %w", err)
	}
	if err = os.Rename(temp, target); err != nil {
		return FSProjectInfo{}, fmt.Errorf("could not place the project in the vault: %w", err)
	}
	completed = true
	_ = protectFolder(target) // Best-effort; a project is still usable without the guard.
	a.emitImportProgress(ImportProgress{Name: name, Done: true})
	return inspectProjectPath(target)
}

// guardVaultFolder applies the delete guard, but only to folders that live in the vault
// so a user's external folder is never touched.
func (a *App) guardVaultFolder(path string) {
	if a.inVault(path) {
		_ = protectFolder(path)
	}
}

func (a *App) unguardVaultFolder(path string) {
	if a.inVault(path) {
		_ = unprotectFolder(path)
	}
}

func (a *App) inVault(path string) bool {
	root, err := a.vaultRoot()
	if err != nil {
		return false
	}
	if canonicalRoot, canonicalErr := canonicalPath(root); canonicalErr == nil {
		root = canonicalRoot
	}
	resolved, err := canonicalPath(path)
	if err != nil {
		if resolved, err = filepath.Abs(path); err != nil {
			return false
		}
	}
	return within(root, resolved)
}

func (a *App) emitImportProgress(progress ImportProgress) {
	ctx := a.context()
	if ctx.Value("events") != nil {
		a.emitEvent(ctx, projectImportProgressEvent, progress)
	}
}

// copyTree copies source into target verbatim. Unreadable individual entries are skipped
// rather than aborting the whole import; symlinks and junctions are recreated as links and
// never followed, which avoids infinite recursion on self-referential trees.
func copyTree(ctx context.Context, source, target string, onProgress func(files int, bytes int64)) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	var files int
	var total int64
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			if path == source {
				return walkErr // Can't read the root itself: nothing to copy.
			}
			return nil // Skip an unreadable entry.
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return nil // Vanished mid-walk; skip.
		}
		mode := info.Mode()

		// A symlink or Windows junction is copied as a link, never followed.
		if mode&(os.ModeSymlink|os.ModeIrregular) != 0 {
			if linkTarget, readErr := os.Readlink(path); readErr == nil {
				_ = os.MkdirAll(filepath.Dir(destination), 0o755)
				_ = os.Symlink(linkTarget, destination) // Best-effort (may need privilege on Windows).
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !mode.IsRegular() {
			return nil // Skip devices, pipes, sockets.
		}
		written, err := copyFile(path, destination, mode.Perm())
		if err != nil {
			return nil // Skip a single unreadable/locked file.
		}
		files++
		total += written
		if files%100 == 0 {
			onProgress(files, total)
		}
		return nil
	})
}

func copyFile(source, destination string, perm os.FileMode) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}

// uniqueVaultTarget returns a free path in the vault based on the desired name, adding a
// " (n)" suffix on collision.
func uniqueVaultTarget(root, desired string) (string, error) {
	base := sanitizeProjectName(desired)
	for attempt := 0; attempt < 1000; attempt++ {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%s (%d)", base, attempt+1)
		}
		target := filepath.Join(root, name)
		if ensureAvailable(target) == nil {
			return target, nil
		}
	}
	return "", errors.New("could not find a free name in the vault")
}

// sanitizeProjectName maps a folder name to a valid Windows project name, replacing
// disallowed characters and falling back to "project" when nothing usable remains.
func sanitizeProjectName(name string) string {
	name = strings.TrimSpace(name)
	if validateProjectName(name) == nil {
		return name
	}
	var builder strings.Builder
	for _, character := range name {
		if character <= 0x1f || strings.ContainsRune(`<>:"/\|?*`, character) {
			builder.WriteRune('-')
		} else {
			builder.WriteRune(character)
		}
	}
	cleaned := strings.Trim(strings.TrimSpace(builder.String()), " .-")
	if cleaned == "" || validateProjectName(cleaned) != nil {
		return "project"
	}
	return cleaned
}

// within reports whether path is root or lives inside it.
func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}
