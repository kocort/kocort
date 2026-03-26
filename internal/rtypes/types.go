// Package rtypes re-exports the shared runtime types now canonically
// defined in kocort/internal/tool.  The type aliases below keep every
// existing consumer compiling unchanged.
//
// Dependency graph (edges = "imports"):
//
//	core       → (nothing)
//	config     → core
//	session    → core, config
//	task       → core
//	tool       → core, session, task, infra, delivery
//	infra      → core, config
//	delivery   → core
//	rtypes     → tool                                     ← this package
//	runtime    → rtypes + everything else
//
// Because none of the leaf packages (session, task, infra, delivery) import
// tool, and tool does not import rtypes, no import cycle is possible.
package rtypes

import "github.com/kocort/kocort/internal/tool"

type RuntimeServices = tool.RuntimeServices
type AgentRunContext = tool.AgentRunContext
type ToolContext = tool.ToolContext
type SandboxContext = tool.SandboxContext
type Backend = tool.Backend
type Tool = tool.Tool
type ToolPlanner = tool.ToolPlanner
