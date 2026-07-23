//go:build windows

package core

import (
	"fmt"
	"os/exec"
	"strings"
)

// Windows deletion model: a folder can be deleted if the caller has DELETE on the folder
// itself OR DELETE_CHILD on its parent. To make a project folder undeletable by accident
// we close both paths with non-inherited deny ACEs, while leaving everything inside fully
// writable so git, npm and editors work normally. Deliberate removal needs a specific
// command: icacls "<folder>" /remove:d "*S-1-1-0" (which is what the app runs itself).
const everyoneSID = "*S-1-1-0"

// protectVaultRoot denies DELETE_CHILD on the vault so no project folder can be deleted or
// renamed through the parent. Creating new project folders still works (that needs
// ADD_SUBDIRECTORY, not DELETE_CHILD).
func protectVaultRoot(root string) error {
	_ = icacls(root, "/remove:d", everyoneSID) // Idempotent: clear any prior deny first.
	return icacls(root, "/deny", everyoneSID+":(DC)")
}

// protectFolder denies DELETE on the folder itself, this-folder-only, so the folder can't
// be removed or renamed while its contents stay mutable.
func protectFolder(path string) error {
	_ = icacls(path, "/remove:d", everyoneSID) // Idempotent: avoid stacking duplicate ACEs.
	return icacls(path, "/deny", everyoneSID+":(DE)")
}

// unprotectFolder removes the deny ACEs so the app can rename or delete on purpose.
func unprotectFolder(path string) error {
	return icacls(path, "/remove:d", everyoneSID)
}

func icacls(path string, arguments ...string) error {
	command := exec.Command("icacls", append([]string{path}, arguments...)...)
	hideWindow(command)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not adjust the vault protection: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}
