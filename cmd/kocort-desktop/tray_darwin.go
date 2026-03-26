//go:build darwin

package main

import "log/slog"

// On macOS the recommended approach is the native Swift AppKit wrapper
// (see desktop/macos/). This Go build is a fallback that simply runs
// the server headlessly — the Swift shell launches this binary and
// provides the menubar icon itself.
//
// If you DO want a pure-Go menubar icon on macOS (without App Store
// support), you can replace this file with a systray-based implementation
// similar to tray_windows.go.

func runDesktop(state *appState) {
	go func() {
		url := state.DashboardURL()
		if err := waitForServerReady(url, desktopStartupReadyTimeout); err != nil {
			slog.Warn("dashboard readiness check failed", "url", url, "error", err)
			return
		}
		if err := openBrowser(url); err != nil {
			slog.Warn("failed to open dashboard", "url", url, "error", err)
		}
	}()
	// No Go-level tray on macOS — the Swift wrapper or the user's
	// terminal provides the UI. Just block until we're asked to stop.
	fallbackWaitForExit(state)
}
