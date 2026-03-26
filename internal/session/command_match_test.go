package session

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestParseSessionCommandForChatTypePrefersReset(t *testing.T) {
	match, ok := ParseSessionCommandForChatType([]string{"/new", "/reset"}, "/reset", core.ChatTypeDirect)
	if !ok {
		t.Fatal("expected command match")
	}
	if match.Kind != SessionCommandReset || match.Reset == nil || match.Reset.Trigger != "/reset" {
		t.Fatalf("unexpected match: %+v", match)
	}
}

func TestParseSessionCommandForChatTypeMatchesCompact(t *testing.T) {
	match, ok := ParseSessionCommandForChatType([]string{"/new", "/reset"}, "@bot /compact shrink hard", core.ChatTypeGroup)
	if !ok {
		t.Fatal("expected command match")
	}
	if match.Kind != SessionCommandCompact || match.Compact == nil || match.Compact.Instructions != "shrink hard" {
		t.Fatalf("unexpected match: %+v", match)
	}
}
