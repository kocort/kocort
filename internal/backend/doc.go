// Package backend defines the LLM backend abstraction layer.
//
// Currently contains:
//   - Backend error types and failure classification (errors.go)
//   - Model selection, normalization, and thinking level logic (model_selection.go)
//   - Model fallback orchestration (model_fallback.go)
//   - OpenAI history sanitization and transcript conversion (openai_history.go)
//   - Anthropic history sanitization and context pruning (anthropic_history.go)
//   - Command helpers: ParseCommandJSONOutput, CloneAnyMap, AsString, AsBool, MustDecodeMap, CommandOutputWatchdog (command_helpers.go)
//
// Future home of:
//   - OpenAI-compatible backend (openai_compat_backend.go)
//   - Anthropic-compatible backend (anthropic_compat_backend.go)
//   - CLI backend (cli_backend.go)
//   - Command backend (command_backend.go)
//   - Embedded backend (embedded_backend.go)
//   - ACP backend (acp_backend.go)
//   - Backend registry and factory
//
// Design: Backends implement a common interface for requesting completions
// from LLM providers with streaming support, tool calling, and model
// fallback.
package backend
