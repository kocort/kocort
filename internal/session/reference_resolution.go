package session

// SessionReferenceKind describes which kind of user-facing reference matched a
// session lookup.
type SessionReferenceKind string

const (
	SessionReferenceUnknown   SessionReferenceKind = ""
	SessionReferenceKey       SessionReferenceKind = "key"
	SessionReferenceSessionID SessionReferenceKind = "session_id"
	SessionReferenceLabel     SessionReferenceKind = "label"
)

// SessionReferenceResolution captures the outcome of resolving a session
// reference into canonical/internal form. This mirrors the shape needed for

// runtime.
type SessionReferenceResolution struct {
	Key               string
	DisplayKey        string
	ResolvedVia       SessionReferenceKind
	ResolvedViaID     bool
	ResolvedReference string
	Found             bool
}

// ResolveSessionReferenceDetailed resolves a user-facing session reference into
// canonical/internal form and preserves how it was resolved.
func ResolveSessionReferenceDetailed(store *SessionStore, opts ResolveReferenceOptions) SessionReferenceResolution {
	if store == nil {
		return SessionReferenceResolution{}
	}
	mainAlias := ResolveMainSessionAlias(opts.MainKey)
	if alias := normalizeReferenceAlias(opts.Alias); alias != "" {
		mainAlias.Alias = alias
	}
	reference := ResolveInternalSessionKey(opts.Reference, mainAlias)
	if reference == "" {
		return SessionReferenceResolution{}
	}
	if reference == mainAlias.Alias {
		key := BuildMainSessionKeyWithMain(opts.RequesterAgentID, mainAlias.MainKey)
		return SessionReferenceResolution{
			Key:               key,
			DisplayKey:        ResolveDisplaySessionKey(mainAlias.MainKey, mainAlias),
			ResolvedVia:       SessionReferenceKey,
			ResolvedViaID:     false,
			ResolvedReference: reference,
			Found:             true,
		}
	}
	if key, kind, ok := store.ResolveSessionKeyReferenceDetailed(reference); ok {
		return SessionReferenceResolution{
			Key:               key,
			DisplayKey:        ResolveDisplaySessionKey(key, mainAlias),
			ResolvedVia:       kind,
			ResolvedViaID:     kind == SessionReferenceSessionID,
			ResolvedReference: reference,
			Found:             true,
		}
	}
	if key, ok := store.ResolveSessionLabel(opts.RequesterAgentID, reference, opts.SpawnedBy); ok {
		return SessionReferenceResolution{
			Key:               key,
			DisplayKey:        ResolveDisplaySessionKey(key, mainAlias),
			ResolvedVia:       SessionReferenceLabel,
			ResolvedViaID:     false,
			ResolvedReference: reference,
			Found:             true,
		}
	}
	return SessionReferenceResolution{
		DisplayKey:        ResolveDisplaySessionKey(reference, mainAlias),
		ResolvedReference: reference,
	}
}

func normalizeReferenceAlias(value string) string {
	return ResolveInternalSessionKey(value, ResolveMainSessionAlias(""))
}
