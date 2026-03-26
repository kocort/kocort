package infra

import "strings"

type SpawnWorkspaceOptions struct {
	ExplicitWorkspace  string
	RequesterWorkspace string
	TargetWorkspace    string
}

// ResolveSpawnWorkspaceDir keeps workspace inheritance policy out of runtime
// orchestration and out of individual spawn launch files.
func ResolveSpawnWorkspaceDir(opts SpawnWorkspaceOptions) string {
	if explicit := strings.TrimSpace(opts.ExplicitWorkspace); explicit != "" {
		return explicit
	}
	if target := strings.TrimSpace(opts.TargetWorkspace); target != "" {
		return target
	}
	return strings.TrimSpace(opts.RequesterWorkspace)
}
