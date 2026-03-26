// Package gateway provides the HTTP gateway server that exposes
// the runtime API including webchat, channel webhooks, dashboard,
// and chat endpoints.
//
// Migrated:
//   - Chat abort trigger detection (chat_abort.go)
//   - EventHub SSE event hub (event_hub.go)
//   - HTTP helpers: writeJSON, ParseChatHistoryLimit, ParseChatHistoryBefore, boolPtr (helpers.go)
//
// Future home of:
//   - Gateway HTTP server (gateway.go)
//   - Chat API handlers (chat_api.go)
//   - Dashboard endpoints (dashboard.go)
//
// Design: The gateway wraps the runtime and exposes REST endpoints
// for external interaction with support for SSE streaming.
package gateway
