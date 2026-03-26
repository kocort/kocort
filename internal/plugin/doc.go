// Package plugin defines the runtime plugin system that allows
// extending the runtime with custom tools, backends, and channels
// via Go interfaces.
//
// Migrated:
//   - Plugin config/policy helpers (config.go)
//
// Stays in runtime (coupled to *Runtime):
//   - RuntimePlugin interface
//   - RuntimePluginToolContext (has *Runtime field)
//   - RuntimePluginRegistry with ResolveTools
//
// Design: Plugins implement a standard interface and are registered
// during runtime initialization, providing hooks into the tool,
// backend, and channel systems.
package plugin
