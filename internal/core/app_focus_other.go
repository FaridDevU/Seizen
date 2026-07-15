//go:build !windows

package core

import "errors"

func focusProcessWindow(int) error {
	return errors.New("focusing a native App is only available on Windows")
}
