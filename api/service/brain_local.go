package service

// Brain-local service: lifecycle management for the brain's local model mode.
// BrainMode "local" = pure local inference (no cloud, no cerebellum).
// BrainMode "cloud" = cloud brain + local cerebellum for safety review.

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/localmodel"
	"github.com/kocort/kocort/internal/localmodel/ffi"
	"github.com/kocort/kocort/runtime"
	"github.com/kocort/kocort/utils"
)

var errNoBrainLocal = errors.New("brain local model manager is not configured")

func brainLocalAutoStartEnabled(rt *runtime.Runtime) bool {
	if rt == nil {
		return false
	}
	return rt.Config.BrainLocal.AutoStart == nil || *rt.Config.BrainLocal.AutoStart
}

func persistBrainLocalAutoStart(rt *runtime.Runtime, enabled bool) error {
	return ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		cfg.BrainLocal.AutoStart = utils.BoolPtr(enabled)
		return ConfigSections{Main: true}, nil
	})
}

// BuildBrainLocalState builds the brain-local model state from the runtime.
func BuildBrainLocalState(rt *runtime.Runtime) *types.LocalModelState {
	if rt == nil || rt.BrainLocal == nil {
		return nil
	}
	enabled := rt.Config.BrainLocalEnabled()
	snap := rt.BrainLocal.Snapshot()
	models := make([]types.CerebellumModelInfo, len(snap.Models))
	for i, m := range snap.Models {
		models[i] = types.CerebellumModelInfo{
			ID:   m.ID,
			Name: m.Name,
			Size: m.Size,
			Capabilities: types.ModelCapabilities{
				Vision:    m.Capabilities.Vision,
				Audio:     m.Capabilities.Audio,
				Video:     m.Capabilities.Video,
				Tools:     m.Capabilities.Tools,
				Reasoning: m.Capabilities.Reasoning,
				Coding:    m.Capabilities.Coding,
			},
		}
	}
	var dlProgress *types.CerebellumDownloadProgress
	if snap.DownloadProgress != nil {
		dlProgress = &types.CerebellumDownloadProgress{
			PresetID:        snap.DownloadProgress.PresetID,
			Filename:        snap.DownloadProgress.Filename,
			TotalBytes:      snap.DownloadProgress.TotalBytes,
			DownloadedBytes: snap.DownloadProgress.DownloadedBytes,
			Active:          snap.DownloadProgress.Active,
			Canceled:        snap.DownloadProgress.Canceled,
			Error:           snap.DownloadProgress.Error,
		}
	}
	var libProgress *types.LibDownloadProgress
	libProg := ffi.GlobalLibDownloadTracker().Progress()
	if libProg.Active || libProg.Canceled || libProg.Error != "" {
		libProgress = &types.LibDownloadProgress{
			DownloadedBytes: libProg.DownloadedBytes,
			TotalBytes:      libProg.TotalBytes,
			Active:          libProg.Active,
			Canceled:        libProg.Canceled,
			Error:           libProg.Error,
		}
	}
	autoStart := brainLocalAutoStartEnabled(rt)
	sp := snap.Sampling
	return &types.LocalModelState{
		Enabled:             enabled,
		Status:              snap.Status,
		ModelID:             snap.ModelID,
		ModelsDir:           rt.Config.BrainLocal.ModelsDir,
		Models:              models,
		LastError:           snap.LastError,
		DownloadProgress:    dlProgress,
		LibDownloadProgress: libProgress,
		AutoStart:        autoStart,
		EnableThinking:   snap.EnableThinking,
		Sampling: &types.SamplingParams{
			Temp:           sp.Temp,
			TopP:           sp.TopP,
			TopK:           sp.TopK,
			MinP:           sp.MinP,
			TypicalP:       sp.TypicalP,
			RepeatLastN:    sp.RepeatLastN,
			PenaltyRepeat:  sp.PenaltyRepeat,
			PenaltyFreq:    sp.PenaltyFreq,
			PenaltyPresent: sp.PenaltyPresent,
		},
		Threads:     snap.Threads,
		ContextSize: snap.ContextSize,
		GpuLayers:   snap.GpuLayers,
	}
}

// BrainModeSwitch switches the brain between "cloud" and "local" modes.
// When switching to "local", auto-starts the brain local model if configured,
// stops the cerebellum (not needed in local mode), and overrides the agent
// backend to use the local model.
// When switching to "cloud", stops the brain local model, re-enables the
// cerebellum, and restores the normal cloud backend.
func BrainModeSwitch(rt *runtime.Runtime, mode string, cerebellumEnabled *bool) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode != "cloud" && mode != "local" {
		return errors.New("invalid brain mode: must be \"cloud\" or \"local\"")
	}

	if mode == "local" {
		recordBrainLocalEvent(rt, "brain_mode_switch", "info", "brain mode switched to local", nil)
		slog.Info("[brain] switching to local mode",
			"modelID", rt.BrainLocal.ModelID(),
			"status", rt.BrainLocal.Status())
		// Stop cerebellum — not needed in local mode.
		if rt.Cerebellum != nil {
			_ = rt.Cerebellum.Stop()
		}
		// Auto-start brain local model only when the persisted preference is enabled.
		if brainLocalAutoStartEnabled(rt) && rt.BrainLocal.ModelID() != "" &&
			rt.BrainLocal.Status() != localmodel.StatusRunning && rt.BrainLocal.Status() != localmodel.StatusStarting {
			if err := rt.BrainLocal.Start(); err != nil {
				slog.Warn("[brain] local model auto-start failed", "error", err)
				recordBrainLocalEvent(rt, "brain_local_autostart_failed", "warning",
					"brain local auto-start failed during mode switch: "+err.Error(), nil)
			} else {
				slog.Info("[brain] local model started successfully", "status", rt.BrainLocal.Status())
			}
		} else if rt.BrainLocal.ModelID() == "" {
			slog.Warn("[brain] no model selected for brain-local, inference will not work")
			recordBrainLocalEvent(rt, "brain_local_no_model", "warning",
				"brain mode switched to local but no model is selected", nil)
		} else if !brainLocalAutoStartEnabled(rt) {
			slog.Info("[brain] local mode enabled without auto-start; model remains stopped")
		}
	} else {
		// Stop brain-local model if running
		if rt.BrainLocal.Status() == localmodel.StatusRunning {
			_ = rt.BrainLocal.Stop()
		}
		recordBrainLocalEvent(rt, "brain_mode_switch", "info", "brain mode switched to cloud", nil)
	}
	return ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		cfg.BrainMode = mode
		// Apply explicit cerebellum enable/disable override for cloud mode.
		if mode == "cloud" && cerebellumEnabled != nil {
			cfg.Cerebellum.Enabled = cerebellumEnabled
		}
		// Restart cerebellum if switching to cloud and enabled in config.
		if mode == "cloud" && cfg.CerebellumEnabled() && rt.Cerebellum != nil {
			_ = rt.Cerebellum.Start()
		}
		// Stop cerebellum if switching to cloud but explicitly disabled.
		if mode == "cloud" && !cfg.CerebellumEnabled() && rt.Cerebellum != nil {
			_ = rt.Cerebellum.Stop()
		}
		return ConfigSections{Main: true}, nil
	})
}

// BrainLocalStart starts the brain local model.
func BrainLocalStart(rt *runtime.Runtime) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	err := rt.BrainLocal.Start()
	if err != nil {
		recordBrainLocalEvent(rt, "brain_local_start_failed", "error",
			"brain local model failed to start: "+err.Error(),
			map[string]any{"modelId": rt.BrainLocal.ModelID()})
		return err
	}
	recordBrainLocalEvent(rt, "brain_local_started", "info", "brain local model started",
		map[string]any{"modelId": rt.BrainLocal.ModelID()})
	return persistBrainLocalAutoStart(rt, true)
}

// BrainLocalStop stops the brain local model.
func BrainLocalStop(rt *runtime.Runtime) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	modelID := rt.BrainLocal.ModelID()
	err := rt.BrainLocal.Stop()
	if err != nil {
		recordBrainLocalEvent(rt, "brain_local_stop_failed", "error",
			"brain local model failed to stop: "+err.Error(),
			map[string]any{"modelId": modelID})
		return err
	}
	recordBrainLocalEvent(rt, "brain_local_stopped", "info", "brain local model stopped",
		map[string]any{"modelId": modelID})
	return persistBrainLocalAutoStart(rt, false)
}

// BrainLocalRestart restarts the brain local model.
func BrainLocalRestart(rt *runtime.Runtime) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	err := rt.BrainLocal.Restart()
	if err != nil {
		recordBrainLocalEvent(rt, "brain_local_restart_failed", "error",
			"brain local model failed to restart: "+err.Error(),
			map[string]any{"modelId": rt.BrainLocal.ModelID()})
		return err
	}
	recordBrainLocalEvent(rt, "brain_local_restarted", "info", "brain local model restarted",
		map[string]any{"modelId": rt.BrainLocal.ModelID()})
	return persistBrainLocalAutoStart(rt, true)
}

// BrainLocalSelectModel selects a model for the brain local manager.
func BrainLocalSelectModel(rt *runtime.Runtime, modelID string) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	previousModel := rt.BrainLocal.ModelID()
	if rt.Config.BrainLocal.EnableThinking == nil {
		rt.BrainLocal.SetEnableThinking(localmodel.ResolveEnableThinkingDefault(nil, modelID, rt.Config.BrainLocal.ModelsDir, localmodel.BuiltinCatalogPresets()))
	}
	if err := rt.BrainLocal.SelectModel(modelID); err != nil {
		recordBrainLocalEvent(rt, "brain_local_model_select_failed", "error",
			"brain local model selection failed: "+err.Error(),
			map[string]any{"modelId": modelID, "previousModelId": previousModel})
		return err
	}
	recordBrainLocalEvent(rt, "brain_local_model_selected", "info",
		"brain local model selected: "+modelID,
		map[string]any{"modelId": modelID, "previousModelId": previousModel})
	// Persist model selection in config.
	return ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		cfg.BrainLocal.ModelID = modelID
		return ConfigSections{Main: true}, nil
	})
}

// BrainLocalClearModelSelection clears the default brain-local model.
func BrainLocalClearModelSelection(rt *runtime.Runtime) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	previousModel := rt.BrainLocal.ModelID()
	if err := rt.BrainLocal.ClearSelectedModel(); err != nil {
		recordBrainLocalEvent(rt, "brain_local_model_clear_failed", "error",
			"brain local model clear failed: "+err.Error(),
			map[string]any{"previousModelId": previousModel})
		return err
	}
	if err := ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		cfg.BrainLocal.ModelID = ""
		return ConfigSections{Main: true}, nil
	}); err != nil {
		return err
	}
	recordBrainLocalEvent(rt, "brain_local_model_cleared", "info",
		"brain local default model cleared", map[string]any{"previousModelId": previousModel})
	return nil
}

// BrainLocalDeleteModel deletes a downloaded brain-local model.
func BrainLocalDeleteModel(rt *runtime.Runtime, modelID string) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	previousModel := rt.BrainLocal.ModelID()
	if err := rt.BrainLocal.DeleteModel(modelID); err != nil {
		recordBrainLocalEvent(rt, "brain_local_model_delete_failed", "error",
			"brain local model delete failed: "+err.Error(),
			map[string]any{"modelId": modelID, "previousModelId": previousModel})
		return err
	}
	if previousModel == modelID {
		if err := ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
			cfg.BrainLocal.ModelID = ""
			return ConfigSections{Main: true}, nil
		}); err != nil {
			return err
		}
	}
	recordBrainLocalEvent(rt, "brain_local_model_deleted", "info",
		"brain local model deleted", map[string]any{"modelId": modelID, "previousModelId": previousModel})
	return nil
}

// BrainLocalDownloadModel downloads a model from the built-in brain catalog.
func BrainLocalDownloadModel(rt *runtime.Runtime, presetID string) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	recordBrainLocalEvent(rt, "brain_local_download_started", "info",
		"brain local model download started: "+presetID,
		map[string]any{"presetId": presetID})

	if err := rt.BrainLocal.DownloadModel(presetID, nil); err != nil {
		recordBrainLocalEvent(rt, "brain_local_download_failed", "error",
			"brain local model download failed: "+err.Error(),
			map[string]any{"presetId": presetID})
		return err
	}
	recordBrainLocalEvent(rt, "brain_local_download_completed", "info",
		"brain local model download completed: "+presetID,
		map[string]any{"presetId": presetID})
	return nil
}

// BrainLocalCancelDownload cancels the active brain-local model download.
func BrainLocalCancelDownload(rt *runtime.Runtime) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}

	if err := rt.BrainLocal.CancelDownload(); err != nil {
		recordBrainLocalEvent(rt, "brain_local_download_cancel_failed", "error",
			"brain local model download cancel failed: "+err.Error(), nil)
		return err
	}

	recordBrainLocalEvent(rt, "brain_local_download_cancel_requested", "info",
		"brain local model download cancel requested", nil)
	return nil
}

// BrainLocalCancelLibDownload cancels the global library download.
func BrainLocalCancelLibDownload(rt *runtime.Runtime) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}
	ffi.GlobalLibDownloadTracker().Cancel()
	return nil
}

// BrainLocalUpdateParams updates sampling and runtime parameters for the brain local model.
// Parameters are applied to the running manager and persisted in config.
func BrainLocalUpdateParams(rt *runtime.Runtime, req types.LocalModelParamsUpdateRequest) error {
	if rt == nil || rt.BrainLocal == nil {
		return errNoBrainLocal
	}

	// Build sampling params (nil if not provided)
	var sp *localmodel.SamplingParams
	if req.Sampling != nil {
		sp = &localmodel.SamplingParams{
			Temp:           req.Sampling.Temp,
			TopP:           req.Sampling.TopP,
			TopK:           req.Sampling.TopK,
			MinP:           req.Sampling.MinP,
			TypicalP:       req.Sampling.TypicalP,
			RepeatLastN:    req.Sampling.RepeatLastN,
			PenaltyRepeat:  req.Sampling.PenaltyRepeat,
			PenaltyFreq:    req.Sampling.PenaltyFreq,
			PenaltyPresent: req.Sampling.PenaltyPresent,
		}
	}

	// Resolve runtime params (use current values as fallback)
	threads := rt.BrainLocal.Threads()
	contextSize := rt.BrainLocal.ContextSize()
	gpuLayers := rt.BrainLocal.GpuLayers()
	if req.Threads != nil {
		threads = *req.Threads
	}
	if req.ContextSize != nil {
		contextSize = *req.ContextSize
	}
	if req.GpuLayers != nil {
		gpuLayers = *req.GpuLayers
	}
	if req.EnableThinking != nil {
		rt.BrainLocal.SetEnableThinking(*req.EnableThinking)
	}

	// Apply all params atomically (at most one restart)
	if err := rt.BrainLocal.UpdateAllParams(sp, threads, contextSize, gpuLayers); err != nil {
		recordBrainLocalEvent(rt, "brain_local_params_failed", "error",
			"brain local params update failed: "+err.Error(), nil)
		return err
	}

	// Persist to config
	recordBrainLocalEvent(rt, "brain_local_params_updated", "info",
		"brain local model parameters updated", nil)
	return ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		if req.Sampling != nil {
			cfg.BrainLocal.Sampling = &config.SamplingConfig{
				Temp:           req.Sampling.Temp,
				TopP:           req.Sampling.TopP,
				TopK:           req.Sampling.TopK,
				MinP:           req.Sampling.MinP,
				TypicalP:       req.Sampling.TypicalP,
				RepeatLastN:    req.Sampling.RepeatLastN,
				PenaltyRepeat:  req.Sampling.PenaltyRepeat,
				PenaltyFreq:    req.Sampling.PenaltyFreq,
				PenaltyPresent: req.Sampling.PenaltyPresent,
			}
		}
		if req.Threads != nil {
			cfg.BrainLocal.Threads = *req.Threads
		}
		if req.ContextSize != nil {
			cfg.BrainLocal.ContextSize = *req.ContextSize
		}
		if req.GpuLayers != nil {
			cfg.BrainLocal.GpuLayers = *req.GpuLayers
		}
		if req.EnableThinking != nil {
			cfg.BrainLocal.EnableThinking = req.EnableThinking
		}
		return ConfigSections{Main: true}, nil
	})
}

func recordBrainLocalEvent(rt *runtime.Runtime, typ, level, message string, data map[string]any) {
	event.RecordAudit(context.Background(), rt.Audit, rt.Logger, core.AuditEvent{
		Category: "brain_local",
		Type:     typ,
		Level:    level,
		Message:  message,
		Data:     utils.CloneAnyMap(data),
	})
}
