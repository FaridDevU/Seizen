//go:build !windows

package main

import (
	"context"
	"fmt"
)

type WslServerProvider struct{}

func NewWslServerProvider() *WslServerProvider { return &WslServerProvider{} }

func (WslServerProvider) unavailable() error {
	return fmt.Errorf("WSL: %w", ErrServerProviderUnavailable)
}

func (provider WslServerProvider) Create(context.Context, Server) (string, error) {
	return "", provider.unavailable()
}
func (provider WslServerProvider) Start(context.Context, Server) error { return provider.unavailable() }
func (provider WslServerProvider) Stop(context.Context, Server) error  { return provider.unavailable() }
func (provider WslServerProvider) Restart(context.Context, Server) error {
	return provider.unavailable()
}
func (provider WslServerProvider) Destroy(context.Context, Server) error {
	return provider.unavailable()
}
func (provider WslServerProvider) Exec(context.Context, Server, string) (ServerExecResult, error) {
	return ServerExecResult{}, provider.unavailable()
}
func (provider WslServerProvider) Stats(context.Context, Server) (ServerStats, error) {
	return ServerStats{}, provider.unavailable()
}
func (provider WslServerProvider) InspectServices(context.Context, Server) ([]ServerService, error) {
	return nil, provider.unavailable()
}
func (provider WslServerProvider) CheckHealth(context.Context, Server) (ServerHealth, error) {
	return ServerHealth{}, provider.unavailable()
}
func (provider WslServerProvider) RootTerminalCommand(Server) (string, []string, error) {
	return "", nil, provider.unavailable()
}
