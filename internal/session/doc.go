// Package session manages agent session lifecycle including session
// resolution, transcript persistence, session compaction, and
// cross-agent session access control.
//
// Currently contains:
//   - Session key building and normalization (session_keys.go)
//   - Session store, resolution, and maintenance (session_store.go)
//   - Session access control types and pure policy functions (access.go)
//
// Future home of:
//   - Transcript management
//   - Session compaction (session_compaction.go)
//
// Design: Sessions are identified by composite keys
// (agent:channel:chatType:target) and persist transcript history
// to the file system.
package session
