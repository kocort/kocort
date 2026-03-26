// Forwarding layer - plugin config helpers live in kocort/internal/plugin.
// The RuntimePlugin interface, RuntimePluginToolContext, and
// RuntimePluginRegistry remain here because they depend on runtime-only types
// (*Runtime, rtypes.Tool, rtypes.AgentRunContext).
package runtime

import (
	"context"
	"fmt"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/rtypes"
	toolfn "github.com/kocort/kocort/internal/tool"

	pluginpkg "github.com/kocort/kocort/internal/plugin"
	"github.com/kocort/kocort/internal/tool"
)

type RuntimePluginToolContext struct {
	Runtime           *Runtime
	WorkspaceDir      string
	Identity          core.AgentIdentity
	Run               rtypes.AgentRunContext
	ExistingToolNames map[string]struct{}
	ToolAllowlist     []string
}

type RuntimePlugin interface {
	ID() string
	Tools(ctx context.Context, pluginCtx RuntimePluginToolContext) ([]rtypes.Tool, error)
}

type RegisteredRuntimeTool struct {
	Tool rtypes.Tool
	Meta core.ToolRegistrationMeta
}

type RuntimePluginRegistry struct {
	config  config.PluginsConfig
	plugins []RuntimePlugin
}

func NewRuntimePluginRegistry(cfg config.PluginsConfig, plugins ...RuntimePlugin) *RuntimePluginRegistry {
	return &RuntimePluginRegistry{
		config:  cfg,
		plugins: append([]RuntimePlugin{}, plugins...),
	}
}

func (r *RuntimePluginRegistry) Register(plugin RuntimePlugin) {
	if r == nil || plugin == nil {
		return
	}
	r.plugins = append(r.plugins, plugin)
}

func (r *RuntimePluginRegistry) ResolveTools(pluginCtx RuntimePluginToolContext) ([]RegisteredRuntimeTool, error) {
	if r == nil || len(r.plugins) == 0 {
		return nil, nil
	}
	if r.config.Enabled != nil && !*r.config.Enabled {
		return nil, nil
	}
	existing := map[string]struct{}{}
	for name := range pluginCtx.ExistingToolNames {
		existing[name] = struct{}{}
	}
	var out []RegisteredRuntimeTool
	for _, p := range r.plugins {
		if p == nil {
			continue
		}
		tools, err := p.Tools(context.Background(), pluginCtx)
		if err != nil {
			return nil, err
		}
		if len(tools) == 0 {
			continue
		}
		pluginID := tool.NormalizeToolPolicyName(p.ID())
		if pluginID == "" {
			return nil, fmt.Errorf("runtime plugin returned empty id")
		}
		if pluginpkg.PluginBlockedByConfig(r.config, pluginID) {
			continue
		}
		if !pluginpkg.PluginEnabledByConfig(r.config, pluginID) {
			continue
		}
		if _, conflict := existing[pluginID]; conflict {
			return nil, fmt.Errorf("runtime plugin id conflicts with existing tool name (%s)", pluginID)
		}
		for _, tool := range tools {
			if tool == nil {
				continue
			}
			name := toolfn.NormalizeToolPolicyName(tool.Name())
			if name == "" {
				continue
			}
			if _, conflict := existing[name]; conflict {
				return nil, fmt.Errorf("runtime plugin tool name conflict (%s): %s", pluginID, name)
			}
			meta := core.ToolRegistrationMeta{PluginID: pluginID}
			if provider, ok := tool.(core.ToolMetadataProvider); ok {
				meta = provider.ToolRegistrationMeta()
				if meta.PluginID == "" {
					meta.PluginID = pluginID
				}
			}
			existing[name] = struct{}{}
			out = append(out, RegisteredRuntimeTool{Tool: tool, Meta: meta})
		}
	}
	return out, nil
}
