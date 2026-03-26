// registry.go — ToolRegistry for storing and retrieving registered tools.
//
// Moved from runtime/runtime_tools.go so that the registry lives alongside
// the Tool interface and built-in tool implementations.  The runtime package
// keeps a type alias (type ToolRegistry = tool.ToolRegistry) so that all
// existing callers — including test files — continue to compile unchanged.
package tool

import (
	"sync"

	"github.com/kocort/kocort/internal/core"
)

// ToolRegistry holds all registered tools and their metadata.
// It is thread-safe for concurrent Register / Get / List calls.
type ToolRegistry struct {
	mu    sync.Mutex
	tools map[string]Tool
	meta  map[string]core.ToolRegistrationMeta
}

// NewToolRegistry creates a ToolRegistry pre-loaded with the given tools.
func NewToolRegistry(tools ...Tool) *ToolRegistry {
	r := &ToolRegistry{
		tools: map[string]Tool{},
		meta:  map[string]core.ToolRegistrationMeta{},
	}
	for _, t := range tools {
		r.Register(t)
	}
	return r
}

// Register adds a tool to the registry, extracting metadata if the tool
// implements core.ToolMetadataProvider.
func (r *ToolRegistry) Register(t Tool) {
	var meta core.ToolRegistrationMeta
	if provider, ok := t.(core.ToolMetadataProvider); ok {
		meta = provider.ToolRegistrationMeta()
	}
	r.RegisterWithMeta(t, meta)
}

// RegisterWithMeta adds a tool with explicit registration metadata.
func (r *ToolRegistry) RegisterWithMeta(t Tool, meta core.ToolRegistrationMeta) {
	if t == nil {
		return
	}
	name := NormalizeToolPolicyName(t.Name())
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = t
	r.meta[name] = meta
}

// Get returns the tool registered under the given name, or nil.
func (r *ToolRegistry) Get(name string) Tool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tools[NormalizeToolPolicyName(name)]
}

// List returns all registered tools in an unordered slice.
func (r *ToolRegistry) List() []Tool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// Meta returns the registration metadata for the named tool.
func (r *ToolRegistry) Meta(name string) core.ToolRegistrationMeta {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.meta[NormalizeToolPolicyName(name)]
}
