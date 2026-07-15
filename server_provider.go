package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type ServerExecResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
}

type ServerStats struct {
	CPUPercent       float64 `json:"cpuPercent"`
	MemoryUsedMB     int     `json:"memoryUsedMb"`
	MemoryLimitMB    int     `json:"memoryLimitMb"`
	LimitsEnforced   bool    `json:"limitsEnforced"`
	LimitDescription string  `json:"limitDescription"`
}

var ErrServerProviderUnavailable = errors.New("the server provider is not available")

// IncusServerProvider is deliberately unavailable until Seizen ships and
// validates an Incus runtime. Keeping the provider explicit avoids silently
// falling back to a weaker isolation model.
type IncusServerProvider struct{}

func (IncusServerProvider) Create(context.Context, Server) (string, error) {
	return "", fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}
func (IncusServerProvider) Start(context.Context, Server) error {
	return fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}
func (IncusServerProvider) Stop(context.Context, Server) error {
	return fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}
func (IncusServerProvider) Restart(context.Context, Server) error {
	return fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}
func (IncusServerProvider) Destroy(context.Context, Server) error {
	return fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}
func (IncusServerProvider) Exec(context.Context, Server, string) (ServerExecResult, error) {
	return ServerExecResult{}, fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}
func (IncusServerProvider) Stats(context.Context, Server) (ServerStats, error) {
	return ServerStats{}, fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}
func (IncusServerProvider) InspectServices(context.Context, Server) ([]ServerService, error) {
	return nil, fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}
func (IncusServerProvider) CheckHealth(context.Context, Server) (ServerHealth, error) {
	return ServerHealth{}, fmt.Errorf("Incus: %w", ErrServerProviderUnavailable)
}

type ServerHealth struct {
	Healthy bool   `json:"healthy"`
	Message string `json:"message"`
}

type ServerProvider interface {
	Create(context.Context, Server) (string, error)
	Start(context.Context, Server) error
	Stop(context.Context, Server) error
	Restart(context.Context, Server) error
	Destroy(context.Context, Server) error
	Exec(context.Context, Server, string) (ServerExecResult, error)
	Stats(context.Context, Server) (ServerStats, error)
	InspectServices(context.Context, Server) ([]ServerService, error)
	CheckHealth(context.Context, Server) (ServerHealth, error)
}

type MockServerProvider struct {
	mu      sync.Mutex
	servers map[string]Server
}

func NewMockServerProvider() *MockServerProvider {
	return &MockServerProvider{servers: make(map[string]Server)}
}

func (p *MockServerProvider) Create(_ context.Context, server Server) (string, error) {
	if server.ID == "" || server.Provider != "mock" {
		return "", errors.New("the mock provider received an invalid server")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.servers[server.ID]; exists {
		return "", errors.New("the mock server already exists")
	}
	server.RuntimeReference = "mock://" + server.ID
	server.Status = "stopped"
	p.servers[server.ID] = server
	return server.RuntimeReference, nil
}

func (p *MockServerProvider) Start(_ context.Context, server Server) error {
	return p.setStatus(server, "running")
}

func (p *MockServerProvider) Stop(_ context.Context, server Server) error {
	return p.setStatus(server, "stopped")
}

func (p *MockServerProvider) Restart(ctx context.Context, server Server) error {
	if err := p.Stop(ctx, server); err != nil {
		return err
	}
	return p.Start(ctx, server)
}

func (p *MockServerProvider) Destroy(_ context.Context, server Server) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.lookup(server); !ok {
		return errors.New("the mock server was not found")
	}
	delete(p.servers, server.ID)
	return nil
}

func (p *MockServerProvider) Exec(_ context.Context, server Server, command string) (ServerExecResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	stored, ok := p.lookup(server)
	if !ok || stored.Status != "running" {
		return ServerExecResult{}, errors.New("the mock server is stopped")
	}
	return ServerExecResult{Output: fmt.Sprintf("mock: %s", command), ExitCode: 0}, nil
}

func (p *MockServerProvider) Stats(_ context.Context, server Server) (ServerStats, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	stored, ok := p.lookup(server)
	if !ok {
		return ServerStats{}, errors.New("the mock server was not found")
	}
	stats := ServerStats{
		MemoryLimitMB:    stored.MemoryMB,
		LimitsEnforced:   false,
		LimitDescription: "Resources requested for the simulation; not system limits.",
	}
	if stored.Status == "running" {
		stats.LimitDescription += " The mock provider does not fabricate CPU or RAM usage."
	}
	return stats, nil
}

func (p *MockServerProvider) InspectServices(_ context.Context, server Server) ([]ServerService, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.lookup(server); !ok {
		return nil, errors.New("the mock server was not found")
	}
	return []ServerService{}, nil
}

func (p *MockServerProvider) CheckHealth(_ context.Context, server Server) (ServerHealth, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	stored, ok := p.lookup(server)
	if !ok {
		return ServerHealth{}, errors.New("the mock server was not found")
	}
	healthy := stored.Status == "running"
	message := "stopped"
	if healthy {
		message = "healthy"
	}
	return ServerHealth{Healthy: healthy, Message: message}, nil
}

func (p *MockServerProvider) setStatus(server Server, status string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	stored, ok := p.lookup(server)
	if !ok {
		return errors.New("the mock server was not found")
	}
	stored.Status = status
	p.servers[server.ID] = stored
	return nil
}

func (p *MockServerProvider) lookup(server Server) (Server, bool) {
	stored, ok := p.servers[server.ID]
	if ok {
		return stored, true
	}
	if server.RuntimeReference == "mock://"+server.ID && server.ID != "" {
		p.servers[server.ID] = server
		return server, true
	}
	return Server{}, false
}

var defaultMockServerProvider = NewMockServerProvider()
