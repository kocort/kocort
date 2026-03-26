// Package infra provides cross-cutting infrastructure concerns
// including structured logging, audit trail, and environment
// variable management.
//
// Future home of:
//   - Structured logger (logger.go)
//   - Audit system (audit.go)
//   - Environment variable resolver (environment.go)
//   - Context source management (context_sources.go)
//   - Memory providers (memory.go)
//   - Prompt builder (prompt_builder.go)
//
// Design: Infrastructure modules provide shared capabilities used
// across all domain packages without introducing domain-specific
// dependencies.
package infra
