package task

import "testing"

func TestResolveSubagentRoleForDepth(t *testing.T) {
	tests := []struct {
		name          string
		depth         int
		maxSpawnDepth int
		want          SubagentSessionRole
	}{
		{"depth 0 is main", 0, 5, SubagentRoleMain},
		{"negative depth clamps to main", -3, 5, SubagentRoleMain},
		{"depth 1 of 5 is orchestrator", 1, 5, SubagentRoleOrchestrator},
		{"depth 4 of 5 is orchestrator", 4, 5, SubagentRoleOrchestrator},
		{"depth 5 of 5 is leaf", 5, 5, SubagentRoleLeaf},
		{"depth 6 of 5 is leaf", 6, 5, SubagentRoleLeaf},
		{"depth 1 of 1 is leaf", 1, 1, SubagentRoleLeaf},
		{"zero maxSpawnDepth defaults to 5", 3, 0, SubagentRoleOrchestrator},
		{"negative maxSpawnDepth defaults to 5", 1, -2, SubagentRoleOrchestrator},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveSubagentRoleForDepth(tt.depth, tt.maxSpawnDepth)
			if got != tt.want {
				t.Errorf("ResolveSubagentRoleForDepth(%d, %d) = %q, want %q",
					tt.depth, tt.maxSpawnDepth, got, tt.want)
			}
		})
	}
}

func TestResolveSubagentControlScopeForRole(t *testing.T) {
	tests := []struct {
		role SubagentSessionRole
		want SubagentControlScope
	}{
		{SubagentRoleMain, SubagentControlChildren},
		{SubagentRoleOrchestrator, SubagentControlChildren},
		{SubagentRoleLeaf, SubagentControlNone},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := ResolveSubagentControlScopeForRole(tt.role)
			if got != tt.want {
				t.Errorf("role %q: got %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestResolveSubagentCapabilities(t *testing.T) {
	caps := ResolveSubagentCapabilities(0, 5)
	if caps.Role != SubagentRoleMain {
		t.Errorf("depth 0: role = %q, want main", caps.Role)
	}
	if !caps.CanSpawn {
		t.Error("main should be able to spawn")
	}
	if !caps.CanControlChildren {
		t.Error("main should control children")
	}

	caps = ResolveSubagentCapabilities(3, 5)
	if caps.Role != SubagentRoleOrchestrator {
		t.Errorf("depth 3: role = %q, want orchestrator", caps.Role)
	}
	if !caps.CanSpawn {
		t.Error("orchestrator should spawn")
	}

	caps = ResolveSubagentCapabilities(5, 5)
	if caps.Role != SubagentRoleLeaf {
		t.Errorf("depth 5: role = %q, want leaf", caps.Role)
	}
	if caps.CanSpawn {
		t.Error("leaf should NOT spawn")
	}
	if caps.CanControlChildren {
		t.Error("leaf should NOT control children")
	}
}

func TestResolveStoredSubagentCapabilities(t *testing.T) {
	// Stored role overrides depth-derived role
	caps := ResolveStoredSubagentCapabilities(5, 5, "orchestrator", "children")
	if caps.Role != SubagentRoleOrchestrator {
		t.Errorf("stored role override: got %q, want orchestrator", caps.Role)
	}
	if !caps.CanSpawn {
		t.Error("stored orchestrator should spawn")
	}
	if !caps.CanControlChildren {
		t.Error("stored children scope should control children")
	}

	// Invalid stored values are ignored
	caps = ResolveStoredSubagentCapabilities(0, 5, "invalid", "invalid")
	if caps.Role != SubagentRoleMain {
		t.Errorf("invalid stored role: got %q, want main", caps.Role)
	}
	if caps.ControlScope != SubagentControlChildren {
		t.Errorf("invalid stored scope: got %q, want children", caps.ControlScope)
	}
}
