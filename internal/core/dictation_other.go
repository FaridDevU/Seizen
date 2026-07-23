//go:build !windows

package core

import "errors"

func startDictation() error {
	return errors.New("dictation is only supported on Windows")
}
