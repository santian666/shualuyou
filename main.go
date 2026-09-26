package main

import (
	"embed"
	"io/fs"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed frontend/src
var assets embed.FS

func main() {
	frontend, err := fs.Sub(assets, "frontend/src")
	if err != nil {
		slog.Error("读取前端资源失败", "error", err)
		return
	}
	app := NewApp()
	if err := wails.Run(&options.App{
		Title:            "小米路由器自动刷机助手 v0.5.7",
		Width:            1280,
		Height:           900,
		MinWidth:         1080,
		MinHeight:        760,
		BackgroundColour: &options.RGBA{R: 243, G: 246, B: 250, A: 255},
		AssetServer:      &assetserver.Options{Assets: frontend},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.beforeClose,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.xiaomi.router.flash.assistant.v055",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if app.ctx != nil {
					wruntime.WindowUnminimise(app.ctx)
					wruntime.WindowShow(app.ctx)
				}
			},
		},
		Bind: []interface{}{app},
	}); err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("启动失败", "error", err)
	}
}
