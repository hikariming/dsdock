//go:build desktop

// Package desktop is DSDock's native app: an icon that stays in the menu
// bar, the quick panel under it, and the main window. It wraps the same
// pages as `dsdock gui`, served in-process by Wails: no network port is
// opened while the app runs.
//
// Built only with -tags desktop (it needs cgo and the platform webview);
// plain builds of dsdock stay pure Go.
package desktop

import (
	_ "embed"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/hikariming/dsdock/internal/core"
	"github.com/hikariming/dsdock/internal/gui"
)

//go:embed tray.png
var trayIcon []byte

const (
	panelWidth  = 400
	panelOffset = 6
)

// Options for Run.
type Options struct {
	Version string
	// Show opens the panel ("panel") or the main window ("main") once
	// started, for screenshots and for `dsdock app --show`.
	Show string
}

type host struct {
	app   *application.App
	panel *application.WebviewWindow
	main  *application.WebviewWindow
	tray  *application.SystemTray
}

// Open shows the main window on a page, from the panel or the tray menu.
func (h *host) Open(route string) {
	application.InvokeSync(func() {
		h.panel.Hide()
		h.main.SetURL("/#" + route)
		h.main.Show()
		h.main.Focus()
	})
}

// Quit ends the app; called from a request, so it must not block on it.
func (h *host) Quit() { application.InvokeAsync(h.app.Quit) }

// ResizePanel fits the panel to its content, keeping it under the icon.
func (h *host) ResizePanel(height int) {
	application.InvokeSync(func() {
		h.panel.SetSize(panelWidth, height)
		if h.panel.IsVisible() {
			_ = h.tray.PositionWindow(h.panel, panelOffset)
		}
	})
}

// Attention shows the panel under the icon: a quick prompt waits for the
// user to allow or reject an action.
func (h *host) Attention() {
	application.InvokeSync(func() {
		if !h.panel.IsVisible() {
			_ = h.tray.PositionWindow(h.panel, panelOffset)
			h.panel.Show()
		}
	})
}

// Run starts the app and blocks until it quits.
func Run(c *core.Core, opt Options) error {
	srv := gui.New(c, opt.Version)
	h := &host{}
	srv.Embed(h)

	h.app = application.New(application.Options{
		Name:        "DSDock",
		Description: "DeepSeek Harness 的外部管理器",
		OnShutdown:  srv.Close, // the DSH web DSDock started stops with it
		Assets:      application.AssetOptions{Handler: srv.Handler()},
		// A menu bar app: no Dock icon; closing the window keeps it running.
		Mac:   application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory},
		Linux: application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
	})

	h.panel = h.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "panel",
		Title:           "DSDock",
		URL:             "/#/panel",
		Width:           panelWidth,
		Height:          300,
		Hidden:          true,
		Frameless:       true,
		AlwaysOnTop:     true,
		DisableResize:   true,
		HideOnEscape:    true,
		HideOnFocusLost: true,
		BackgroundType:  application.BackgroundTypeTranslucent,
		Mac: application.MacWindow{
			Backdrop:     application.MacBackdropTranslucent,
			CornerRadius: 12,
			// On every Space, over full-screen apps too, and shown without
			// activating DSDock, as a menu bar extra's panel is (the same
			// settings Magpie arrived at; see its #2141).
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary,
			WindowClass:      application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{NonActivating: true},
		},
		Windows: application.WindowsWindow{HiddenOnTaskbar: true},
	})

	h.main = h.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "DSDock",
		URL:       "/#/",
		Width:     1080,
		Height:    760,
		MinWidth:  760,
		MinHeight: 520,
		Hidden:    true,
		Mac:       application.MacWindow{TitleBar: application.MacTitleBarHiddenInset},
	})
	// Closing the window hides it; the app stays in the menu bar.
	h.main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		h.main.Hide()
		e.Cancel()
	})

	menu := application.NewMenu()
	menu.Add("打开 DSDock").OnClick(func(*application.Context) { h.Open("/") })
	menu.Add("DSH 版本").OnClick(func(*application.Context) { h.Open("/versions") })
	menu.Add("打开 DSH Web").OnClick(func(*application.Context) { go srv.OpenWeb() })
	menu.AddSeparator()
	menu.Add("退出 DSDock").OnClick(func(*application.Context) { h.app.Quit() })

	h.tray = h.app.SystemTray.New()
	h.tray.SetTooltip("DSDock")
	if runtime.GOOS == "darwin" {
		h.tray.SetTemplateIcon(trayIcon) // recoloured for light and dark menu bars
	} else {
		h.tray.SetIcon(trayIcon)
	}
	h.tray.SetMenu(menu)
	// Left click toggles the panel under the icon; right click opens the menu.
	h.tray.AttachWindow(h.panel).WindowOffset(panelOffset)

	if opt.Show != "" {
		h.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			application.InvokeAsync(func() {
				switch opt.Show {
				case "main":
					h.main.Show()
					h.main.Focus()
				case "panel":
					_ = h.tray.PositionWindow(h.panel, panelOffset)
					h.panel.Show()
				}
			})
		})
	}
	return h.app.Run()
}
