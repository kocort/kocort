package session

import (
	"testing"
	"time"
)

func TestClassifySessionKind(t *testing.T) {
	tests := []struct {
		key     string
		mainKey string
		want    string
	}{
		{key: "agent:main:main", mainKey: "main", want: "main"},
		{key: "agent:main:assistant", mainKey: "assistant", want: "main"},
		{key: "agent:main:webchat:group:team", mainKey: "main", want: "group"},
		{key: "agent:main:main:thread:th-1", mainKey: "main", want: "thread"},
		{key: "cron:nightly", mainKey: "main", want: "cron"},
		{key: "hook:github", mainKey: "main", want: "hook"},
		{key: "node:planner", mainKey: "main", want: "node"},
		{key: "agent:main:subagent:abc", mainKey: "main", want: "subagent"},
		{key: "agent:main:acp:acp-live:abc", mainKey: "main", want: "acp"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := ClassifySessionKind(tt.key, tt.mainKey); got != tt.want {
				t.Fatalf("ClassifySessionKind(%q, %q) = %q, want %q", tt.key, tt.mainKey, got, tt.want)
			}
		})
	}
}

func TestDeriveSessionChannel(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		kind        string
		channel     string
		lastChannel string
		want        string
	}{
		{name: "internal kind", key: "cron:nightly", kind: "cron", want: "internal"},
		{name: "subagent internal", key: "agent:main:subagent:abc", kind: "subagent", want: "internal"},
		{name: "acp internal", key: "agent:main:acp:acp-live:abc", kind: "acp", want: "internal"},
		{name: "explicit channel", key: "agent:main:main", kind: "main", channel: "slack", want: "slack"},
		{name: "last channel fallback", key: "agent:main:main", kind: "main", lastChannel: "telegram", want: "telegram"},
		{name: "unknown fallback", key: "agent:main:main", kind: "main", want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveSessionChannel(tt.key, tt.kind, tt.channel, tt.lastChannel); got != tt.want {
				t.Fatalf("DeriveSessionChannel(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFilterSessionListItems(t *testing.T) {
	now := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	items := []SessionListItem{
		{Key: "agent:main:main", UpdatedAt: now.Add(-2 * time.Minute)},
		{Key: "agent:main:webchat:group:room", UpdatedAt: now.Add(-1 * time.Minute)},
		{Key: "cron:nightly", UpdatedAt: now.Add(-30 * time.Minute)},
	}
	got := FilterSessionListItems(items, SessionListFilterOptions{
		Now:           now,
		ActiveMinutes: 10,
		Limit:         1,
		AllowedKinds:  map[string]struct{}{"group": {}},
		MainKey:       "main",
		Allow: func(item SessionListItem) bool {
			return item.Key != "cron:nightly"
		},
	})
	if len(got) != 1 || got[0].Key != "agent:main:webchat:group:room" {
		t.Fatalf("unexpected filtered items: %+v", got)
	}
}

func TestDecorateSessionListItems(t *testing.T) {
	items := []SessionListItem{
		{Key: "agent:main:main", LastChannel: "slack"},
		{Key: "cron:nightly"},
		{Key: "agent:main:acp:acp-live:abc"},
	}
	got := DecorateSessionListItems(items, "main")
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d", len(got))
	}
	if got[0].DisplayKey != "main" || got[0].Kind != "main" || got[0].Channel != "slack" {
		t.Fatalf("unexpected decorated main item: %+v", got[0])
	}
	if got[1].Kind != "cron" || got[1].Channel != "internal" {
		t.Fatalf("unexpected decorated cron item: %+v", got[1])
	}
	if got[2].Kind != "acp" || got[2].Channel != "internal" {
		t.Fatalf("unexpected decorated acp item: %+v", got[2])
	}
}
