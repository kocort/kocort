package spawnpolicy

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestNormalizeSpawnSandboxMode(t *testing.T) {
	if got := NormalizeSpawnSandboxMode("require"); got != "require" {
		t.Fatalf("expected require, got %q", got)
	}
	if got := NormalizeSpawnSandboxMode("  "); got != "inherit" {
		t.Fatalf("expected inherit, got %q", got)
	}
}

func TestValidateSpawnRuntimePolicy(t *testing.T) {
	requesterSandboxed := core.AgentIdentity{ID: "main", SandboxMode: "all"}
	targetSandboxed := core.AgentIdentity{ID: "worker", SandboxMode: "all"}
	targetUnsandboxed := core.AgentIdentity{ID: "worker"}

	if err := ValidateSpawnRuntimePolicy(requesterSandboxed, targetUnsandboxed, "inherit"); err == nil {
		t.Fatal("expected sandbox escape to be rejected")
	}
	if err := ValidateSpawnRuntimePolicy(core.AgentIdentity{ID: "main"}, targetUnsandboxed, "require"); err == nil {
		t.Fatal("expected require to reject unsandboxed target")
	}
	if err := ValidateSpawnRuntimePolicy(requesterSandboxed, targetSandboxed, "require"); err != nil {
		t.Fatalf("expected sandbox-compatible spawn to pass, got %v", err)
	}
}
