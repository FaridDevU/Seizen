//go:build !windows

package main

func wslRestartRequired() (bool, string) {
	return false, ""
}
