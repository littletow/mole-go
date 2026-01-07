package main

import (
	"embed"
	_ "embed"
	"log"
	"log/slog"
	"runtime"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS // 调用前端资源

func init() {
	// Register a custom event whose associated data type is string.
	// This is not required, but the binding generator will pick up registered events
	// and provide a strongly typed JS/TS API for them.
	application.RegisterEvent[string]("time")
}

// AppManager 统一管理应用和窗口
type AppManager struct {
	App        *application.App
	MainWindow application.Window
}

var manager = &AppManager{}

// main function serves as the application's entry point. It initializes the application, creates a window,
// and starts a goroutine that emits a time-based event every second. It subsequently runs the application and
// logs any error that might occur.
func main() {

	ms := NewMoleService()
	// Create a new Wails application by providing the necessary options.
	// Variables 'Name' and 'Description' are for application metadata.
	// 'Assets' configures the asset server with the 'FS' variable pointing to the frontend files.
	// 'Bind' is a list of Go struct instances. The frontend has access to the methods of these instances.
	// 'Mac' options tailor the application when running an macOS.
	manager.App = application.New(application.Options{
		Name:        "FRP管理客户端",
		Description: "一个实现自动内网穿透的管理工具",
		LogLevel:    slog.LevelDebug,
		Services: []application.Service{
			application.NewService(ms),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		OnShutdown: func() {
			// 清理资源
			ms.cleanup()
		},
	})

	// Create a new window with the necessary options.
	// 'Title' is the title of the window.
	// 'Mac' options tailor the window when running on macOS.
	// 'BackgroundColour' is the background colour of the window.
	// 'URL' is the URL that will be loaded into the webview.
	// Create a new window with the necessary options.
	manager.MainWindow = manager.App.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          "main",
		Title:         "FRP 控制面板",
		Width:         875, // 设置宽度
		Height:        725, // 设置高度
		DisableResize: true,
		MaxWidth:      875,
		MaxHeight:     725,

		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
			// macOS 禁用缩放也会自动禁用全屏按钮
		},
		BackgroundColour: application.NewRGB(27, 38, 54),
		URL:              "/",
	})

	manager.MainWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		// Hide the window
		manager.MainWindow.Hide()
		// Cancel the event so it doesn't get destroyed
		e.Cancel()
	})

	// Create a goroutine that emits an event containing the current time every second.
	// The frontend can listen to this event and update the UI accordingly.
	go func() {
		for {
			now := time.Now().Format(time.RFC1123)
			manager.App.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	systemTray := manager.App.SystemTray.New()

	// Use the template icon on macOS so the clock respects light/dark modes.
	if runtime.GOOS == "darwin" {
		systemTray.SetTemplateIcon(icons.SystrayMacTemplate)
	}

	// 2. 定义左键点击逻辑：显示并聚焦窗口
	systemTray.OnClick(func() {
		if manager.MainWindow != nil {
			manager.MainWindow.Show()
			manager.MainWindow.Focus()
		}
	})

	// 3. 定义右键菜单
	menu := manager.App.NewMenu()
	menu.Add("显示窗口").OnClick(func(ctx *application.Context) {
		manager.MainWindow.Show()
		manager.MainWindow.Focus()
	})
	menu.AddSeparator() // 分割线
	menu.Add("退出").OnClick(func(ctx *application.Context) {
		manager.App.Quit()
	})

	systemTray.SetMenu(menu)

	// Run the application. This blocks until the application has been exited.
	err := manager.App.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}
