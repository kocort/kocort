package tool

import (
	"testing"
)

// ---------------------------------------------------------------------------
// NormalizeToolPolicyName
// ---------------------------------------------------------------------------

func TestNormalizeToolPolicyName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"exec", "exec"},
		{"EXEC", "exec"},
		{"  Exec  ", "exec"},
		{"bash", "exec"},
		{"Bash", "exec"},
		{"apply-patch", "apply_patch"},
		{"unknown_tool", "unknown_tool"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := NormalizeToolPolicyName(tt.input); got != tt.want {
				t.Errorf("NormalizeToolPolicyName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NormalizeToolList
// ---------------------------------------------------------------------------

func TestNormalizeToolList(t *testing.T) {
	t.Run("normalizes_and_aliases", func(t *testing.T) {
		got := NormalizeToolList([]string{"Bash", "exec", "  CRON  "})
		if len(got) != 3 || got[0] != "exec" || got[1] != "exec" || got[2] != "cron" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("nil_input", func(t *testing.T) {
		got := NormalizeToolList(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
	t.Run("empty_input", func(t *testing.T) {
		got := NormalizeToolList([]string{})
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ExpandToolGroups
// ---------------------------------------------------------------------------

func TestExpandToolGroups(t *testing.T) {
	t.Run("expands_group", func(t *testing.T) {
		got := ExpandToolGroups([]string{"group:agents"})
		if len(got) == 0 {
			t.Fatal("expected non-empty expansion")
		}
		found := map[string]bool{}
		for _, name := range got {
			found[name] = true
		}
		if !found["sessions_spawn"] || !found["subagents"] {
			t.Errorf("expected sessions_spawn and subagents in expansion, got %v", got)
		}
	})
	t.Run("deduplicates", func(t *testing.T) {
		got := ExpandToolGroups([]string{"exec", "exec", "bash"})
		if len(got) != 1 || got[0] != "exec" {
			t.Errorf("expected single exec, got %v", got)
		}
	})
	t.Run("nil_input", func(t *testing.T) {
		got := ExpandToolGroups(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
	t.Run("mixed_groups_and_tools", func(t *testing.T) {
		got := ExpandToolGroups([]string{"group:agents", "exec"})
		found := map[string]bool{}
		for _, name := range got {
			found[name] = true
		}
		if !found["exec"] || !found["sessions_spawn"] {
			t.Errorf("expected exec and sessions_spawn, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ResolveToolProfilePolicy
// ---------------------------------------------------------------------------

func TestResolveToolProfilePolicy(t *testing.T) {
	t.Run("minimal", func(t *testing.T) {
		allow, ok := ResolveToolProfilePolicy("minimal")
		if !ok {
			t.Fatal("expected ok for minimal")
		}
		if len(allow) != 1 || allow[0] != "session_status" {
			t.Errorf("got %v", allow)
		}
	})
	t.Run("full_returns_nil_list", func(t *testing.T) {
		allow, ok := ResolveToolProfilePolicy("full")
		if !ok {
			t.Fatal("expected ok for full")
		}
		if allow != nil {
			t.Errorf("expected nil for full profile, got %v", allow)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		_, ok := ResolveToolProfilePolicy("nonexistent")
		if ok {
			t.Error("expected not-ok for unknown profile")
		}
	})
	t.Run("case_insensitive", func(t *testing.T) {
		_, ok := ResolveToolProfilePolicy("CODING")
		if !ok {
			t.Error("expected ok for CODING (case-insensitive)")
		}
	})
}

// ---------------------------------------------------------------------------
// ResolveSubagentDenyList
// ---------------------------------------------------------------------------

func TestResolveSubagentDenyList(t *testing.T) {
	t.Run("within_depth", func(t *testing.T) {
		deny := ResolveSubagentDenyList(1, 5)
		if len(deny) != len(SubagentToolDenyAlways) {
			t.Errorf("expected %d deny entries, got %d", len(SubagentToolDenyAlways), len(deny))
		}
	})
	t.Run("at_max_depth", func(t *testing.T) {
		deny := ResolveSubagentDenyList(5, 5)
		expected := len(SubagentToolDenyAlways) + len(SubagentToolDenyLeaf)
		if len(deny) != expected {
			t.Errorf("expected %d deny entries at max depth, got %d", expected, len(deny))
		}
	})
	t.Run("zero_max_uses_default", func(t *testing.T) {
		deny := ResolveSubagentDenyList(0, 0)
		if len(deny) != len(SubagentToolDenyAlways) {
			t.Errorf("expected %d deny entries, got %d", len(SubagentToolDenyAlways), len(deny))
		}
	})
	t.Run("negative_max_uses_default", func(t *testing.T) {
		deny := ResolveSubagentDenyList(DefaultSubagentMaxSpawnDepth, -1)
		expected := len(SubagentToolDenyAlways) + len(SubagentToolDenyLeaf)
		if len(deny) != expected {
			t.Errorf("expected %d deny entries, got %d", expected, len(deny))
		}
	})
}

// ---------------------------------------------------------------------------
// MergeToolLists
// ---------------------------------------------------------------------------

func TestMergeToolLists(t *testing.T) {
	t.Run("both_non_empty", func(t *testing.T) {
		merged := MergeToolLists([]string{"a", "b"}, []string{"c"})
		if len(merged) != 3 || merged[0] != "a" || merged[2] != "c" {
			t.Errorf("got %v", merged)
		}
	})
	t.Run("both_nil", func(t *testing.T) {
		merged := MergeToolLists(nil, nil)
		if merged != nil {
			t.Errorf("expected nil, got %v", merged)
		}
	})
	t.Run("does_not_mutate_base", func(t *testing.T) {
		base := []string{"a", "b"}
		_ = MergeToolLists(base, []string{"c"})
		if len(base) != 2 {
			t.Error("base was mutated")
		}
	})
}

// ---------------------------------------------------------------------------
// IsSessionScopedToolName
// ---------------------------------------------------------------------------

func TestIsSessionScopedToolName(t *testing.T) {
	sessionScoped := []string{"session_status", "sessions_history", "sessions_list", "sessions_send", "sessions_spawn", "subagents"}
	for _, name := range sessionScoped {
		if !IsSessionScopedToolName(name) {
			t.Errorf("expected %q to be session-scoped", name)
		}
	}
	nonScoped := []string{"exec", "cron", "process", "memory_search"}
	for _, name := range nonScoped {
		if IsSessionScopedToolName(name) {
			t.Errorf("expected %q to not be session-scoped", name)
		}
	}
}
