package infra

import "testing"

func TestResolveSpawnWorkspaceDir(t *testing.T) {
	if got := ResolveSpawnWorkspaceDir(SpawnWorkspaceOptions{
		ExplicitWorkspace:  "/tmp/explicit",
		RequesterWorkspace: "/tmp/requester",
		TargetWorkspace:    "/tmp/target",
	}); got != "/tmp/explicit" {
		t.Fatalf("expected explicit workspace, got %q", got)
	}
	if got := ResolveSpawnWorkspaceDir(SpawnWorkspaceOptions{
		RequesterWorkspace: "/tmp/requester",
		TargetWorkspace:    "/tmp/target",
	}); got != "/tmp/target" {
		t.Fatalf("expected target workspace fallback, got %q", got)
	}
	if got := ResolveSpawnWorkspaceDir(SpawnWorkspaceOptions{
		RequesterWorkspace: "/tmp/requester",
	}); got != "/tmp/requester" {
		t.Fatalf("expected requester workspace fallback, got %q", got)
	}
}
