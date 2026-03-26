package session

import (
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func TestResolveSessionResetType(t *testing.T) {
	tests := []struct {
		name       string
		sessionKey string
		chatType   core.ChatType
		threadID   string
		want       SessionResetType
	}{
		{name: "direct", sessionKey: "agent:main:main", chatType: core.ChatTypeDirect, want: SessionResetDirect},
		{name: "group by chat type", sessionKey: "agent:main:main", chatType: core.ChatTypeGroup, want: SessionResetGroup},
		{name: "group by key", sessionKey: "agent:main:webchat:group:room", chatType: core.ChatTypeDirect, want: SessionResetGroup},
		{name: "thread by id", sessionKey: "agent:main:main", chatType: core.ChatTypeDirect, threadID: "thread-1", want: SessionResetThread},
		{name: "thread by key", sessionKey: "agent:main:main:thread:thread-1", chatType: core.ChatTypeDirect, want: SessionResetThread},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveSessionResetType(tt.sessionKey, tt.chatType, tt.threadID); got != tt.want {
				t.Fatalf("ResolveSessionResetType(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveFreshnessPolicyForSessionUsesResolvedResetType(t *testing.T) {
	cfg := config.AppConfig{
		Session: config.SessionConfig{
			ResetByType: &config.SessionResetByTypeConfig{
				Direct: &config.SessionResetConfig{Mode: "idle", IdleMinutes: 10},
				Group:  &config.SessionResetConfig{Mode: "idle", IdleMinutes: 20},
				Thread: &config.SessionResetConfig{Mode: "idle", IdleMinutes: 30},
			},
		},
	}
	got := ResolveFreshnessPolicyForSession(cfg, "agent:main:webchat:group:room:thread:thread-1", core.ChatTypeDirect, "webchat", "")
	if got.IdleMinutes != 30 {
		t.Fatalf("expected thread policy, got %+v", got)
	}
}
