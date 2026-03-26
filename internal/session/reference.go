package session

// ResolveReferenceOptions describes how to resolve a user-facing session
// reference into a canonical session key.
type ResolveReferenceOptions struct {
	Reference        string
	RequesterAgentID string
	SpawnedBy        string
	MainKey          string
	Alias            string
}

// ResolveSessionReference resolves a user-facing reference into a canonical
// session key. It supports:
// - "main" alias for the requester's main session
// - direct key / session ID / globally unique label references already known to the store
// - scoped label lookup by agent ID and optional spawnedBy chain
func ResolveSessionReference(store *SessionStore, opts ResolveReferenceOptions) (string, bool) {
	resolution := ResolveSessionReferenceDetailed(store, opts)
	return resolution.Key, resolution.Found
}
