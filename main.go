package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
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
		AssetServer:      &assetserver.Options{Assets: assets, Middleware: denyFraming},
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

func denyFraming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}
