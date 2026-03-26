package event

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/utils"
)

// RecordAudit records a generic audit event.
// Both rec and logger are optional; failure is best-effort and non-critical.
func RecordAudit(ctx context.Context, rec AuditRecorder, logger AuditLogger, event core.AuditEvent) {
	if rec != nil {
		_ = rec.Record(ctx, event) // best-effort; failure is non-critical
	}
	if logger != nil {
		logger.LogAuditEvent(event)
	}
}

// RecordModelEvent records an audit event in the "model" category.
func RecordModelEvent(ctx context.Context, rec AuditRecorder, logger AuditLogger, agentID, sessionKey, runID, typ, level, message string, data map[string]any) {
	RecordAudit(ctx, rec, logger, core.AuditEvent{
		Category:   core.AuditCategoryModel,
		Type:       strings.TrimSpace(typ),
		Level:      utils.NonEmpty(strings.TrimSpace(level), "info"),
		AgentID:    strings.TrimSpace(agentID),
		SessionKey: strings.TrimSpace(sessionKey),
		RunID:      strings.TrimSpace(runID),
		Message:    strings.TrimSpace(message),
		Data:       utils.CloneAnyMap(data),
	})
}

// RecordRuntimeEvent records an audit event in the "runtime" category.
func RecordRuntimeEvent(ctx context.Context, rec AuditRecorder, logger AuditLogger, agentID, sessionKey, runID, typ, level, message string, data map[string]any) {
	RecordAudit(ctx, rec, logger, core.AuditEvent{
		Category:   core.AuditCategoryRuntime,
		Type:       strings.TrimSpace(typ),
		Level:      utils.NonEmpty(strings.TrimSpace(level), "info"),
		AgentID:    strings.TrimSpace(agentID),
		SessionKey: strings.TrimSpace(sessionKey),
		RunID:      strings.TrimSpace(runID),
		Message:    strings.TrimSpace(message),
		Data:       utils.CloneAnyMap(data),
	})
}

// RecordChannelEvent records an audit event in the "channel" category.
func RecordChannelEvent(ctx context.Context, rec AuditRecorder, logger AuditLogger, msg core.ChannelInboundMessage, typ, message string, data map[string]any) {
	sessionKey := ""
	if v, ok := data["sessionKey"].(string); ok {
		sessionKey = v
	}
	RecordAudit(ctx, rec, logger, core.AuditEvent{
		Category:   core.AuditCategoryChannel,
		Type:       strings.TrimSpace(typ),
		Level:      "info",
		AgentID:    strings.TrimSpace(msg.AgentID),
		SessionKey: strings.TrimSpace(sessionKey),
		Channel:    strings.TrimSpace(msg.Channel),
		Message:    strings.TrimSpace(message),
		Data:       utils.CloneAnyMap(data),
	})
}

// RecordCerebellumEvent records an audit event in the "cerebellum" category.
func RecordCerebellumEvent(ctx context.Context, rec AuditRecorder, logger AuditLogger, typ, level, message string, data map[string]any) {
	RecordAudit(ctx, rec, logger, core.AuditEvent{
		Category: core.AuditCategoryCerebellum,
		Type:     strings.TrimSpace(typ),
		Level:    utils.NonEmpty(strings.TrimSpace(level), "info"),
		Message:  strings.TrimSpace(message),
		Data:     utils.CloneAnyMap(data),
	})
}
