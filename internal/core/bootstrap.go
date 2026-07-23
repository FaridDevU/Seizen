package core

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// Run boots Seizen: the MCP bridge subprocess when requested via
// --seizen-agent-bridge, otherwise the desktop app.
func Run(assets embed.FS) {
	if len(os.Args) == 2 && os.Args[1] == "--seizen-agent-bridge" {
		if err := runAgentMCPBridge(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	}
	app := NewApp()
	databasePath, err := app.database.databasePath()
	if err != nil {
		log.Fatal(err)
	}
	webviewData, err := ensureWebviewData(databasePath)
	if err != nil {
		log.Fatal("could not create the browser profile: ", err)
	}
	err = wails.Run(&options.App{
		Title:            "Seizen",
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: options.NewRGB(247, 246, 243),
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Middleware: app.assetMiddleware(),
		},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		WindowStartState: options.Normal,
		DisableResize:    false,
		Fullscreen:       false,
		Frameless:        true,
		Windows:          &windows.Options{WebviewUserDataPath: webviewData},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func ensureWebviewData(databasePath string) (string, error) {
	path := filepath.Join(filepath.Dir(databasePath), "webview")
	return path, os.MkdirAll(path, 0o700)
}

// assetMiddleware serves managed workspace assets and denies framing. It must run
// as middleware (not AssetServer.Handler) so /workspace-asset/ is intercepted before
// the request reaches the frontend. In `wails dev` the frontend is a Vite server whose
// SPA fallback answers every path with index.html, which would otherwise shadow the
// Handler and make dropped documents load HTML instead of their bytes.
func (a *App) assetMiddleware() assetserver.Middleware {
	assets := a.workspaceAssetHandler()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
			response.Header().Set("X-Frame-Options", "DENY")
			if strings.HasPrefix(request.URL.Path, workspaceAssetURLPrefix) ||
				strings.HasPrefix(request.URL.Path, workspaceBackgroundURLPrefix) {
				assets.ServeHTTP(response, request)
				return
			}
			next.ServeHTTP(response, request)
		})
	}
}
