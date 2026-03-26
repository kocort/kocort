// Package main provides the desktop (tray/menubar) entry point for kocort.
// On Windows it shows a system-tray icon; on macOS it is typically wrapped
// by the native Swift AppKit shell (see desktop/macos); on Linux it falls
// back to a plain headless server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/kocort/kocort/api"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/runtime"
)

// appState holds the running server state so that platform-specific tray code
// can restart or stop it.
type appState struct {
	mu     sync.Mutex
	server *api.Server
	rt     *runtime.Runtime
	cancel context.CancelFunc
	addr   string
}

// Addr returns the current server listen address.
func (a *appState) Addr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr
}

// startServer boots the kocort runtime + HTTP gateway in the background.
// It returns the listening address (e.g. "127.0.0.1:18789").
func (a *appState) startServer() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	configDir := resolveConfigDir()
	loadOpts := config.ConfigLoadOptions{
		ConfigDir: configDir,
	}
	cfg, err := config.LoadRuntimeConfig(config.DefaultConfigJSON(), loadOpts)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	config.ResolveConfigPaths(&cfg, configDir)

	rt, err := runtime.NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		AgentID:    "main",
		Deliverer:  &delivery.StdoutDeliverer{},
		ConfigLoad: loadOpts,
	})
	if err != nil {
		return fmt.Errorf("build runtime: %w", err)
	}

	// webhook := generic.NewGenericJSONChannelAdapter("webhook")
	// rt.Channels.RegisterIntegration(channelPkg.ChannelIntegration{
	// 	ID:           webhook.ID(),
	// 	Transport:    webhook,
	// 	Outbound:     webhook,
	// 	Capabilities: webhook,
	// })

	srv := api.NewServer(rt, cfg.Gateway)
	a.addr = srv.Addr()
	a.rt = rt
	a.server = srv

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	go func() {
		if err := srv.Start(ctx); err != nil {
			slog.Error("server error", "error", err)
		}
	}()

	return nil
}

// stopServer gracefully shuts down the running server.
func (a *appState) stopServer() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
}

// restartServer stops then starts the server again.
func (a *appState) restartServer() error {
	a.stopServer()
	return a.startServer()
}

// resolveConfigDir returns the config directory for the desktop app.
// Desktop apps use ~/.kocort as the default so configuration stays in a
// predictable, user-visible location across all platforms.
func resolveConfigDir() string {
	return config.ResolveDesktopConfigDir()
}

func main() {
	state := &appState{}

	if err := state.startServer(); err != nil {
		slog.Error("failed to start kocort", "error", err)
		os.Exit(1)
	}

	slog.Info("kocort server started", "addr", state.Addr())

	// Platform-specific tray / menubar integration.
	// On platforms without a tray implementation this just blocks on signals.
	runDesktop(state)
}

// fallbackWaitForExit blocks until SIGINT/SIGTERM is received.
// Used by platforms that don't have a tray implementation.
func fallbackWaitForExit(state *appState) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("shutting down")
	state.stopServer()
}
