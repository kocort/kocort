// Package gateway — shared authentication helpers.
//
// ValidateTokenAuth centralises the bearer-token check that was previously
// duplicated in runtime.GatewayServer.wrapAuth and api.Server.authMiddleware.
package gateway

import (
	"strings"

	"github.com/kocort/kocort/internal/config"
)

// ValidateTokenAuth verifies the Authorization header against the gateway
// auth configuration. Returns true if the request is allowed through.
//
// Rules:
//   - nil config → always allow
//   - mode "" or "none" → always allow
//   - mode "token" → require a non-empty configured token that matches the
//     Bearer value in authHeader (case-sensitive)
func ValidateTokenAuth(cfg *config.GatewayAuthConfig, authHeader string) bool {
	if cfg == nil {
		return true
	}
	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" || mode == "none" {
		return true
	}
	if mode == "token" {
		token := strings.TrimSpace(cfg.Token)
		auth := strings.TrimPrefix(strings.TrimSpace(authHeader), "Bearer ")
		return token != "" && auth == token
	}
	return true
}
