package task

import (
	"fmt"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

// ValidateSpawnTargetAgent enforces the requester identity's allowlist for
// child session spawns. The rule is shared by subagent and ACP spawn paths so
// it lives in internal/task instead of runtime.
func ValidateSpawnTargetAgent(requesterIdentity core.AgentIdentity, requesterAgentID string, targetAgentID string) error {
	if len(requesterIdentity.SubagentAllowAgents) == 0 {
		return nil
	}
	resolvedTarget := session.NormalizeAgentID(targetAgentID)
	if resolvedTarget == "" {
		resolvedTarget = session.NormalizeAgentID(requesterAgentID)
	}
	for _, allowedAgent := range requesterIdentity.SubagentAllowAgents {
		trimmed := session.NormalizeAgentID(allowedAgent)
		if allowedAgent == "*" {
			trimmed = "*"
		}
		normalized := trimmed
		if normalized == "*" || normalized == resolvedTarget {
			return nil
		}
	}
	return fmt.Errorf("sessions_spawn target agent %q is not allowed", resolvedTarget)
}
