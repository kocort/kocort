package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

const desktopStartupReadyTimeout = 20 * time.Second

func (a *appState) DashboardURL() string {
	return dashboardURL(a.Addr())
}

func openDashboardWhenReady(state *appState, timeout time.Duration, autoOpen bool, onReady func(string), onError func(error)) {
	url := state.DashboardURL()
	go func() {
		if err := waitForServerReady(url, timeout); err != nil {
			slog.Warn("dashboard readiness check failed", "url", url, "error", err)
			if onError != nil {
				onError(err)
			}
			return
		}

		slog.Info("dashboard is ready", "url", url)
		if onReady != nil {
			onReady(url)
		}
		if autoOpen {
			if err := openBrowser(url); err != nil {
				slog.Warn("failed to open dashboard", "url", url, "error", err)
			}
		}
	}()
}

func dashboardURL(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return "http://127.0.0.1:18789"
	}

	host, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		return "http://" + trimmed
	}

	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, port)
}

func waitForServerReady(baseURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{
		Timeout: 800 * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	target := strings.TrimRight(baseURL, "/") + "/"
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := probeServerReady(ctx, client, target); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s: %w", target, ctx.Err())
		case <-ticker.C:
		}
	}
}

func probeServerReady(ctx context.Context, client *http.Client, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
