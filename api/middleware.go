package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	gw "github.com/kocort/kocort/internal/gateway"
)

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !gw.ValidateTokenAuth(s.Config.Auth, c.GetHeader("Authorization")) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if allowedOrigin := s.allowedOrigin(origin); allowedOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *Server) allowedOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	webchat := s.Config.Webchat
	if webchat == nil || len(webchat.AllowedOrigins) == 0 {
		if strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:") ||
			strings.HasPrefix(origin, "https://127.0.0.1:") || strings.HasPrefix(origin, "https://localhost:") {
			return origin
		}
		return ""
	}
	for _, candidate := range webchat.AllowedOrigins {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.EqualFold(candidate, origin) {
			return origin
		}
	}
	return ""
}

func (s *Server) webchatEnabled() bool {
	if s.Config.Webchat == nil || s.Config.Webchat.Enabled == nil {
		return true
	}
	return *s.Config.Webchat.Enabled
}
