// Package sandbox resolves agent sandbox contexts for tool invocations.
//
// A sandbox context describes whether a particular tool execution is
// restricted to a sandboxed workspace, and if so what the scope and
// access mode are.  The canonical entry point is ResolveSandboxContext.
package sandbox
