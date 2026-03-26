package session

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestNormalizeSessionCommandMessageStripsTimestampPrefix(t *testing.T) {
	got := NormalizeSessionCommandMessage("[Dec 4 17:35] /reset", core.ChatTypeDirect)
	if got != "/reset" {
		t.Fatalf("expected /reset, got %q", got)
	}
}

func TestNormalizeSessionCommandMessageStripsLeadingMentionInGroup(t *testing.T) {
	got := NormalizeSessionCommandMessage("@bot /new continue", core.ChatTypeGroup)
	if got != "/new continue" {
		t.Fatalf("expected /new continue, got %q", got)
	}
}

func TestMatchSessionResetTriggerForChatTypeMatchesNormalizedGroupCommand(t *testing.T) {
	trigger, remainder := MatchSessionResetTriggerForChatType([]string{"/new", "/reset"}, "[Dec 4 17:35] @bot /reset keep going", core.ChatTypeGroup)
	if trigger != "/reset" || remainder != "keep going" {
		t.Fatalf("expected normalized group reset match, got trigger=%q remainder=%q", trigger, remainder)
	}
}

func TestParseSessionResetCommandForChatTypeBuildsReasonAndReply(t *testing.T) {
	match, ok := ParseSessionResetCommandForChatType([]string{"/new", "/reset"}, "/new", core.ChatTypeDirect)
	if !ok {
		t.Fatal("expected reset command match")
	}
	if match.Trigger != "/new" || match.Reason != "new" || match.Remainder != "" {
		t.Fatalf("unexpected match: %+v", match)
	}
	if got := match.ReplyText(); got != "Started a new session." {
		t.Fatalf("unexpected reply text: %q", got)
	}
}

func TestParseSessionCompactCommandForChatTypeMatchesNormalizedGroupCommand(t *testing.T) {
	match, ok := ParseSessionCompactCommandForChatType("[Dec 4 17:35] @bot /compact keep decisions only", core.ChatTypeGroup)
	if !ok {
		t.Fatal("expected compact command match")
	}
	if match.Trigger != "/compact" || match.Instructions != "keep decisions only" {
		t.Fatalf("unexpected compact match: %+v", match)
	}
}
