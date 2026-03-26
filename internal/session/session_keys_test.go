package session

import (
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// NormalizeAgentID
// ---------------------------------------------------------------------------

func TestNormalizeAgentID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", DefaultAgentID},
		{"  ", DefaultAgentID},
		{"main", "main"},
		{"MAIN", "main"},
		{"  Main  ", "main"},
		{"my-agent", "my-agent"},
		{"my_agent", "my_agent"},
		{"My Agent", "my-agent"},
		{"agent123", "agent123"},
		{"hello@world", "hello-world"},
		{"---agent---", "agent"},
		{"  !!!  ", DefaultAgentID},
		{strings.Repeat("a", 100), strings.Repeat("a", 64)},
		{"CamelCase_Agent-123", "camelcase_agent-123"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeAgentID(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeAgentID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildMainSessionKey
// ---------------------------------------------------------------------------

func TestBuildMainSessionKey(t *testing.T) {
	tests := []struct {
		agentID string
		want    string
	}{
		{"main", "agent:main:main"},
		{"worker", "agent:worker:main"},
		{"", "agent:main:main"},
		{"  MyAgent  ", "agent:myagent:main"},
	}
	for _, tt := range tests {
		t.Run(tt.agentID, func(t *testing.T) {
			got := BuildMainSessionKey(tt.agentID)
			if got != tt.want {
				t.Errorf("BuildMainSessionKey(%q) = %q, want %q", tt.agentID, got, tt.want)
			}
		})
	}
}

func TestBuildMainSessionKeyWithMain(t *testing.T) {
	tests := []struct {
		agentID, mainKey, want string
	}{
		{"main", "main", "agent:main:main"},
		{"main", "custom", "agent:main:custom"},
		{"main", "", "agent:main:main"},
		{"worker", "lobby", "agent:worker:lobby"},
	}
	for _, tt := range tests {
		t.Run(tt.agentID+"_"+tt.mainKey, func(t *testing.T) {
			got := BuildMainSessionKeyWithMain(tt.agentID, tt.mainKey)
			if got != tt.want {
				t.Errorf("BuildMainSessionKeyWithMain(%q, %q) = %q, want %q", tt.agentID, tt.mainKey, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildDirectSessionKey
// ---------------------------------------------------------------------------

func TestBuildDirectSessionKey(t *testing.T) {
	tests := []struct {
		agentID, channel, peerID, want string
	}{
		{"main", "slack", "U12345", "agent:main:slack:direct:u12345"},
		{"main", "", "U12345", "agent:main:main"},
		{"main", "slack", "", "agent:main:main"},
		{"worker", "telegram", "user1", "agent:worker:telegram:direct:user1"},
	}
	for _, tt := range tests {
		t.Run(tt.agentID+"_"+tt.channel+"_"+tt.peerID, func(t *testing.T) {
			got := BuildDirectSessionKey(tt.agentID, tt.channel, tt.peerID)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildGroupSessionKey
// ---------------------------------------------------------------------------

func TestBuildGroupSessionKey(t *testing.T) {
	tests := []struct {
		agentID, channel string
		chatType         core.ChatType
		peerID, want     string
	}{
		{"main", "slack", core.ChatTypeGroup, "C123", "agent:main:slack:group:c123"},
		{"main", "slack", core.ChatTypeTopic, "T456", "agent:main:slack:topic:t456"},
		{"main", "", core.ChatTypeGroup, "C123", "agent:main:main"},
		{"main", "slack", core.ChatTypeGroup, "", "agent:main:main"},
	}
	for _, tt := range tests {
		t.Run(tt.peerID, func(t *testing.T) {
			got := BuildGroupSessionKey(tt.agentID, tt.channel, tt.chatType, tt.peerID)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildThreadSessionKey
// ---------------------------------------------------------------------------

func TestBuildThreadSessionKey(t *testing.T) {
	tests := []struct {
		baseKey, threadID, want string
	}{
		{"agent:main:slack:direct:u1", "t123", "agent:main:slack:direct:u1:thread:t123"},
		{"agent:main:main", "", "agent:main:main"},
		{"  base  ", "  tid  ", "base:thread:tid"},
	}
	for _, tt := range tests {
		t.Run(tt.threadID, func(t *testing.T) {
			got := BuildThreadSessionKey(tt.baseKey, tt.threadID)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildAcpSessionKey
// ---------------------------------------------------------------------------

func TestBuildAcpSessionKey(t *testing.T) {
	tests := []struct {
		agentID, backend, sessionRef, want string
	}{
		{"main", "openai", "ref1", "agent:main:acp:openai:ref1"},
		{"main", "", "ref1", "agent:main:acp:acp:ref1"},
		{"main", "openai", "", "agent:main:acp:openai"},
	}
	for _, tt := range tests {
		t.Run(tt.backend+"_"+tt.sessionRef, func(t *testing.T) {
			got := BuildAcpSessionKey(tt.agentID, tt.backend, tt.sessionRef)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildSubagentSessionKey
// ---------------------------------------------------------------------------

func TestBuildSubagentSessionKey(t *testing.T) {
	key, err := BuildSubagentSessionKey("main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(key, "agent:main:subagent:") {
		t.Errorf("key %q does not have expected prefix", key)
	}
	// Two calls should produce different keys.
	key2, err := BuildSubagentSessionKey("main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key == key2 {
		t.Errorf("expected unique keys, got same: %q", key)
	}
}

// ---------------------------------------------------------------------------
// ResolveAgentIDFromSessionKey
// ---------------------------------------------------------------------------

func TestResolveAgentIDFromSessionKey(t *testing.T) {
	tests := []struct {
		sessionKey string
		want       string
	}{
		{"agent:main:main", "main"},
		{"agent:worker:slack:direct:u1", "worker"},
		{"invalid", DefaultAgentID},
		{"agent:MyAgent:main", "myagent"},
		{"", DefaultAgentID},
	}
	for _, tt := range tests {
		t.Run(tt.sessionKey, func(t *testing.T) {
			got := ResolveAgentIDFromSessionKey(tt.sessionKey)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsSubagentSessionKey / IsAcpSessionKey
// ---------------------------------------------------------------------------

func TestIsSubagentSessionKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"agent:main:subagent:abc123", true},
		{"agent:main:main", false},
		{"agent:main:acp:openai", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := IsSubagentSessionKey(tt.key); got != tt.want {
				t.Errorf("IsSubagentSessionKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestIsAcpSessionKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"agent:main:acp:openai:ref1", true},
		{"agent:main:acp:openai", true},
		{"agent:main:main", false},
		{"agent:main:subagent:abc", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := IsAcpSessionKey(tt.key); got != tt.want {
				t.Errorf("IsAcpSessionKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RandomToken
// ---------------------------------------------------------------------------

func TestRandomToken(t *testing.T) {
	tok, err := RandomToken(16)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tok) != 32 { // hex encoding doubles the byte count
		t.Errorf("expected 32 hex chars, got %d", len(tok))
	}
	tok2, _ := RandomToken(16)
	if tok == tok2 {
		t.Errorf("two tokens should not be equal")
	}
}

// ---------------------------------------------------------------------------
// ResolveSessionKeyFromOptions
// ---------------------------------------------------------------------------

func TestResolveSessionKeyFromOptions(t *testing.T) {
	t.Run("explicit_session_key", func(t *testing.T) {
		got := ResolveSessionKeyFromOptions(SessionResolveOptions{
			SessionKey: "my:custom:key",
			AgentID:    "main",
		})
		if got != "my:custom:key" {
			t.Errorf("got %q, want %q", got, "my:custom:key")
		}
	})

	t.Run("direct_with_peer", func(t *testing.T) {
		got := ResolveSessionKeyFromOptions(SessionResolveOptions{
			AgentID:  "main",
			Channel:  "slack",
			ChatType: core.ChatTypeDirect,
			To:       "user1",
		})
		if got != "agent:main:slack:direct:user1" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("direct_dm_scope_main", func(t *testing.T) {
		got := ResolveSessionKeyFromOptions(SessionResolveOptions{
			AgentID:  "main",
			Channel:  "slack",
			ChatType: core.ChatTypeDirect,
			To:       "user1",
			DMScope:  "main",
		})
		if got != "agent:main:main" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("group_with_thread", func(t *testing.T) {
		got := ResolveSessionKeyFromOptions(SessionResolveOptions{
			AgentID:  "main",
			Channel:  "slack",
			ChatType: core.ChatTypeGroup,
			To:       "C123",
			ThreadID: "t456",
		})
		if got != "agent:main:slack:group:c123:thread:t456" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("defaults_to_webchat_channel", func(t *testing.T) {
		got := ResolveSessionKeyFromOptions(SessionResolveOptions{
			AgentID: "main",
		})
		if got != "agent:main:main" {
			t.Errorf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ParseSessionMaintenanceDuration
// ---------------------------------------------------------------------------

func TestParseSessionMaintenanceDuration(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		want    string
	}{
		{"", false, "0s"},
		{"30d", false, "720h0m0s"},
		{"2h", false, "2h0m0s"},
		{"500ms", false, "500ms"},
		{"bad", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSessionMaintenanceDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ParseSessionMaintenanceBytes
// ---------------------------------------------------------------------------

func TestParseSessionMaintenanceBytes(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"", 0, false},
		{"1024", 1024, false},
		{"10kb", 10240, false},
		{"5mb", 5 * 1024 * 1024, false},
		{"1gb", 1024 * 1024 * 1024, false},
		{"100b", 100, false},
		{"bad", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSessionMaintenanceBytes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
