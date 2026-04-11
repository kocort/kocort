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
	"github.com/kocort/kocort/internal/localmodel/ffi"
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

// LlamaCpp handles GET /api/system/llamacpp.
func (h *System) LlamaCpp(c *gin.Context) {
	c.JSON(http.StatusOK, h.buildLlamaCppState())
}

// LlamaCppSave handles POST /api/system/llamacpp/save.
func (h *System) LlamaCppSave(c *gin.Context) {
	var req types.LlamaCppSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	version := strings.TrimSpace(req.Version)
	gpuType := strings.TrimSpace(strings.ToLower(req.GPUType))

	// Update the runtime config and persist
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		cfg.LlamaCpp.Version = version
		cfg.LlamaCpp.GPUType = gpuType
		return service.ConfigSections{Main: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update the ffi config for future downloads/init
	ffi.SetLibraryConfig(version, gpuType, ffi.DefaultCacheDir(), h.Runtime.HTTPClient.Client())

	// If libraries for the new variant already exist, reinit the backend
	// immediately so the GPU switch takes effect without a restart.
	if ffi.CheckLibrariesExist(ffi.DownloadConfig{Version: version, GPUType: gpuType}) {
		_ = ffi.BackendReinit()
	}

	// Return updated state
	c.JSON(http.StatusOK, h.buildLlamaCppState())
}

// LlamaCppDownload handles POST /api/system/llamacpp/download.
// Starts an async library download. Progress is polled via GET /api/system/llamacpp.
func (h *System) LlamaCppDownload(c *gin.Context) {
	var req types.LlamaCppSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = h.Runtime.Config.LlamaCpp.Version
	}
	if version == "" {
		version = ffi.LlamaCppVersion
	}
	gpuType := strings.TrimSpace(strings.ToLower(req.GPUType))
	if gpuType == "" {
		gpuType = h.Runtime.Config.LlamaCpp.GPUType
	}

	err := ffi.StartLibDownload(ffi.DownloadConfig{
		Version:    version,
		GPUType:    gpuType,
		HTTPClient: h.Runtime.HTTPClient.Client(),
	})
	if err != nil && err != ffi.ErrLibDLActive {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Return current state (includes active download progress even if already running).
	c.JSON(http.StatusOK, h.buildLlamaCppState())
}

// LlamaCppDownloadCancel handles POST /api/system/llamacpp/download/cancel.
func (h *System) LlamaCppDownloadCancel(c *gin.Context) {
	ffi.GlobalLibDownloadTracker().Cancel()
	c.JSON(http.StatusOK, h.buildLlamaCppState())
}

// buildLlamaCppState constructs the full LlamaCppState API response.
func (h *System) buildLlamaCppState() types.LlamaCppState {
	version, gpuType, libDir, loaded := ffi.LibraryStatus()
	state := types.LlamaCppState{
		Version:            version,
		GPUType:            gpuType,
		DetectedGPUType:    ffi.DetectBestGPU(),
		LibDir:             libDir,
		Loaded:             loaded,
		DefaultVersion:     ffi.LlamaCppVersion,
		DownloadedVariants: buildVariantsList(),
		AvailableGPUTypes:  ffi.AvailableGPUTypes(),
	}

	dl := ffi.GlobalLibDownloadTracker().Progress()
	if dl.Active || dl.Error != "" || dl.Canceled {
		state.DownloadProgress = &types.LlamaCppDownloadProgress{
			Version:         dl.Version,
			GPUType:         dl.GPUType,
			Active:          dl.Active,
			Canceled:        dl.Canceled,
			Error:           dl.Error,
			DownloadedBytes: dl.DownloadedBytes,
			TotalBytes:      dl.TotalBytes,
		}
	}

	return state
}

// buildVariantsList converts ffi.LibVariant list to API types.
func buildVariantsList() []types.LlamaCppVariant {
	ffiVariants := ffi.ListDownloadedVariants("")
	variants := make([]types.LlamaCppVariant, len(ffiVariants))
	for i, v := range ffiVariants {
		variants[i] = types.LlamaCppVariant{Version: v.Version, GPUType: v.GPUType}
	}
	return variants
}
