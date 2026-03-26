package task

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestValidateSpawnTargetAgentAllowsWildcard(t *testing.T) {
	err := ValidateSpawnTargetAgent(core.AgentIdentity{
		SubagentAllowAgents: []string{"*"},
	}, "main", "worker")
	if err != nil {
		t.Fatalf("expected wildcard allow, got %v", err)
	}
}

func TestValidateSpawnTargetAgentRejectsDisallowedTarget(t *testing.T) {
	err := ValidateSpawnTargetAgent(core.AgentIdentity{
		SubagentAllowAgents: []string{"worker"},
	}, "main", "reviewer")
	if err == nil {
		t.Fatal("expected allowlist rejection")
	}
}
