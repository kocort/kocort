package runtime

import (
	"context"
	"log/slog"
	"strings"

	"github.com/kocort/kocort/internal/backend"
	cerebellumpkg "github.com/kocort/kocort/internal/cerebellum"
	"github.com/kocort/kocort/internal/channel"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/heartbeat"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/localmodel"
	memorypkg "github.com/kocort/kocort/internal/memory"
	"github.com/kocort/kocort/internal/rtypes"
	sessionpkg "github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

// RuntimePolicy groups value-type policy fields that don't require methods.
// Collapsing them into a single struct reduces the Runtime field count.
type RuntimePolicy struct {
	SessionToolsVisibility core.SessionToolsVisibility
	AgentToAgent           core.AgentToAgentPolicy
}

// Runtime is the core orchestration layer for the kocort agent system.
//
// Fields marked with interface types (SessionManager, BackendResolver, etc.)
// can be substituted with test doubles or alternative implementations.
// Fields that remain concrete types have cross-package dependencies that
// prevent interface conversion at this time (see interfaces.go for details).
type Runtime struct {
	Config        config.AppConfig
	ConfigStore   *config.RuntimeConfigStore
	Environment   EnvironmentResolver      // interface (was *infra.EnvironmentRuntime)
	Logger        RuntimeLogReloader       // interface (was *infra.SlogAuditLogger)
	Audit         AuditRecorder            // interface (was *infra.AuditLog)
	Sessions      *sessionpkg.SessionStore // concrete: SweepOrphans + GetSessions() need it
	Processes     *tool.ProcessRegistry
	SystemEvents  *infra.SystemEventQueue
	Heartbeats    *heartbeat.HeartbeatRunner
	Identities    core.IdentityResolver
	Memory        core.MemoryProvider
	Backend       rtypes.Backend
	Backends      BackendResolver // interface (was *backend.BackendRegistry)
	Deliverer     core.Deliverer
	EventHub      EventBus // interface (was *gateway.EventHub)
	Channels      *channel.ChannelManager
	Subagents     *task.SubagentRegistry
	Tasks         *task.TaskScheduler
	Queue         *task.FollowupQueue
	ActiveRuns    *task.ActiveRunRegistry
	ToolLoops     *tool.ToolLoopRegistry
	Tools         *tool.ToolRegistry
	Plugins       *RuntimePluginRegistry
	Approvals     tool.ToolApprovalRunner
	Hooks         delivery.OutboundHookRunner
	Policy        RuntimePolicy // groups SessionToolsVisibility + AgentToAgent
	Cerebellum    *cerebellumpkg.Manager
	BrainLocal    *localmodel.Manager
	ProxyProvider *infra.AtomicProxyProvider // shared mutable proxy source
	HTTPClient    *infra.DynamicHTTPClient   // global dynamic proxy-aware HTTP client
}

// ReloadEnvironment reloads the runtime environment from config.
func (r *Runtime) ReloadEnvironment() {
	if r == nil || r.Environment == nil {
		return
	}
	r.Environment.Reload(r.Config.Env)
	event.RecordAudit(context.Background(), r.Audit, r.Logger, core.AuditEvent{
		Category: core.AuditCategoryEnvironment,
		Type:     "environment_reloaded",
		Level:    "info",
		Message:  "runtime environment reloaded",
	})
}

// ApplyConfig applies a new configuration to the runtime.
func (r *Runtime) ApplyConfig(cfg config.AppConfig) error {
	if r == nil {
		return nil
	}
	r.Config = cfg
	stateDir := ""
	if r.Sessions != nil {
		stateDir = strings.TrimSpace(r.Sessions.BaseDir())
	}
	if r.Logger != nil {
		if err := r.Logger.Reload(cfg.Logging, stateDir); err != nil {
			return err
		}
	}
	// Keep the global slog level in sync with the (possibly updated) config.
	infra.ApplySlogLevel(cfg.Logging)
	if r.Environment != nil {
		r.Environment.Reload(cfg.Env)
	}
	effectiveProxyURL := cfg.Network.EffectiveProxyURL()
	// Update the shared proxy provider so all DynamicHTTPClients pick up the change.
	if r.ProxyProvider != nil {
		r.ProxyProvider.Set(effectiveProxyURL)
	}
	if r.Sessions != nil {
		if identities, err := config.BuildConfiguredIdentityMap(cfg, stateDir); err == nil {
			r.Identities = infra.NewStaticIdentityResolver(identities)
		} else {
			return err
		}
	}
	r.Memory = memorypkg.NewManager(cfg)
	if cfg.BrainLocalEnabled() && r.BrainLocal != nil {
		// In local mode, auto-start the local model if not already running,
		// then use the local model backend for all agent runs.
		if r.BrainLocal.Status() != localmodel.StatusRunning {
			autoStart := cfg.BrainLocal.AutoStart == nil || *cfg.BrainLocal.AutoStart
			if autoStart && r.BrainLocal.ModelID() != "" {
				slog.Info("[runtime] local brain mode: model not running, attempting auto-start", "status", r.BrainLocal.Status())
				if startErr := r.BrainLocal.Start(); startErr != nil {
					slog.Warn("[runtime] local brain auto-start failed", "error", startErr)
					event.RecordAudit(context.Background(), r.Audit, r.Logger, core.AuditEvent{
						Category: "brain_local",
						Type:     "brain_local_autostart_failed",
						Level:    "warning",
						Message:  "brain local model auto-start failed during config apply: " + startErr.Error(),
					})
				} else {
					slog.Info("[runtime] local brain model auto-started successfully", "status", r.BrainLocal.Status())
				}
			}
		} else {
			slog.Info("[runtime] local brain mode active, model already running")
		}
		r.Backend = backend.NewLocalModelBackend(r.BrainLocal)
		r.Backends = nil
	} else if envConcrete, ok := r.Environment.(*infra.EnvironmentRuntime); ok {
		r.Backends = backend.NewBackendRegistry(cfg, envConcrete, r.HTTPClient)
		r.Backend = backend.NewOpenAICompatBackend(cfg, envConcrete, r.HTTPClient)
	}
	if r.Channels != nil {
		r.Channels.ApplyConfig(cfg.Channels)
		r.Channels.RestartChannelBackgrounds(context.Background(), r)
	} else {
		r.Channels = channel.NewChannelManager(cfg.Channels)
		r.Channels.SetDynamicHTTPClient(r.HTTPClient)
	}
	r.Plugins = NewRuntimePluginRegistry(cfg.Plugins)
	r.Policy.SessionToolsVisibility = cfg.Session.ToolsVisibility
	r.Policy.AgentToAgent = core.AgentToAgentPolicy{
		Enabled: cfg.Session.AgentToAgent.Enabled,
		Allow:   append([]string{}, cfg.Session.AgentToAgent.Allow...),
	}
	if deliverer, ok := r.Deliverer.(*delivery.RouterDeliverer); ok {
		deliverer.Channels = r.Channels
	}
	event.RecordAudit(context.Background(), r.Audit, r.Logger, core.AuditEvent{
		Category: core.AuditCategoryConfig,
		Type:     "runtime_config_applied",
		Level:    "info",
		Message:  "runtime configuration updated through api layer",
	})
	return nil
}

// PersistConfig saves the current configuration to disk.
func (r *Runtime) PersistConfig(mainChanged bool, modelsChanged bool, channelsChanged bool) error {
	if r == nil || r.ConfigStore == nil {
		return nil
	}
	return r.ConfigStore.SaveSections(r.Config, mainChanged, modelsChanged, channelsChanged)
}

// ---------------------------------------------------------------------------
// Event / audit accessors (implement tool.RuntimeServices)
// ---------------------------------------------------------------------------

// GetAudit returns the audit recorder.
func (r *Runtime) GetAudit() event.AuditRecorder {
	if r == nil {
		return nil
	}
	return r.Audit
}

// GetEventBus returns the event bus (webchat hub).
func (r *Runtime) GetEventBus() event.EventBus {
	if r == nil {
		return nil
	}
	return r.EventHub
}

// IdentitySnapshot returns all configured agent identities.
func (r *Runtime) IdentitySnapshot() []core.AgentIdentity {
	resolver, ok := r.Identities.(*infra.StaticIdentityResolver)
	if !ok || resolver == nil {
		return nil
	}
	return resolver.List()
}
