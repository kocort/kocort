package session

import "strings"

// VisibleSessionReferenceResolution represents the outcome of resolving a
// session reference and applying sandbox-spawn visibility constraints before
// generic session ACL checks run.
type VisibleSessionReferenceResolution struct {
	Key        string
	DisplayKey string
	Found      bool
	Status     string
	Error      string
}

// ResolveVisibleSessionReference resolves a session reference and, when the
// caller is sandbox-restricted to spawned sessions, rejects references outside
// the current spawned tree before broader ACL checks run.
//

// resolveSessionReference + resolveVisibleSessionReference flow, adapted to the
// current project's SessionStore-based implementation.
func ResolveVisibleSessionReference(store *SessionStore, opts ResolveVisibleSessionReferenceOptions) VisibleSessionReferenceResolution {
	resolved := ResolveSessionReferenceDetailed(store, opts.Resolve)
	if !resolved.Found {
		return VisibleSessionReferenceResolution{
			DisplayKey: nonEmptyDisplayKey(resolved.DisplayKey, opts.Resolve.Reference),
			Status:     "error",
			Error:      "session not found",
		}
	}
	result := VisibleSessionReferenceResolution{
		Key:        resolved.Key,
		DisplayKey: nonEmptyDisplayKey(resolved.DisplayKey, resolved.Key),
		Found:      true,
	}
	if !ShouldRestrictToSpawnedVisibility(opts) {
		return result
	}
	requesterKey := strings.TrimSpace(opts.RequesterSessionKey)
	if requesterKey == "" || requesterKey == resolved.Key || resolved.ResolvedViaID {
		return result
	}
	if store != nil && store.IsSpawnedSessionVisible(requesterKey, resolved.Key) {
		return result
	}
	return VisibleSessionReferenceResolution{
		Key:        resolved.Key,
		DisplayKey: result.DisplayKey,
		Found:      true,
		Status:     "forbidden",
		Error:      "Session not visible from this sandboxed agent session: " + strings.TrimSpace(opts.Resolve.Reference),
	}
}

// ResolveVisibleSessionReferenceOptions describes the pre-ACL visibility
// constraints used during session reference resolution.
type ResolveVisibleSessionReferenceOptions struct {
	Resolve                  ResolveReferenceOptions
	RequesterSessionKey      string
	SandboxEnabled           bool
	SandboxSessionVisibility string
}

// ShouldRestrictToSpawnedVisibility reports whether the caller should be
// constrained to the requester's spawned session tree for reference
// resolution/visibility purposes.
func ShouldRestrictToSpawnedVisibility(opts ResolveVisibleSessionReferenceOptions) bool {
	if !opts.SandboxEnabled {
		return false
	}
	visibility := strings.TrimSpace(strings.ToLower(opts.SandboxSessionVisibility))
	if visibility == "all" {
		return false
	}
	requesterKey := strings.TrimSpace(opts.RequesterSessionKey)
	if requesterKey == "" {
		return false
	}
	return !IsSubagentSessionKey(requesterKey)
}

func nonEmptyDisplayKey(value string, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(fallback)
}
