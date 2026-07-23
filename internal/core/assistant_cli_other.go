//go:build !windows

package core

import (
	"context"
	"errors"
)

func assistantCLIStatus(string) AssistantCLIStatus { return AssistantCLIStatus{} }

func assistantClaudeCLIModels() ([]AssistantModel, error) {
	return nil, errors.New("subscription providers are Windows-only for now")
}

func assistantCodexCLIModels() ([]AssistantModel, error) {
	return nil, errors.New("subscription providers are Windows-only for now")
}

func runAssistantCLI(context.Context, string, string, string, string, string, string, string, string) (string, string, error) {
	return "", "", errors.New("subscription providers are Windows-only for now")
}
