package manager

// ── Command handlers (snapshot / wait) ──────────────────────────────────────

func (m *Manager) handleSnapshot(cmd *cmdSnapshot) {
	m.models = scanModels(m.modelsDir)

	models := make([]ModelInfo, len(m.models))
	copy(models, m.models)
	catalog := make([]ModelPreset, len(m.catalog))
	copy(catalog, m.catalog)

	var dlProg *DownloadProgress
	if m.dlProgress.PresetID != "" {
		cp := m.dlProgress
		if m.dlReporter != nil {
			cp.DownloadedBytes = m.dlReporter.Downloaded()
			if total := m.dlReporter.Total(); total > 0 {
				cp.TotalBytes = total
			}
			if fn := m.dlReporter.Filename(); fn != "" {
				cp.Filename = fn
			}
		}
		dlProg = &cp
	}

	cmd.reply <- Snapshot{
		Status:           m.status,
		ModelID:          m.modelID,
		Models:           models,
		LastError:        m.lastError,
		Catalog:          catalog,
		DownloadProgress: dlProg,
		Sampling:         m.sampling,
		Threads:          m.threads,
		ContextSize:      m.contextSize,
		GpuLayers:        m.gpuLayers,
		EnableThinking:   m.enableThinking,
	}
}

func (m *Manager) handleWaitReady(cmd *cmdWaitReady) {
	if !m.lifecycleBusy {
		cmd.reply <- m.status
		return
	}
	m.waiters = append(m.waiters, cmd.reply)
}

func (m *Manager) handleGetModels(cmd *cmdGetModels) {
	m.models = scanModels(m.modelsDir)
	out := make([]ModelInfo, len(m.models))
	copy(out, m.models)
	cmd.reply <- out
}
