package manager

import (
	"fmt"
	"os"
)

// ── Command handlers (model selection / deletion) ───────────────────────────

func (m *Manager) handleSelectModel(cmd *cmdSelectModel) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}

	m.models = scanModels(m.modelsDir)
	found := false
	for _, model := range m.models {
		if model.ID == cmd.modelID {
			found = true
			break
		}
	}
	if !found {
		cmd.reply <- fmt.Errorf("model %q not found", cmd.modelID)
		return
	}

	wasRunning := m.status == StatusRunning
	m.modelID = cmd.modelID
	m.syncAtomics()

	if wasRunning {
		m.handleRestart(&cmdRestart{reply: cmd.reply})
		return
	}
	cmd.reply <- nil
}

func (m *Manager) handleClearModel(cmd *cmdClearModel) {
	if m.modelID == "" {
		cmd.reply <- nil
		return
	}
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}

	if m.status == StatusRunning || m.status == StatusStarting {
		// Need to stop first, then clear.
		m.status = StatusStopping
		m.lastError = ""
		m.lifecycleBusy = true
		m.syncAtomics()
		m.pendingAfterStop = &pendingOp{kind: "clear", reply: cmd.reply}
		backend := m.backend
		ch := m.cmdCh
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ch <- &cmdLifecycleDone{err: fmt.Errorf("stop panicked: %v", r), op: "stop-for-pending"}
				}
			}()
			err := backend.Stop()
			ch <- &cmdLifecycleDone{err: err, op: "stop-for-pending"}
		}()
		return
	}

	m.modelID = ""
	m.lastError = ""
	if m.status == StatusError {
		m.status = StatusStopped
	}
	m.syncAtomics()
	cmd.reply <- nil
}

func (m *Manager) handleDeleteModel(cmd *cmdDeleteModel) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}

	m.models = scanModels(m.modelsDir)
	found := false
	isSelected := m.modelID == cmd.modelID
	for _, model := range m.models {
		if model.ID == cmd.modelID {
			found = true
			break
		}
	}
	if !found {
		cmd.reply <- fmt.Errorf("model %q not found", cmd.modelID)
		return
	}
	if m.modelsDir == "" {
		cmd.reply <- fmt.Errorf("models directory is not configured")
		return
	}

	if isSelected && (m.status == StatusRunning || m.status == StatusStarting) {
		// Need to stop first, then delete.
		m.status = StatusStopping
		m.lastError = ""
		m.lifecycleBusy = true
		m.syncAtomics()
		m.pendingAfterStop = &pendingOp{kind: "delete", modelID: cmd.modelID, reply: cmd.reply}
		backend := m.backend
		ch := m.cmdCh
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ch <- &cmdLifecycleDone{err: fmt.Errorf("stop panicked: %v", r), op: "stop-for-pending"}
				}
			}()
			err := backend.Stop()
			ch <- &cmdLifecycleDone{err: err, op: "stop-for-pending"}
		}()
		return
	}

	if isSelected {
		m.modelID = ""
		m.lastError = ""
		if m.status == StatusError {
			m.status = StatusStopped
		}
		m.syncAtomics()
	}

	m.executePendingDelete(&pendingOp{kind: "delete", modelID: cmd.modelID, reply: cmd.reply})
}

func (m *Manager) executePendingDelete(pending *pendingOp) {
	if m.modelID == pending.modelID {
		m.modelID = ""
		m.syncAtomics()
	}

	modelPaths := installedModelFiles(m.modelsDir, pending.modelID)
	if len(modelPaths) == 0 {
		pending.reply <- fmt.Errorf("model file not found: %s", resolveInstalledModelPath(m.modelsDir, pending.modelID))
		return
	}
	for _, modelPath := range modelPaths {
		if err := os.Remove(modelPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			pending.reply <- fmt.Errorf("delete model file: %w", err)
			return
		}
	}
	m.models = scanModels(m.modelsDir)
	pending.reply <- nil
}
