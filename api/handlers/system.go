package handlers

// System HTTP handlers for dashboard, audit, and environment operations.

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kocort/kocort/api/service"
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/runtime"
)

// System holds dependencies for system handlers.
type System struct {
	Runtime *runtime.Runtime
}

// Dashboard handles GET /api/system/dashboard.
func (h *System) Dashboard(c *gin.Context) {
	snapshot, err := service.BuildDashboardSnapshot(c.Request.Context(), h.Runtime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

// AuditList handles POST /api/system/audit/list.
func (h *System) AuditList(c *gin.Context) {
	var req core.AuditListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.Runtime.Audit == nil {
		c.JSON(http.StatusOK, core.AuditListResponse{})
		return
	}
	events, err := h.Runtime.Audit.List(c.Request.Context(), core.AuditQuery{
		Category:   req.Category,
		Type:       req.Type,
		Level:      req.Level,
		Text:       req.Text,
		SessionKey: req.SessionKey,
		RunID:      req.RunID,
		TaskID:     req.TaskID,
		Limit:      req.Limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, core.AuditListResponse{Events: events})
}

// Environment handles GET /api/system/environment.
func (h *System) Environment(c *gin.Context) {
	c.JSON(http.StatusOK, service.BuildEnvironmentState(h.Runtime))
}

// EnvironmentSave handles POST /api/system/environment/save.
func (h *System) EnvironmentSave(c *gin.Context) {
	var req types.EnvironmentSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		cfg.Env = req.Environment
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildEnvironmentState(h.Runtime))
}

// EnvironmentReload handles POST /api/system/environment/reload.
func (h *System) EnvironmentReload(c *gin.Context) {
	h.Runtime.ReloadEnvironment()
	c.JSON(http.StatusOK, service.BuildEnvironmentState(h.Runtime))
}

// Network handles GET /api/system/network.
func (h *System) Network(c *gin.Context) {
	c.JSON(http.StatusOK, types.NetworkState{
		UseSystemProxy: h.Runtime.Config.Network.UseSystemProxyEnabled(),
		ProxyURL:       h.Runtime.Config.Network.ProxyURL,
		Language:       h.Runtime.Config.Network.LanguageOrDefault(),
	})
}

// NetworkSave handles POST /api/system/network/save.
func (h *System) NetworkSave(c *gin.Context) {
	var req types.NetworkSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		cfg.Network.UseSystemProxy = &req.UseSystemProxy
		cfg.Network.ProxyURL = strings.TrimSpace(req.ProxyURL)
		cfg.Network.Language = strings.TrimSpace(strings.ToLower(req.Language))
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, types.NetworkState{
		UseSystemProxy: h.Runtime.Config.Network.UseSystemProxyEnabled(),
		ProxyURL:       h.Runtime.Config.Network.ProxyURL,
		Language:       h.Runtime.Config.Network.LanguageOrDefault(),
	})
}
