// Pure command-backend helper utilities.
//
// Output parsing, map cloning, type coercion, and the output watchdog timer.
// None of these reference *Runtime, AgentRunContext, or ToolContext.
package backend

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// Generic helpers
// ---------------------------------------------------------------------------

// CloneAnyMap returns a shallow copy of a map[string]any.
// Delegates to utils.CloneAnyMap.
func CloneAnyMap(input map[string]any) map[string]any {
	return utils.CloneAnyMap(input)
}

// AsString performs a type assertion to string, returning "" on failure.
func AsString(value any) string {
	raw, _ := value.(string) // zero value fallback is intentional
	return raw
}

// AsBool performs a type assertion to bool, returning false on failure.
func AsBool(value any) bool {
	raw, _ := value.(bool) // zero value fallback is intentional
	return raw
}

// MustDecodeMap decodes JSON bytes into a map, swallowing errors.
func MustDecodeMap(output []byte) map[string]any {
	var decoded map[string]any
	_ = json.Unmarshal(output, &decoded) // best-effort; nil map fallback is acceptable
	return decoded
}

// ---------------------------------------------------------------------------
// Session ID extraction
// ---------------------------------------------------------------------------

// ExtractSessionIDFromMap extracts a session/conversation/thread ID from a map
// by checking a list of candidate field names.
func ExtractSessionIDFromMap(raw map[string]any, fields []string) string {
	candidates := fields
	if len(candidates) == 0 {
		candidates = []string{"session_id", "sessionId", "conversation_id", "conversationId", "thread_id"}
	}
	for _, field := range candidates {
		if value := strings.TrimSpace(AsString(raw[field])); value != "" {
			return value
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Command JSON output parsing
// ---------------------------------------------------------------------------

// ParseCommandJSONOutput parses JSON output from a command backend process
// into an AgentRunResult.
func ParseCommandJSONOutput(output []byte, cfg core.CommandBackendConfig) (core.AgentRunResult, error) {
	var payload struct {
		Text         string              `json:"text"`
		Payloads     []core.ReplyPayload `json:"payloads"`
		Usage        map[string]any      `json:"usage"`
		SessionID    string              `json:"session_id"`
		SessionIDAlt string              `json:"sessionId"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return core.AgentRunResult{}, err
	}
	result := core.AgentRunResult{Usage: payload.Usage}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(payload.SessionIDAlt)
	}
	if sessionID == "" {
		sessionID = ExtractSessionIDFromMap(MustDecodeMap(output), cfg.SessionIDFields)
	}
	if sessionID != "" {
		if result.Usage == nil {
			result.Usage = map[string]any{}
		}
		result.Usage["sessionId"] = sessionID
	}
	if len(payload.Payloads) > 0 {
		result.Payloads = payload.Payloads
		result.StopReason = strings.TrimSpace(AsString(MustDecodeMap(output)["stopReason"]))
		return result, nil
	}
	if strings.TrimSpace(payload.Text) != "" {
		result.Payloads = []core.ReplyPayload{{Text: strings.TrimSpace(payload.Text)}}
	}
	result.StopReason = strings.TrimSpace(AsString(MustDecodeMap(output)["stopReason"]))
	return result, nil
}

// ---------------------------------------------------------------------------
// CommandOutputWatchdog — cancels a context after a period of inactivity.
// ---------------------------------------------------------------------------

// CommandOutputWatchdog monitors output activity and cancels a context
// when no output has been produced for the configured timeout.
type CommandOutputWatchdog struct {
	mu       sync.Mutex
	timer    *time.Timer
	timedOut bool
	cancel   context.CancelFunc
	stopped  bool
	timeout  time.Duration
}

// NewCommandOutputWatchdog creates a watchdog that will call cancel after
// timeout of inactivity.
func NewCommandOutputWatchdog(_ context.Context, timeout time.Duration, cancel context.CancelFunc) *CommandOutputWatchdog {
	w := &CommandOutputWatchdog{
		timeout: timeout,
		cancel:  cancel,
	}
	w.timer = time.AfterFunc(timeout, func() {
		w.mu.Lock()
		if w.stopped {
			w.mu.Unlock()
			return
		}
		w.timedOut = true
		w.mu.Unlock()
		cancel()
	})
	return w
}

// Touch resets the inactivity timer.
func (w *CommandOutputWatchdog) Touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.timer == nil {
		return
	}
	if !w.timer.Stop() {
		select {
		case <-w.timer.C:
		default:
		}
	}
	w.timer.Reset(w.timeout)
}

// TimedOut returns true if the watchdog fired.
func (w *CommandOutputWatchdog) TimedOut() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timedOut
}

// Stop disables the watchdog.
func (w *CommandOutputWatchdog) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
}
