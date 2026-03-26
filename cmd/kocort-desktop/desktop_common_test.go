package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDashboardURLUsesLoopbackForWildcardBind(t *testing.T) {
	t.Parallel()

	got := dashboardURL("0.0.0.0:18789")
	if got != "http://127.0.0.1:18789" {
		t.Fatalf("dashboardURL() = %q, want %q", got, "http://127.0.0.1:18789")
	}
}

func TestDashboardURLPreservesIPv6(t *testing.T) {
	t.Parallel()

	got := dashboardURL("[::1]:18789")
	if got != "http://[::1]:18789" {
		t.Fatalf("dashboardURL() = %q, want %q", got, "http://[::1]:18789")
	}
}

func TestWaitForServerReadySucceeds(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	if err := waitForServerReady(ts.URL, time.Second); err != nil {
		t.Fatalf("waitForServerReady() error = %v", err)
	}
}

func TestWaitForServerReadyTimesOut(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	err = waitForServerReady("http://"+addr, 300*time.Millisecond)
	if err == nil {
		t.Fatal("waitForServerReady() error = nil, want timeout")
	}
}
