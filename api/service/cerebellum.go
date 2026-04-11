package service

// Cerebellum service: lifecycle management and help queries for the local
// cerebellum model (小脑). Review is integrated into the agent architecture
// and not exposed as a standalone HTTP endpoint; help is exposed at
// POST /api/engine/brain/cerebellum/help.

import (
	"context"

	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/cerebellum"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/localmodel"
	"github.com/kocort/kocort/internal/localmodel/ffi"
	"github.com/kocort/kocort/runtime"
	"github.com/kocort/kocort/utils"
)

func cerebellumAutoStartEnabled(rt *runtime.Runtime) bool {
	if rt == nil {
		return false
	}
	return rt.Config.Cerebellum.AutoStart == nil || *rt.Config.Cerebellum.AutoStart
}

func persistCerebellumAutoStart(rt *runtime.Runtime, enabled bool) error {
	return ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		cfg.Cerebellum.AutoStart = utils.BoolPtr(enabled)
		return ConfigSections{Main: true}, nil
	})
}

// BuildCerebellumState builds the cerebellum state from the runtime.
func BuildCerebellumState(rt *runtime.Runtime) *types.CerebellumState {
	if rt == nil || rt.Cerebellum == nil {
		return nil
	}
	enabled := rt.Config.CerebellumEnabled()
	snap := rt.Cerebellum.Snapshot(enabled)
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
	catalog := make([]types.CerebellumModelPreset, len(snap.Catalog))
	for i, p := range snap.Catalog {
		caps := p.CapabilitiesResolved()
		catalog[i] = types.CerebellumModelPreset{
			ID:          p.ID,
			ModelID:     p.ModelID(),
			Name:        p.Name,
			Description: cloneLocalizedText(p.Description),
			Size:        p.Size,
			DownloadURL: p.DownloadURL,
			Filename:    p.Filename,
			Capabilities: types.ModelCapabilities{
				Vision:    caps.Vision,
				Audio:     caps.Audio,
				Video:     caps.Video,
				Tools:     caps.Tools,
				Reasoning: caps.Reasoning,
				Coding:    caps.Coding,
			},
		}
		if p.Defaults != nil {
			catalog[i].Defaults = &types.ModelPresetDefaults{
				Threads:     p.Defaults.Threads,
				ContextSize: p.Defaults.ContextSize,
				GpuLayers:   p.Defaults.GpuLayers,
			}
			if p.Defaults.Sampling != nil {
				catalog[i].Defaults.Sampling = &types.SamplingParams{
					Temp:           p.Defaults.Sampling.Temp,
					TopP:           p.Defaults.Sampling.TopP,
					TopK:           p.Defaults.Sampling.TopK,
					MinP:           p.Defaults.Sampling.MinP,
					TypicalP:       p.Defaults.Sampling.TypicalP,
					RepeatLastN:    p.Defaults.Sampling.RepeatLastN,
					PenaltyRepeat:  p.Defaults.Sampling.PenaltyRepeat,
					PenaltyFreq:    p.Defaults.Sampling.PenaltyFreq,
					PenaltyPresent: p.Defaults.Sampling.PenaltyPresent,
				}
			}
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
	autoStart := cerebellumAutoStartEnabled(rt)
	return &types.CerebellumState{
		Enabled:          snap.Enabled,
		Status:           snap.Status,
		ModelID:          snap.ModelID,
		ModelsDir:        rt.Config.Cerebellum.ModelsDir,
		Models:           models,
		Catalog:          catalog,
		LastError:        snap.LastError,
		DownloadProgress: dlProgress,
		LibDownloadProgress: libProgress,
		AutoStart:        autoStart,
		Sampling: &types.SamplingParams{
			Temp:           snap.Sampling.Temp,
			TopP:           snap.Sampling.TopP,
			TopK:           snap.Sampling.TopK,
			MinP:           snap.Sampling.MinP,
			TypicalP:       snap.Sampling.TypicalP,
			RepeatLastN:    snap.Sampling.RepeatLastN,
			PenaltyRepeat:  snap.Sampling.PenaltyRepeat,
			PenaltyFreq:    snap.Sampling.PenaltyFreq,
			PenaltyPresent: snap.Sampling.PenaltyPresent,
		},
		Threads:     snap.Threads,
		ContextSize: snap.ContextSize,
		GpuLayers:   snap.GpuLayers,
	}
}

// CerebellumStart starts the cerebellum model.
func CerebellumStart(rt *runtime.Runtime) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}
	err := rt.Cerebellum.Start()
	if err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_start_failed", "error",
			"cerebellum failed to start: "+err.Error(),
			map[string]any{"modelId": rt.Cerebellum.ModelID()})
		return err
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_started", "info",
		"cerebellum started",
		map[string]any{"modelId": rt.Cerebellum.ModelID()})
	return persistCerebellumAutoStart(rt, true)
}

// CerebellumStop stops the cerebellum model.
func CerebellumStop(rt *runtime.Runtime) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}
	modelID := rt.Cerebellum.ModelID()
	err := rt.Cerebellum.Stop()
	if err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_stop_failed", "error",
			"cerebellum failed to stop: "+err.Error(),
			map[string]any{"modelId": modelID})
		return err
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_stopped", "info",
		"cerebellum stopped",
		map[string]any{"modelId": modelID})
	return persistCerebellumAutoStart(rt, false)
}

// CerebellumRestart restarts the cerebellum model.
func CerebellumRestart(rt *runtime.Runtime) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}
	err := rt.Cerebellum.Restart()
	if err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_restart_failed", "error",
			"cerebellum failed to restart: "+err.Error(),
			map[string]any{"modelId": rt.Cerebellum.ModelID()})
		return err
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_restarted", "info",
		"cerebellum restarted",
		map[string]any{"modelId": rt.Cerebellum.ModelID()})
	return persistCerebellumAutoStart(rt, true)
}

// CerebellumSelectModel selects a model for the cerebellum.
func CerebellumSelectModel(rt *runtime.Runtime, modelID string) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}
	previousModel := rt.Cerebellum.ModelID()
	if err := rt.Cerebellum.SelectModel(modelID); err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_model_select_failed", "error",
			"cerebellum model selection failed: "+err.Error(),
			map[string]any{"modelId": modelID, "previousModelId": previousModel})
		return err
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_model_selected", "info",
		"cerebellum model selected: "+modelID,
		map[string]any{"modelId": modelID, "previousModelId": previousModel})
	// Persist model selection in config.
	return ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		cfg.Cerebellum.ModelID = modelID
		return ConfigSections{Main: true}, nil
	})
}

// CerebellumClearModelSelection clears the default cerebellum model.
func CerebellumClearModelSelection(rt *runtime.Runtime) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}
	previousModel := rt.Cerebellum.ModelID()
	if err := rt.Cerebellum.ClearSelectedModel(); err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_model_clear_failed", "error",
			"cerebellum model clear failed: "+err.Error(),
			map[string]any{"previousModelId": previousModel})
		return err
	}
	if err := ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		cfg.Cerebellum.ModelID = ""
		return ConfigSections{Main: true}, nil
	}); err != nil {
		return err
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_model_cleared", "info",
		"cerebellum default model cleared",
		map[string]any{"previousModelId": previousModel})
	return nil
}

// CerebellumDeleteModel deletes a downloaded cerebellum model.
func CerebellumDeleteModel(rt *runtime.Runtime, modelID string) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}
	previousModel := rt.Cerebellum.ModelID()
	if err := rt.Cerebellum.DeleteModel(modelID); err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_model_delete_failed", "error",
			"cerebellum model delete failed: "+err.Error(),
			map[string]any{"modelId": modelID, "previousModelId": previousModel})
		return err
	}
	if previousModel == modelID {
		if err := ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
			cfg.Cerebellum.ModelID = ""
			return ConfigSections{Main: true}, nil
		}); err != nil {
			return err
		}
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_model_deleted", "info",
		"cerebellum model deleted",
		map[string]any{"modelId": modelID, "previousModelId": previousModel})
	return nil
}

// CerebellumDownloadModel downloads a model from the built-in catalog.
func CerebellumDownloadModel(rt *runtime.Runtime, presetID string) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_download_started", "info",
		"cerebellum model download started: "+presetID,
		map[string]any{"presetId": presetID})

	if err := rt.Cerebellum.DownloadModel(presetID, nil); err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_download_failed", "error",
			"cerebellum model download failed: "+err.Error(),
			map[string]any{"presetId": presetID})
		return err
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_download_completed", "info",
		"cerebellum model download completed: "+presetID,
		map[string]any{"presetId": presetID})
	return nil
}

// CerebellumCancelDownload cancels the active cerebellum model download.
func CerebellumCancelDownload(rt *runtime.Runtime) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}

	if err := rt.Cerebellum.CancelDownload(); err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_download_cancel_failed", "error",
			"cerebellum model download cancel failed: "+err.Error(), nil)
		return err
	}

	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_download_cancel_requested", "info",
		"cerebellum model download cancel requested", nil)
	return nil
}

// CerebellumCancelLibDownload cancels the global library download.
func CerebellumCancelLibDownload(rt *runtime.Runtime) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
	}
	ffi.GlobalLibDownloadTracker().Cancel()
	return nil
}

// CerebellumHelp processes a natural-language configuration query via the
// local cerebellum model and returns actionable configuration suggestions.
func CerebellumHelp(rt *runtime.Runtime, query, helpContext string) (cerebellum.HelpResult, error) {
	if rt == nil || rt.Cerebellum == nil {
		return cerebellum.HelpResult{}, errNoCerebellum
	}
	result, err := rt.Cerebellum.Help(cerebellum.HelpRequest{
		Query:   query,
		Context: helpContext,
	})
	if err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_help_failed", "error",
			"cerebellum help query failed: "+err.Error(),
			map[string]any{"query": query})
		return cerebellum.HelpResult{}, err
	}
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_help", "info",
		"cerebellum help query answered",
		map[string]any{"query": query})
	return result, nil
}

var errNoCerebellum = cerebellum.ErrNotConfigured

// CerebellumUpdateParams updates sampling and runtime parameters for the cerebellum.
// Parameters are applied to the running manager and persisted in config.
func CerebellumUpdateParams(rt *runtime.Runtime, req types.LocalModelParamsUpdateRequest) error {
	if rt == nil || rt.Cerebellum == nil {
		return errNoCerebellum
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
	threads := rt.Cerebellum.Threads()
	contextSize := rt.Cerebellum.ContextSize()
	gpuLayers := rt.Cerebellum.GpuLayers()
	if req.Threads != nil {
		threads = *req.Threads
	}
	if req.ContextSize != nil {
		contextSize = *req.ContextSize
	}
	if req.GpuLayers != nil {
		gpuLayers = *req.GpuLayers
	}

	// Apply all params atomically (at most one restart)
	if err := rt.Cerebellum.UpdateAllParams(sp, threads, contextSize, gpuLayers); err != nil {
		event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
			"cerebellum_params_failed", "error",
			"cerebellum params update failed: "+err.Error(), nil)
		return err
	}

	// Persist to config
	event.RecordCerebellumEvent(context.Background(), rt.Audit, rt.Logger,
		"cerebellum_params_updated", "info",
		"cerebellum model parameters updated", nil)
	return ModifyAndPersist(rt, func(cfg *config.AppConfig) (ConfigSections, error) {
		if req.Sampling != nil {
			cfg.Cerebellum.Sampling = &config.SamplingConfig{
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
			cfg.Cerebellum.Threads = *req.Threads
		}
		if req.ContextSize != nil {
			cfg.Cerebellum.ContextSize = *req.ContextSize
		}
		if req.GpuLayers != nil {
			cfg.Cerebellum.GpuLayers = *req.GpuLayers
		}
		return ConfigSections{Main: true}, nil
	})
}
