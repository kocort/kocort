// Package acp implements the Agent Communication Protocol (ACP) layer
// for inter-agent communication including ACP runtime management,
// session coordination, and protocol adapters.
//
// Migrated:
//   - AcpSessionManager and session lifecycle (manager.go)
//
// Future home of:
//   - ACP runtime adapter (acp_runtime_adapter.go) - blocked on AgentRunContext
//   - ACP backend (acp_backend.go) - stays in runtime
//
// Design: ACP enables agents to communicate and delegate tasks
// through a standardized protocol with session lifecycle management.
package acp
