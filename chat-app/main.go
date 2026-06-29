package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"chat-app/logutil"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "Crux Agent Chat",
		Width:  1280,
		Height: 820,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 245, G: 245, B: 243, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// shutdown is called by Wails when the app is about to close.
func (a *App) shutdown(ctx context.Context) {
	logutil.Infof("Crux Agent Chat shutting down")

	// Save memory
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		_ = mem.Save()
	}

	logutil.Close()
}
