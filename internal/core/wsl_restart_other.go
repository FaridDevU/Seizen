//go:build !windows

package core

func wslRestartRequired() (bool, string) {
	return false, ""
}
