package manager

import (
	"fmt"
	"log/slog"
)

// ── Command handlers (lifecycle) ────────────────────────────────────────────

func (m *Manager) handleStart(cmd *cmdStart) {
	if m.status == StatusRunning || m.status == StatusStarting {
		cmd.reply <- nil
		return
	}
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}
	if m.isStub {
		m.status = StatusError
		m.lastError = "local inference not available"
		m.syncAtomics()
		cmd.reply <- fmt.Errorf("local inference not available")
		return
	}
	if m.modelID == "" {
		m.status = StatusError
		m.lastError = "no model selected and no models available"
		m.syncAtomics()
		cmd.reply <- fmt.Errorf("no model selected")
		return
	}

	m.status = StatusStarting
	m.lastError = ""
	m.lifecycleBusy = true
	m.syncAtomics()
	cmd.reply <- nil // fast response: accepted

	modelPath := m.resolveModelPath()
	threads := m.threads
	contextSize := m.contextSize
	gpuLayers := m.gpuLayers
	sampling := m.sampling
	enableThinking := m.enableThinking
	backend := m.backend
	ch := m.cmdCh

	// Resolve companion mmproj file for vision support.
	mmprojPath := resolveMMProjPath(m.modelsDir, m.modelID, m.catalog)
	if mmprojPath != "" {
		slog.Info("[localmodel] found mmproj companion", "path", mmprojPath)
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[localmodel] Start panicked — recovered", "panic", r)
				ch <- &cmdLifecycleDone{err: fmt.Errorf("start panicked: %v", r), op: "start"}
			}
		}()
		err := backend.Start(modelPath, threads, contextSize, gpuLayers, sampling, enableThinking, mmprojPath)
		cs := 0
		if err == nil {
			cs = backend.ContextSize()
		}
		ch <- &cmdLifecycleDone{err: err, contextSize: cs, op: "start"}
	}()
}

func (m *Manager) handleStop(cmd *cmdStop) {
	if m.status == StatusStopped || m.status == StatusStopping {
		cmd.reply <- nil
		return
	}
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}

	m.status = StatusStopping
	m.lastError = ""
	m.lifecycleBusy = true
	m.syncAtomics()
	cmd.reply <- nil

	backend := m.backend
	ch := m.cmdCh

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[localmodel] Stop panicked — recovered", "panic", r)
				ch <- &cmdLifecycleDone{err: fmt.Errorf("stop panicked: %v", r), op: "stop"}
			}
		}()
		err := backend.Stop()
		ch <- &cmdLifecycleDone{err: err, op: "stop"}
	}()
}

func (m *Manager) handleRestart(cmd *cmdRestart) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}
	if m.status == StatusStopped || m.status == StatusError {
		// Not running — just start.
		m.handleStart(&cmdStart{reply: cmd.reply})
		return
	}

	m.status = StatusStopping
	m.lastError = ""
	m.lifecycleBusy = true
	m.syncAtomics()
	cmd.reply <- nil

	modelPath := m.resolveModelPath()
	threads := m.threads
	contextSize := m.contextSize
	gpuLayers := m.gpuLayers
	sampling := m.sampling
	enableThinking := m.enableThinking
	backend := m.backend
	ch := m.cmdCh

	// Resolve companion mmproj file for vision support.
	mmprojPath := resolveMMProjPath(m.modelsDir, m.modelID, m.catalog)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[localmodel] Restart panicked — recovered", "panic", r)
				ch <- &cmdLifecycleDone{err: fmt.Errorf("restart panicked: %v", r), op: "restart"}
			}
		}()
		// Phase 1: stop
		if err := backend.Stop(); err != nil {
			ch <- &cmdLifecycleDone{err: fmt.Errorf("stop during restart failed: %v", err), op: "restart"}
			return
		}
		// Notify actor to update observable status to "starting".
		ch <- &cmdStatusHint{status: StatusStarting}
		// Phase 2: start
		if err := backend.Start(modelPath, threads, contextSize, gpuLayers, sampling, enableThinking, mmprojPath); err != nil {
			ch <- &cmdLifecycleDone{err: fmt.Errorf("start during restart failed: %v", err), op: "restart"}
			return
		}
		cs := backend.ContextSize()
		ch <- &cmdLifecycleDone{err: nil, contextSize: cs, op: "restart"}
	}()
}

func (m *Manager) handleLifecycleDone(done *cmdLifecycleDone) {
	m.lifecycleBusy = false

	switch done.op {
	case "start":
		if done.err != nil {
			m.status = StatusError
			m.lastError = fmt.Sprintf("backend start failed: %v", done.err)
			slog.Error("[localmodel] backend start failed", "error", done.err)
		} else {
			if done.contextSize > 0 {
				m.contextSize = done.contextSize
			}
			m.status = StatusRunning
			slog.Info("[localmodel] model started")
		}
	case "stop":
		if done.err != nil {
			m.status = StatusError
			m.lastError = fmt.Sprintf("backend stop failed: %v", done.err)
			slog.Error("[localmodel] backend stop failed", "error", done.err)
		} else {
			m.status = StatusStopped
			slog.Info("[localmodel] model stopped")
		}
	case "restart":
		if done.err != nil {
			m.status = StatusError
			m.lastError = done.err.Error()
			slog.Error("[localmodel] restart failed", "error", done.err)
		} else {
			if done.contextSize > 0 {
				m.contextSize = done.contextSize
			}
			m.status = StatusRunning
			slog.Info("[localmodel] model restarted")
		}
	case "stop-for-pending":
		if done.err != nil {
			m.status = StatusError
			m.lastError = fmt.Sprintf("stop failed: %v", done.err)
			slog.Error("[localmodel] stop for pending op failed", "error", done.err)
		} else {
			m.status = StatusStopped
		}
	}
	m.syncAtomics()

	// Handle pending compound operation (delete-after-stop, clear-after-stop).
	if m.pendingAfterStop != nil {
		pending := m.pendingAfterStop
		m.pendingAfterStop = nil
		if done.err != nil {
			pending.reply <- fmt.Errorf("stop failed before %s: %w", pending.kind, done.err)
		} else {
			switch pending.kind {
			case "delete":
				m.executePendingDelete(pending)
			case "clear":
				m.modelID = ""
				m.lastError = ""
				m.syncAtomics()
				pending.reply <- nil
			}
		}
	}

	m.notifyWaiters()
}
