package task

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

func SyncACPChildRuntimeMetadata(record *SubagentRunRecord, entry *core.SessionEntry) {
	if record == nil || entry == nil || entry.ACP == nil {
		return
	}
	record.RuntimeBackend = strings.TrimSpace(entry.ACP.Backend)
	record.RuntimeState = strings.TrimSpace(entry.ACP.State)
	record.RuntimeMode = strings.TrimSpace(string(entry.ACP.Mode))
	record.RuntimeSessionName = strings.TrimSpace(entry.ACP.RuntimeSessionName)
	record.RuntimeBackendSessionID = strings.TrimSpace(entry.ACP.BackendSessionID)
	record.RuntimeAgentSessionID = strings.TrimSpace(entry.ACP.AgentSessionID)
	if entry.ACP.RuntimeStatus != nil {
		record.RuntimeStatusSummary = strings.TrimSpace(entry.ACP.RuntimeStatus.Summary)
	}
}
