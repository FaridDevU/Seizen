//go:build !windows

package core

// Folder-level deletion guards are Windows-only; elsewhere the vault relies on living in
// app storage. These no-ops keep the cross-platform call sites simple.

func protectVaultRoot(string) error { return nil }

func protectFolder(string) error { return nil }

func unprotectFolder(string) error { return nil }
