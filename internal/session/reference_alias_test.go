package session

import "testing"

func TestResolveMainSessionAliasDefaultsToMain(t *testing.T) {
	alias := ResolveMainSessionAlias("")
	if alias.MainKey != "main" || alias.Alias != "main" {
		t.Fatalf("expected default main alias, got %#v", alias)
	}
}

func TestResolveDisplaySessionKeyMapsConfiguredMainToMain(t *testing.T) {
	alias := ResolveMainSessionAlias("assistant")
	got := ResolveDisplaySessionKey("assistant", alias)
	if got != "main" {
		t.Fatalf("expected main display key, got %q", got)
	}
}

func TestResolveDisplaySessionKeyMapsCanonicalMainSessionKeyToMain(t *testing.T) {
	alias := ResolveMainSessionAlias("assistant")
	got := ResolveDisplaySessionKey("agent:worker:assistant", alias)
	if got != "main" {
		t.Fatalf("expected main display key, got %q", got)
	}
}

func TestResolveInternalSessionKeyMapsMainToConfiguredAlias(t *testing.T) {
	alias := ResolveMainSessionAlias("assistant")
	got := ResolveInternalSessionKey("main", alias)
	if got != "assistant" {
		t.Fatalf("expected assistant alias, got %q", got)
	}
}
