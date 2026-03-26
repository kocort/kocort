// Package tool defines the tool system: built-in tools, tool registration,
// tool execution pipeline, and sandbox security.
//
// Canonical implementations:
//   - Tool policy: name normalisation, tool groups, profiles, subagent deny lists
//   - Tool loop detection: ToolLoopRegistry, loop detectors, warning buckets
//   - Process registry: managed subprocess lifecycle (start/poll/kill)
//   - Tool approval types: ToolApprovalRequest, ToolApprovalDecision, ToolApprovalRunner
//   - Tool params: ReadStringParam, ReadBoolParam, ReadOptionalPositiveDurationParam, ReadOptionalStringMapParam, JSONResult, ToolInputError (params.go)
//
// Future home of:
//   - Tool interface and execution pipeline
//   - Built-in tool implementations (session tools, process tools, etc.)
//   - Tool registration and discovery
//   - Sandbox context and security enforcement
//   - Tool planner interface
//
// Design: Tools are registered with metadata and executed within a
// sandboxed context that enforces security policies per agent.
package tool
