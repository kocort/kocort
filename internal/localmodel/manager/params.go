package manager

import "fmt"

// ── Command handlers (params) ───────────────────────────────────────────────

func (m *Manager) handleUpdateAllParams(cmd *cmdUpdateAllParams) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s), try again after it completes", m.status)
		return
	}

	needsRestart := false

	if cmd.sp != nil {
		m.sampling = *cmd.sp
		m.backend.SetSamplingParams(m.sampling)
		if m.status == StatusRunning {
			needsRestart = true
		}
	}

	if cmd.threads != m.threads || cmd.contextSize != m.contextSize || cmd.gpuLayers != m.gpuLayers {
		m.threads = cmd.threads
		m.contextSize = cmd.contextSize
		m.gpuLayers = cmd.gpuLayers
		if m.status == StatusRunning {
			needsRestart = true
		}
	}

	if needsRestart {
		m.handleRestart(&cmdRestart{reply: cmd.reply})
		return
	}
	cmd.reply <- nil
}

func (m *Manager) handleSetSamplingParams(cmd *cmdSetSamplingParams) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}
	m.sampling = cmd.sp
	m.backend.SetSamplingParams(m.sampling)
	if m.status == StatusRunning {
		m.handleRestart(&cmdRestart{reply: cmd.reply})
		return
	}
	cmd.reply <- nil
}

func (m *Manager) handleUpdateRuntimeParams(cmd *cmdUpdateRuntimeParams) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}
	m.threads = cmd.threads
	m.contextSize = cmd.contextSize
	m.gpuLayers = cmd.gpuLayers
	if m.status == StatusRunning {
		m.handleRestart(&cmdRestart{reply: cmd.reply})
		return
	}
	cmd.reply <- nil
}
