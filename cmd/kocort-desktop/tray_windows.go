//go:build windows

package main

import (
	_ "embed"
	"log/slog"

	"github.com/energye/systray"
)

//go:embed tray.png
var trayIconData []byte

// runDesktop shows a Windows system-tray icon with basic controls.
func runDesktop(state *appState) {
	systray.Run(func() {
		onReady(state)
	}, func() {
		state.stopServer()
	})
}

func onReady(state *appState) {
	systray.SetIcon(trayIconData)
	systray.SetTitle("Kocort")
	systray.SetTooltip(T("tooltip.starting"))

	mStatus := systray.AddMenuItem(T("status.starting"), T("hint.status"))
	mStatus.Disable()
	systray.AddSeparator()

	mOpen := systray.AddMenuItem(T("menu.open_dashboard"), T("hint.open"))
	mOpen.Disable()
	systray.AddSeparator()
	mRestart := systray.AddMenuItem(T("menu.restart"), T("hint.restart"))
	systray.AddSeparator()
	mQuit := systray.AddMenuItem(T("menu.quit"), T("hint.quit"))

	mOpen.Click(func() {
		if err := openBrowser(state.DashboardURL()); err != nil {
			slog.Error("open dashboard failed", "error", err)
		}
	})

	openDashboardWhenReady(state, desktopStartupReadyTimeout, true, func(url string) {
		mStatus.SetTitle(T("status.running"))
		mOpen.Enable()
		systray.SetTooltip(Tf("tooltip.running", state.Addr()))
	}, func(err error) {
		mStatus.SetTitle(T("status.timeout"))
		systray.SetTooltip(T("tooltip.error"))
	})

	mRestart.Click(func() {
		slog.Info("restarting kocort server")
		mStatus.SetTitle(T("status.restarting"))
		mOpen.Disable()
		systray.SetTooltip(T("tooltip.starting"))
		if err := state.restartServer(); err != nil {
			mStatus.SetTitle(T("status.restart_failed"))
			systray.SetTooltip(T("tooltip.error"))
			slog.Error("restart failed", "error", err)
		} else {
			slog.Info("server restarted", "addr", state.Addr())
			openDashboardWhenReady(state, desktopStartupReadyTimeout, false, func(url string) {
				mStatus.SetTitle(T("status.running"))
				mOpen.Enable()
				systray.SetTooltip(Tf("tooltip.running", state.Addr()))
			}, func(err error) {
				mStatus.SetTitle(T("status.timeout"))
				systray.SetTooltip(T("tooltip.error"))
			})
		}
	})

	mQuit.Click(func() {
		systray.Quit()
	})
}
