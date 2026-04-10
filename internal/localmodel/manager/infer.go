package manager

import "fmt"

// ── Command handlers (inference) ────────────────────────────────────────────

func (m *Manager) handleInfer(cmd *cmdInfer) {
	if m.status != StatusRunning {
		cmd.reply <- inferResult{err: fmt.Errorf("local model is not running (status: %s)", m.status)}
		return
	}
	enableThinking := m.enableThinking
	// backend.CreateChatCompletionStream returns immediately with a channel;
	// actual streaming happens on the backend's own goroutine, so the actor
	// is not blocked.
	ch, err := m.backend.CreateChatCompletionStream(cmd.ctx, cmd.req, enableThinking)
	cmd.reply <- inferResult{ch: ch, err: err}
}
