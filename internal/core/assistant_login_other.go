//go:build !windows

package core

import "errors"

func startAssistantLogin(*App, string) error {
	return errors.New("subscription providers are Windows-only for now")
}

func submitAssistantLoginCode(string) error {
	return errors.New("subscription providers are Windows-only for now")
}

func cancelAssistantLogin() error { return nil }
