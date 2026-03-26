package runtime

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
)

func TestSlogAuditLoggerWritesAuditEvents(t *testing.T) {
	// Capture slog output into a buffer for verification.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))
	defer func() {
		// Restore default slog handler after test.
		infra.ApplySlogLevel(config.LoggingConfig{Level: "info"})
	}()

	logger, err := infra.NewSlogAuditLogger(config.LoggingConfig{
		Level: "info",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("NewSlogAuditLogger: %v", err)
	}
	runtime := &Runtime{Logger: logger}
	event.RecordAudit(context.Background(), nil, runtime.Logger, core.AuditEvent{
		Category: core.AuditCategoryTool,
		Type:     "tool_execute_completed",
		Level:    "info",
		ToolName: "exec",
		Message:  "tool execution completed",
		Data:     map[string]any{"text": "ok"},
	})

	text := buf.String()
	if !strings.Contains(text, "tool_execute_completed") {
		t.Fatalf("expected audit event type in slog output, got %q", text)
	}
	if !strings.Contains(text, "exec") {
		t.Fatalf("expected tool name in slog output, got %q", text)
	}
	if !strings.Contains(text, "tool execution completed") {
		t.Fatalf("expected message in slog output, got %q", text)
	}
}
