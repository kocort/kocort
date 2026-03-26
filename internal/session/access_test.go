package session

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// NormalizeSessionVisibility
// ---------------------------------------------------------------------------

func TestNormalizeSessionVisibility(t *testing.T) {
	tests := []struct {
		input core.SessionToolsVisibility
		want  core.SessionToolsVisibility
	}{
		{core.SessionVisibilitySelf, core.SessionVisibilitySelf},
		{core.SessionVisibilityTree, core.SessionVisibilityTree},
		{core.SessionVisibilityAgent, core.SessionVisibilityAgent},
		{core.SessionVisibilityAll, core.SessionVisibilityAll},
		{"unknown", core.SessionVisibilityTree},
		{"", core.SessionVisibilityTree},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got := NormalizeSessionVisibility(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeSessionVisibility(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ActionPrefix
// ---------------------------------------------------------------------------

func TestActionPrefix(t *testing.T) {
	tests := []struct {
		action SessionAccessAction
		want   string
	}{
		{SessionAccessHistory, "Session history"},
		{SessionAccessSend, "Session send"},
		{SessionAccessList, "Session list"},
		{"unknown", "Session list"},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			got := ActionPrefix(tt.action)
			if got != tt.want {
				t.Errorf("ActionPrefix(%q) = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MakeSessionAccessError
// ---------------------------------------------------------------------------

func TestMakeSessionAccessError(t *testing.T) {
	result := MakeSessionAccessError(SessionAccessHistory, core.SessionVisibilitySelf)
	if result.Allowed {
		t.Error("expected forbidden result")
	}
	if result.Status != "forbidden" {
		t.Errorf("expected status=forbidden, got %q", result.Status)
	}
	if !result.IsForbidden() {
		t.Error("IsForbidden should return true")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message")
	}
}

// ---------------------------------------------------------------------------
// MatchAgentPattern
// ---------------------------------------------------------------------------

func TestMatchAgentPattern(t *testing.T) {
	tests := []struct {
		pattern string
		agentID string
		want    bool
	}{
		// Exact match
		{"main", "main", true},
		{"main", "worker", false},
		// Wildcard all
		{"*", "anything", true},
		// Prefix wildcard
		{"*agent", "myagent", true},
		{"*agent", "mything", false},
		// Suffix wildcard
		{"my*", "myagent", true},
		{"my*", "youragent", false},
		// Contains wildcard
		{"*gen*", "myagent", true},
		{"*gen*", "myclient", false},
		// Case insensitive
		{"Main", "MAIN", true},
		// Empty inputs
		{"", "main", false},
		{"main", "", false},
		{"", "", false},
		// Multi-part wildcard
		{"a*c", "abc", true},
		{"a*c", "adc", true},
		{"a*c", "abd", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.agentID, func(t *testing.T) {
			got := MatchAgentPattern(tt.pattern, tt.agentID)
			if got != tt.want {
				t.Errorf("MatchAgentPattern(%q, %q) = %v, want %v", tt.pattern, tt.agentID, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MatchesA2AAllow
// ---------------------------------------------------------------------------

func TestMatchesA2AAllow(t *testing.T) {
	t.Run("empty_allow_list_allows_all", func(t *testing.T) {
		policy := core.AgentToAgentPolicy{Enabled: true, Allow: nil}
		if !MatchesA2AAllow(policy, "main") {
			t.Error("empty allow list should match all")
		}
	})

	t.Run("explicit_allow_matches", func(t *testing.T) {
		policy := core.AgentToAgentPolicy{Enabled: true, Allow: []string{"main", "worker"}}
		if !MatchesA2AAllow(policy, "main") {
			t.Error("should match 'main'")
		}
		if MatchesA2AAllow(policy, "other") {
			t.Error("should not match 'other'")
		}
	})

	t.Run("pattern_allow_matches", func(t *testing.T) {
		policy := core.AgentToAgentPolicy{Enabled: true, Allow: []string{"agent-*"}}
		if !MatchesA2AAllow(policy, "agent-1") {
			t.Error("should match 'agent-1'")
		}
		if MatchesA2AAllow(policy, "other") {
			t.Error("should not match 'other'")
		}
	})
}

// ---------------------------------------------------------------------------
// IsA2AAllowed
// ---------------------------------------------------------------------------

func TestIsA2AAllowed(t *testing.T) {
	t.Run("same_agent_always_allowed", func(t *testing.T) {
		policy := core.AgentToAgentPolicy{Enabled: false}
		if !IsA2AAllowed(policy, "main", "main") {
			t.Error("same agent should always be allowed")
		}
	})

	t.Run("disabled_denies_cross_agent", func(t *testing.T) {
		policy := core.AgentToAgentPolicy{Enabled: false}
		if IsA2AAllowed(policy, "main", "worker") {
			t.Error("disabled policy should deny cross-agent access")
		}
	})

	t.Run("enabled_allows_cross_agent", func(t *testing.T) {
		policy := core.AgentToAgentPolicy{Enabled: true}
		if !IsA2AAllowed(policy, "main", "worker") {
			t.Error("enabled policy with no allow list should permit cross-agent")
		}
	})

	t.Run("enabled_with_allow_list_filters", func(t *testing.T) {
		policy := core.AgentToAgentPolicy{Enabled: true, Allow: []string{"main"}}
		if IsA2AAllowed(policy, "main", "worker") {
			t.Error("worker not in allow list, should be denied")
		}
		if !IsA2AAllowed(policy, "main", "main") {
			t.Error("same agent should still be allowed")
		}
	})
}

// ---------------------------------------------------------------------------
// A2ADisabledMessage / A2ADeniedMessage
// ---------------------------------------------------------------------------

func TestA2AMessages(t *testing.T) {
	for _, action := range []SessionAccessAction{SessionAccessHistory, SessionAccessSend, SessionAccessList} {
		disabled := A2ADisabledMessage(action)
		if disabled == "" {
			t.Errorf("A2ADisabledMessage(%q) returned empty string", action)
		}
		denied := A2ADeniedMessage(action)
		if denied == "" {
			t.Errorf("A2ADeniedMessage(%q) returned empty string", action)
		}
	}
}
