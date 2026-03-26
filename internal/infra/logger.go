package infra

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// Global slog level management
// ---------------------------------------------------------------------------

// globalSlogLevel is the dynamically adjustable log level for slog.
// It is set once during init and can be updated at runtime via SetSlogLevel.
var globalSlogLevel = new(slog.LevelVar)

func init() {
	// Install a text handler with the adjustable level as the slog default.
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: globalSlogLevel,
	})
	slog.SetDefault(slog.New(h))
}

// ParseSlogLevel converts a human-friendly level string ("debug", "info",
// "warn", "error") to the corresponding slog.Level. Defaults to slog.LevelInfo.
func ParseSlogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetSlogLevel updates the global slog log level at runtime.
// It is safe for concurrent use.
func SetSlogLevel(level string) {
	globalSlogLevel.Set(ParseSlogLevel(level))
}

// ApplySlogLevel is a convenience wrapper that reads the level from a
// LoggingConfig and applies it to the global slog handler.
func ApplySlogLevel(cfg config.LoggingConfig) {
	SetSlogLevel(cfg.Level)
}

// ---------------------------------------------------------------------------
// SlogAuditLogger — audit event logger backed by slog
// ---------------------------------------------------------------------------

// SlogAuditLogger logs audit events via slog. It satisfies the
// runtime.RuntimeLogReloader interface (LogAuditEvent + Reload).
type SlogAuditLogger struct{}

// NewSlogAuditLogger creates a SlogAuditLogger and applies the initial
// log level from the config. The stateDir parameter is accepted for
// backward compatibility but is unused (slog writes to stderr).
func NewSlogAuditLogger(cfg config.LoggingConfig, _ string) (*SlogAuditLogger, error) {
	ApplySlogLevel(cfg)
	return &SlogAuditLogger{}, nil
}

// Reload applies the updated log level from config. File-based parameters
// are ignored because slog writes to stderr.
func (l *SlogAuditLogger) Reload(cfg config.LoggingConfig, _ string) error {
	ApplySlogLevel(cfg)
	return nil
}

// auditSlogLevel maps an audit event level string to the corresponding slog.Level.
func auditSlogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogAuditEvent emits an audit event through slog with structured attributes.
func (l *SlogAuditLogger) LogAuditEvent(ev core.AuditEvent) {
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}

	attrs := []slog.Attr{
		slog.String("category", string(ev.Category)),
		slog.String("type", ev.Type),
	}
	if ev.AgentID != "" {
		attrs = append(attrs, slog.String("agent", ev.AgentID))
	}
	if ev.SessionKey != "" {
		attrs = append(attrs, slog.String("session", ev.SessionKey))
	}
	if ev.RunID != "" {
		attrs = append(attrs, slog.String("run", ev.RunID))
	}
	if ev.ToolName != "" {
		attrs = append(attrs, slog.String("tool", ev.ToolName))
	}
	if ev.TaskID != "" {
		attrs = append(attrs, slog.String("task", ev.TaskID))
	}
	if ev.Channel != "" {
		attrs = append(attrs, slog.String("channel", ev.Channel))
	}
	if len(ev.Data) > 0 {
		if raw, err := json.Marshal(ev.Data); err == nil {
			attrs = append(attrs, slog.String("data", string(raw)))
		}
	}

	anyAttrs := make([]any, len(attrs))
	for i, a := range attrs {
		anyAttrs[i] = a
	}

	slog.Log(nil, auditSlogLevel(ev.Level), "[audit] "+ev.Message, anyAttrs...)
}
