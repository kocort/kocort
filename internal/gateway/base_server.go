// Package gateway — shared HTTP server utilities.
//
// ResolveAddr and ListenAndServe eliminate the duplicated Addr()/Start()
// boilerplate that previously existed in both runtime.GatewayServer and
// api.Server.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ResolveAddr builds a "host:port" listen address from config values.
// An empty or "loopback" bind string resolves to "127.0.0.1".
// A non-positive port resolves to the default gateway port 18789.
func ResolveAddr(bind string, port int) string {
	bind = strings.TrimSpace(bind)
	if bind == "" || bind == "loopback" {
		bind = "127.0.0.1"
	}
	if port <= 0 {
		port = 18789
	}
	return fmt.Sprintf("%s:%d", bind, port)
}

// ListenAndServe starts an HTTP server on addr and blocks until ctx is
// cancelled or the server returns an error. A graceful 5-second shutdown is
// attempted after ctx is done. http.ErrServerClosed is swallowed and treated
// as a clean exit.
func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx) // best-effort; graceful shutdown failure is non-critical
	}()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
