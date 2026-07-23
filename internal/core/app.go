package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const beforeCloseEvent = "seizen:before-close"
const closeHandshakeTimeout = 10 * time.Second

type App struct {
	mu                   sync.RWMutex
	ctx                  context.Context
	database             *Database
	terminals            *terminalManager
	appRuntimes          *AppRuntimeManager
	servers              *ServerManager
	vscode               *managedVSCodeInstaller
	editors              *editorSessionManager
	nativeEditors        *nativeEditorManager
	media                mediaController
	agentBridge          *AgentBridge
	agentTokens          *agentTokenStore
	startExperimentAgent func(string, string, string, string) (string, error)
	closePending         bool
	closeAllowed         bool
	closeAttempt         uint64
	importCancel         context.CancelFunc
	emitEvent            func(context.Context, string, ...interface{})
	quit                 func(context.Context)
}

func NewApp() *App {
	return &App{
		database:    NewDatabase(),
		agentTokens: newAgentTokenStore(),
		emitEvent:   wailsruntime.EventsEmit,
		quit:        wailsruntime.Quit,
	}
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	// Off the UI path: re-assert the vault guards and clear any leftovers from a crashed
	// import or delete. Per-project path health is reported by ListProjects (Project.Missing),
	// which the library loads on open.
	go a.healVault()
}

func (a *App) shutdown(context.Context) {
	a.mu.RLock()
	runtimes := a.appRuntimes
	servers := a.servers
	editors := a.editors
	nativeEditors := a.nativeEditors
	media := a.media
	bridge := a.agentBridge
	a.mu.RUnlock()
	if bridge != nil {
		bridge.Close()
	}
	if media != nil {
		media.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeHandshakeTimeout)
	_ = a.cleanupAttachedApps(ctx, "")
	cancel()
	prepareManagedClose(runtimes, servers, editors, nativeEditors)
	if terminals := a.projectTerminalManager(); terminals != nil {
		terminals.stopAll()
	}
	a.database.Close()
}

func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	if a.closeAllowed {
		a.mu.Unlock()
		return false
	}
	if a.closePending {
		a.mu.Unlock()
		return true
	}
	a.closePending = true
	a.closeAttempt++
	attempt := a.closeAttempt
	emitEvent := a.emitEvent
	a.mu.Unlock()

	time.AfterFunc(closeHandshakeTimeout, func() { a.expireCloseAttempt(attempt) })
	emitEvent(ctx, beforeCloseEvent)
	return true
}

func (a *App) expireCloseAttempt(attempt uint64) {
	a.mu.Lock()
	if a.closePending && a.closeAttempt == attempt {
		a.closePending = false
	}
	a.mu.Unlock()
}

// ConfirmClose completes a close request after the frontend saved its state.
func (a *App) ConfirmClose() {
	a.mu.Lock()
	if a.closeAllowed {
		a.mu.Unlock()
		return
	}
	a.closeAllowed = true
	a.closePending = false
	quit := a.quit
	ctx := a.ctx
	runtimes := a.appRuntimes
	servers := a.servers
	editors := a.editors
	nativeEditors := a.nativeEditors
	bridge := a.agentBridge
	a.mu.Unlock()

	if bridge != nil {
		bridge.Close()
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), closeHandshakeTimeout)
	_ = a.cleanupAttachedApps(cleanupContext, "")
	cancel()
	prepareManagedClose(runtimes, servers, editors, nativeEditors)
	if terminals := a.currentTerminalManager(); terminals != nil {
		terminals.stopAll()
	}
	quit(ctx)
}

func prepareManagedClose(runtimes *AppRuntimeManager, servers *ServerManager, editors *editorSessionManager, nativeEditors *nativeEditorManager) {
	if editors != nil {
		editors.close()
	}
	if nativeEditors != nil {
		nativeEditors.close()
	}
	if runtimes != nil {
		ctx, cancel := context.WithTimeout(context.Background(), closeHandshakeTimeout)
		_ = runtimes.PrepareRuntimeClose(ctx)
		cancel()
	}
	if servers != nil {
		ctx, cancel := context.WithTimeout(context.Background(), closeHandshakeTimeout)
		_ = servers.PrepareServerClose(ctx)
		cancel()
	}
}

// CancelClose allows a new close request if frontend persistence failed.
func (a *App) CancelClose() {
	a.mu.Lock()
	a.closePending = false
	a.mu.Unlock()
}

func (a *App) context() context.Context {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (a *App) projectTerminalManager() *terminalManager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminals == nil {
		a.terminals = newTerminalManager(func(name string, payload any) {
			if name == terminalExitEvent {
				if event, ok := payload.(TerminalExitEvent); ok {
					a.ensureAgentTokenStore().RevokeSession(event.SessionID)
					a.handleAttachedTerminalExit(event.SessionID, event.Error)
				}
			}
			ctx := a.context()
			if ctx.Value("events") != nil {
				wailsruntime.EventsEmit(ctx, name, payload)
			}
		})
	}
	return a.terminals
}

func (a *App) currentTerminalManager() *terminalManager {
	a.mu.RLock()
	manager := a.terminals
	a.mu.RUnlock()
	return manager
}

func (a *App) ensureAgentTokenStore() *agentTokenStore {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.agentTokens == nil {
		a.agentTokens = newAgentTokenStore()
	}
	return a.agentTokens
}

func (a *App) ensureAgentBridge() (*AgentBridge, string, error) {
	tokens := a.ensureAgentTokenStore()
	a.mu.Lock()
	if a.agentBridge == nil {
		a.agentBridge = newAgentBridge(a, tokens)
	}
	bridge := a.agentBridge
	a.mu.Unlock()
	url, err := bridge.Start()
	return bridge, url, err
}

func (a *App) projectAppRuntimeManager() *AppRuntimeManager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appRuntimes == nil {
		a.appRuntimes = newAppRuntimeManager(a.database, func(name string, payload any) {
			ctx := a.context()
			if ctx.Value("events") != nil && a.emitEvent != nil {
				a.emitEvent(ctx, name, payload)
			}
		})
		a.appRuntimes.configure = a.UpdateApp
	}
	return a.appRuntimes
}

func (a *App) projectServerManager() *ServerManager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.servers == nil {
		a.servers = NewServerManager(a.database, func(name string, payload any) {
			a.emitAgentEvent(name, payload)
		})
	}
	return a.servers
}

// Initialize opens the local database and applies the bundled schema migration.
func (a *App) Initialize() error {
	if err := a.database.Initialize(a.context()); err != nil {
		return err
	}
	if err := a.projectServerManager().ReconcileInterrupted(a.context()); err != nil {
		a.emitAgentEvent("server.reconcile.failed", map[string]string{"error": err.Error()})
	}
	if integrations, err := a.database.EditorIntegrations(a.context()); err == nil {
		for _, integration := range integrations {
			if integration.ID == "vscode" && integration.Enabled {
				if installer, installErr := a.managedVSCodeInstaller(); installErr == nil {
					go func() { _ = installer.install(a.context()) }()
				}
				break
			}
		}
	}
	return nil
}

// GetProjectRoot reports the vault that now holds every project. It is no longer a
// user-chosen location — that was what let a project's folder be moved or deleted out
// from under the app.
func (a *App) GetProjectRoot() (string, error) {
	root, err := a.vaultRoot()
	if err != nil {
		return "", err
	}
	return displayPath(root), nil
}

// SetProjectRoot is kept for binding compatibility only. Projects always live in the
// protected vault, so the location can't be changed; this returns the vault path.
func (a *App) SetProjectRoot(string) (string, error) {
	return a.GetProjectRoot()
}

func (a *App) GetAppearance() (Appearance, error) {
	return a.database.Appearance(a.context())
}

func (a *App) SetAppearance(mode, accent string) (Appearance, error) {
	return a.database.SetAppearance(a.context(), mode, accent)
}

// StartDictation opens the OS dictation overlay for the focused input.
func (a *App) StartDictation() error {
	return startDictation()
}

// ChooseDirectory opens the native Wails directory picker. An empty string means cancel.
func (a *App) ChooseDirectory(title string) (string, error) {
	if title == "" {
		title = "Choose folder"
	}
	path, err := wailsruntime.OpenDirectoryDialog(a.context(), wailsruntime.OpenDialogOptions{
		Title:                title,
		CanCreateDirectories: true,
		ResolvesAliases:      true,
	})
	if err != nil {
		return "", fmt.Errorf("could not open the folder picker: %w", err)
	}
	return path, nil
}
