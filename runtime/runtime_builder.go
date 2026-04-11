// runtime_builder.go — Builder pattern for constructing a Runtime.
//
// RuntimeBuilder breaks the monolithic NewRuntimeFromConfig function into
// composable, overridable steps. Each With* method returns the builder
// itself for chaining. The Build() method performs validation and produces
// a fully wired *Runtime.
//
// Existing callers can continue to use NewRuntimeFromConfig, which now
// delegates to RuntimeBuilder internally.
package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/backend"
	cerebellumpkg "github.com/kocort/kocort/internal/cerebellum"
	"github.com/kocort/kocort/internal/channel"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/heartbeat"
	hookspkg "github.com/kocort/kocort/internal/hooks"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/localmodel"
	"github.com/kocort/kocort/internal/localmodel/ffi"
	memorypkg "github.com/kocort/kocort/internal/memory"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"

	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// RuntimeBuilder
// ---------------------------------------------------------------------------

// RuntimeBuilder constructs a Runtime step by step.
// Required inputs are the AppConfig and RuntimeConfigParams (set via New).
// All subsystems have sensible defaults; use With* methods to override.
type RuntimeBuilder struct {
	cfg    config.AppConfig
	params config.RuntimeConfigParams

	// Optional overrides — when nil, Build() creates defaults.
	environment EnvironmentResolver
	logger      RuntimeLogReloader
	audit       AuditRecorder
	sessions    *session.SessionStore
	backends    BackendResolver
	deliverer   core.Deliverer
	eventHub    *gateway.EventHub // concrete: satisfies both EventBus and delivery.EventNotifier
	channels    *channel.ChannelManager
	memory      core.MemoryProvider
	identities  core.IdentityResolver
	tools       []tool.Tool
	plugins     *RuntimePluginRegistry
	approvals   tool.ToolApprovalRunner
}

// NewRuntimeBuilder creates a builder pre-loaded with configuration and
// params. Call With* methods to override defaults, then Build().
func NewRuntimeBuilder(cfg config.AppConfig, params config.RuntimeConfigParams) *RuntimeBuilder {
	return &RuntimeBuilder{cfg: cfg, params: params}
}

// ---------------------------------------------------------------------------
// With* chainable setters
// ---------------------------------------------------------------------------

// WithEnvironment overrides the default EnvironmentRuntime.
func (b *RuntimeBuilder) WithEnvironment(env EnvironmentResolver) *RuntimeBuilder {
	b.environment = env
	return b
}

// WithLogger overrides the default RuntimeLogger.
func (b *RuntimeBuilder) WithLogger(l RuntimeLogReloader) *RuntimeBuilder {
	b.logger = l
	return b
}

// WithAudit overrides the default AuditLog.
func (b *RuntimeBuilder) WithAudit(a AuditRecorder) *RuntimeBuilder {
	b.audit = a
	return b
}

// WithSessions overrides the default SessionStore.
func (b *RuntimeBuilder) WithSessions(s *session.SessionStore) *RuntimeBuilder {
	b.sessions = s
	return b
}

// WithBackends overrides the default BackendRegistry.
func (b *RuntimeBuilder) WithBackends(br BackendResolver) *RuntimeBuilder {
	b.backends = br
	return b
}

// WithDeliverer overrides the default Deliverer.
func (b *RuntimeBuilder) WithDeliverer(d core.Deliverer) *RuntimeBuilder {
	b.deliverer = d
	return b
}

// WithEventHub overrides the default EventHub.
func (b *RuntimeBuilder) WithEventHub(h *gateway.EventHub) *RuntimeBuilder {
	b.eventHub = h
	return b
}

// WithWebchat is an alias for WithEventHub for backward compatibility.
// Deprecated: Use WithEventHub instead.
func (b *RuntimeBuilder) WithWebchat(w *gateway.EventHub) *RuntimeBuilder {
	return b.WithEventHub(w)
}

// WithChannels overrides the default ChannelRegistry.
func (b *RuntimeBuilder) WithChannels(ch *channel.ChannelManager) *RuntimeBuilder {
	b.channels = ch
	return b
}

// WithMemory overrides the default MemoryProvider.
func (b *RuntimeBuilder) WithMemory(m core.MemoryProvider) *RuntimeBuilder {
	b.memory = m
	return b
}

// WithIdentities overrides the default IdentityResolver.
func (b *RuntimeBuilder) WithIdentities(id core.IdentityResolver) *RuntimeBuilder {
	b.identities = id
	return b
}

// WithTools sets the initial tool list (replaces defaults).
func (b *RuntimeBuilder) WithTools(tools ...tool.Tool) *RuntimeBuilder {
	b.tools = tools
	return b
}

// WithPlugins overrides the default plugin registry.
func (b *RuntimeBuilder) WithPlugins(p *RuntimePluginRegistry) *RuntimeBuilder {
	b.plugins = p
	return b
}

// WithApprovals overrides the default tool approval runner.
func (b *RuntimeBuilder) WithApprovals(runner tool.ToolApprovalRunner) *RuntimeBuilder {
	b.approvals = runner
	return b
}

// ---------------------------------------------------------------------------
// Build
// ---------------------------------------------------------------------------

// Build constructs and returns a fully wired *Runtime.
// It resolves defaults for any subsystem not explicitly overridden,
// starts background services (tasks, heartbeats, channel runners),
// and records an initialization audit event.
func (b *RuntimeBuilder) Build() (*Runtime, error) {
	// ── Phase A: resolve paths & identities ──────────────────────────
	stateDir := strings.TrimSpace(b.params.StateDir)
	if stateDir == "" {
		stateDir = config.ResolveStateDirFromConfig(b.cfg, strings.TrimSpace(b.params.ConfigLoad.ConfigDir))
	}
	agentID := session.NormalizeAgentID(b.params.AgentID)
	if agentID == "" {
		agentID = config.ResolveDefaultConfiguredAgentID(b.cfg)
	}
	identity, err := config.BuildConfiguredAgentIdentity(
		b.cfg, stateDir, agentID,
		b.params.Provider, b.params.Model, "",
	)
	if err != nil {
		return nil, err
	}
	effectiveProxyURL := b.cfg.Network.EffectiveProxyURL()

	// ── Proxy infrastructure: create the shared dynamic HTTP client ──
	// All components share the same AtomicProxyProvider and DynamicHTTPClient,
	// so when the proxy config changes (via ApplyConfig), every HTTP request
	// automatically picks up the new proxy setting.
	proxyProvider := infra.NewAtomicProxyProvider(effectiveProxyURL)
	dynamicHTTPClient := infra.NewDynamicHTTPClient(proxyProvider, 0)

	// ── Phase B: infrastructure ──────────────────────────────────────
	sessions := b.sessions
	if sessions == nil {
		sessions, err = session.NewSessionStore(stateDir)
		if err != nil {
			return nil, err
		}
	}
	sessions.SetMaintenanceConfig(session.NewSessionStoreMaintenanceConfig(b.cfg.Session.Maintenance))

	var audit AuditRecorder = b.audit
	if audit == nil {
		audit, err = infra.NewAuditLog(stateDir)
		if err != nil {
			return nil, err
		}
	}

	var logger RuntimeLogReloader = b.logger
	if logger == nil {
		logger, err = infra.NewSlogAuditLogger(b.cfg.Logging, stateDir)
		if err != nil {
			return nil, err
		}
	}

	// Apply the configured log level to the global slog handler so that all
	// slog.Debug / slog.Info / slog.Warn / slog.Error calls respect it.
	infra.ApplySlogLevel(b.cfg.Logging)

	// Environment — keep concrete *infra.EnvironmentRuntime for backend constructors.
	var envConcrete *infra.EnvironmentRuntime
	if b.environment != nil {
		if concrete, ok := b.environment.(*infra.EnvironmentRuntime); ok {
			envConcrete = concrete
		}
	}
	if envConcrete == nil {
		envConcrete = infra.NewEnvironmentRuntime(b.cfg.Env)
	}

	// ── Phase C: delivery ────────────────────────────────────────────
	deliverer := b.deliverer
	if deliverer == nil {
		if b.params.Deliverer != nil {
			deliverer = b.params.Deliverer
		} else {
			deliverer = &delivery.StdoutDeliverer{}
		}
	}
	eventHub := b.eventHub
	if eventHub == nil {
		eventHub = gateway.NewEventHub()
	}
	channels := b.channels
	if channels == nil {
		channels = channel.NewChannelManager(b.cfg.Channels)
		for channelID, entry := range b.cfg.Channels.Entries {
			channels.RegisterChannelByConfig(channelID, entry)
		}
	}
	auditLog, _ := audit.(*infra.AuditLog) // may be nil if custom audit
	routerDeliverer := &delivery.RouterDeliverer{
		Fallback:         deliverer,
		Events:           eventHub,
		Channels:         channels,
		Sessions:         sessions,
		Audit:            auditLog,
		NormalizeChannel: backend.NormalizeProviderID,
		ChunkText: func(text string, outbound any, cfg config.ChannelConfig, accountID string) []string {
			return channel.ChunkOutboundTextForAdapter(text, outbound, cfg, accountID)
		},
	}

	// ── Phase D: identities & memory ─────────────────────────────────
	memoryProvider := b.memory
	if memoryProvider == nil {
		if b.params.Memory != nil {
			memoryProvider = b.params.Memory
		} else {
			memoryProvider = memorypkg.NewManager(b.cfg)
		}
	}

	identities := b.identities
	if identities == nil {
		identityMap, buildErr := config.BuildConfiguredIdentityMap(b.cfg, stateDir)
		if buildErr != nil {
			return nil, buildErr
		}
		identityMap[identity.ID] = identity
		identities = infra.NewStaticIdentityResolver(identityMap)
	}

	// ── Phase E: tools & plugins ─────────────────────────────────────
	toolList := b.tools
	if toolList == nil {
		headless := b.cfg.Tools.BrowserHeadless
		toolList = []tool.Tool{
			tool.NewReadTool(),
			tool.NewWriteTool(),
			tool.NewEditTool(),
			tool.NewApplyPatchTool(),
			tool.NewGrepTool(),
			tool.NewFindTool(),
			tool.NewLSTool(),
			tool.NewCronTool(),
			tool.NewExecTool(b.cfg.Tools.Exec),
			tool.NewWebSearchTool(dynamicHTTPClient),
			tool.NewWebFetchTool(dynamicHTTPClient),
			tool.NewBrowserTool(tool.BrowserToolOptions{
				Headless:    headless == nil || *headless,
				ArtifactDir: filepath.Join(stateDir, "browser"),
				Profile:     b.cfg.Tools.BrowserUserDataDir,
			}),
			tool.NewAgentsListTool(),
			tool.NewGatewayTool(),
			tool.NewImageTool(),
			tool.NewMessageTool(),
			tool.NewMemoryGetTool(),
			tool.NewMemorySearchTool(),
			tool.NewProcessTool(),
			tool.NewSessionStatusTool(),
			tool.NewSessionsHistoryTool(),
			tool.NewSessionsListTool(),
			tool.NewSessionsSendTool(),
			tool.NewSessionsSpawnTool(),
			tool.NewSessionsYieldTool(),
			tool.NewSubagentsTool(),
		}
	}
	plugins := b.plugins
	if plugins == nil {
		plugins = NewRuntimePluginRegistry(b.cfg.Plugins)
	}

	// ── Phase E2: cerebellum ────────────────────────────────────────
	// Configure llama.cpp library version/GPU from persisted config.
	// Use configDir/lib as the library cache so downloads land alongside config.
	configDir := strings.TrimSpace(b.params.ConfigLoad.ConfigDir)
	libCacheDir := ""
	if configDir != "" {
		libCacheDir = filepath.Join(configDir, "lib")
	}
	ffi.SetLibraryConfig(b.cfg.LlamaCpp.Version, b.cfg.LlamaCpp.GPUType, libCacheDir, dynamicHTTPClient.Client())

	cerebellum := cerebellumpkg.NewManager(b.cfg.Cerebellum)
	cerebellum.SetDynamicHTTPClient(dynamicHTTPClient)

	// ── Phase E3: brain local model ─────────────────────────────────
	brainLocal := localmodel.NewManager(localmodel.Config{
		ModelID:        b.cfg.BrainLocal.ModelID,
		ModelsDir:      b.cfg.BrainLocal.ModelsDir,
		Threads:        b.cfg.BrainLocal.Threads,
		ContextSize:    b.cfg.BrainLocal.ContextSize,
		GpuLayers:      b.cfg.BrainLocal.GpuLayers,
		Sampling:       configSamplingToLocal(b.cfg.BrainLocal.Sampling),
		EnableThinking: localmodel.ResolveEnableThinkingDefault(b.cfg.BrainLocal.EnableThinking, b.cfg.BrainLocal.ModelID, b.cfg.BrainLocal.ModelsDir, localmodel.BuiltinBrainCatalog),
	}, localmodel.BuiltinBrainCatalog)
	brainLocal.SetDynamicHTTPClient(dynamicHTTPClient)

	// ── Phase F: assemble Runtime ────────────────────────────────────
	processCleanupTTL := time.Duration(0)
	if b.cfg.Tools.Exec != nil && b.cfg.Tools.Exec.CleanupMs > 0 {
		processCleanupTTL = time.Duration(b.cfg.Tools.Exec.CleanupMs) * time.Millisecond
	}
	rt := &Runtime{
		Config:        b.cfg,
		ConfigStore:   config.NewRuntimeConfigStore(b.params.ConfigLoad),
		Environment:   envConcrete,
		Logger:        logger,
		Audit:         audit,
		Sessions:      sessions,
		SystemEvents:  infra.NewSystemEventQueue(),
		Identities:    identities,
		Memory:        memoryProvider,
		Backend:       backend.NewOpenAICompatBackend(b.cfg, envConcrete, dynamicHTTPClient),
		Deliverer:     routerDeliverer,
		EventHub:      eventHub,
		Channels:      channels,
		Subagents:     task.NewSubagentRegistry(),
		Tasks:         nil,
		Queue:         task.NewFollowupQueue(),
		ActiveRuns:    task.NewActiveRunRegistry(),
		ToolLoops:     tool.NewToolLoopRegistry(),
		Processes:     tool.NewProcessRegistry(tool.ProcessRegistryOptions{CleanupTTL: processCleanupTTL}),
		Tools:         tool.NewToolRegistry(toolList...),
		Plugins:       plugins,
		Approvals:     b.approvals,
		Hooks:         nil,
		InternalHooks: hookspkg.NewRegistry(),
		Policy: RuntimePolicy{
			SessionToolsVisibility: b.params.ToolPolicy,
			AgentToAgent:           b.params.A2A,
		},
		Cerebellum:    cerebellum,
		BrainLocal:    brainLocal,
		ProxyProvider: proxyProvider,
		HTTPClient:    dynamicHTTPClient,
	}
	channels.SetDynamicHTTPClient(dynamicHTTPClient)

	// Backends (interface field).
	if b.backends != nil {
		rt.Backends = b.backends
	} else {
		rt.Backends = backend.NewBackendRegistry(b.cfg, envConcrete, dynamicHTTPClient)
	}

	// ── Phase G: post-assembly wiring ────────────────────────────────
	rt.Heartbeats = heartbeat.NewHeartbeatRunner(rt)
	rt.Subagents.SetArchiveHandler(func(entry task.SubagentRunRecord) {
		if rt.Sessions != nil {
			_ = rt.Sessions.Delete(entry.ChildSessionKey)
		}
	})
	if rt.Sessions != nil {
		rt.Subagents.SetArchivePath(filepath.Join(rt.Sessions.BaseDir(), "subagents", "runs.json"))
		rt.Subagents.SetStatePath(filepath.Join(rt.Sessions.BaseDir(), "subagents", "state.json"))
		_ = rt.Subagents.RestoreFromState() // best-effort restore; empty/missing state is acceptable
		rt.restoreSubagentAnnouncementRecovery()
		// Attempt to resume subagent runs that were interrupted by a
		// previous process exit. This is deferred slightly so the rest
		// of the runtime has time to finish wiring.
		go func() {
			time.Sleep(task.DefaultOrphanRecoveryDelay)
			rt.recoverOrphanedSubagentRuns()
		}()
	}
	if rt.Policy.SessionToolsVisibility == "" {
		rt.Policy.SessionToolsVisibility = b.cfg.Session.ToolsVisibility
	}
	if !rt.Policy.AgentToAgent.Enabled && len(rt.Policy.AgentToAgent.Allow) == 0 {
		rt.Policy.AgentToAgent = core.AgentToAgentPolicy{
			Enabled: b.cfg.Session.AgentToAgent.Enabled,
			Allow:   append([]string{}, b.cfg.Session.AgentToAgent.Allow...),
		}
	}

	// ── Phase H: start background services ───────────────────────────
	tasks, err := task.NewTaskScheduler(stateDir, b.cfg.Tasks)
	if err != nil {
		return nil, err
	}
	rt.Tasks = tasks
	rt.Tasks.SetEventSink(func(record core.TaskRecord, eventType string, data map[string]any) {
		event.RecordAudit(context.Background(), rt.Audit, rt.Logger, core.AuditEvent{
			Category:   core.AuditCategoryTask,
			Type:       strings.TrimSpace(eventType),
			Level:      "info",
			AgentID:    record.AgentID,
			SessionKey: record.SessionKey,
			RunID:      record.RunID,
			TaskID:     record.ID,
			Message:    utils.NonEmpty(strings.TrimSpace(record.Title), strings.TrimSpace(record.Message)),
			Data:       data,
		})
	})
	rt.Tasks.SetFailureNotifier(func(ctx context.Context, rec core.TaskRecord) error {
		return task.NotifyTaskFailure(ctx, rt.Deliverer, rt.Tasks, rt.Audit, rec)
	})
	rt.Tasks.Start(context.Background(), rt, b.cfg.Tasks)
	rt.SetHeartbeatsEnabled(true)
	rt.Heartbeats.Start()

	// Start delivery replay worker for retrying failed deliveries.
	if strings.TrimSpace(stateDir) != "" && rt.Deliverer != nil {
		go delivery.StartReplayWorker(context.Background(), stateDir, rt.Deliverer, 30*time.Second, 20)
	}

	if rt.Channels != nil {
		rt.Channels.RestartChannelBackgrounds(context.Background(), rt)
	}
	if resumed := rt.ResumeAllPersistentACPSessions(context.Background()); len(resumed) > 0 {
		event.RecordAudit(context.Background(), rt.Audit, rt.Logger, core.AuditEvent{
			Category: core.AuditCategoryConfig,
			Type:     "acp_sessions_resumed",
			Level:    "info",
			Message:  "persistent ACP sessions resumed during runtime startup",
			Data:     map[string]any{"count": len(resumed)},
		})
	}

	// ── Phase I: auto-start local models ─────────────────────────────
	// Auto-start brain local model if in local mode and autoStart is not explicitly false.
	if b.cfg.BrainLocalEnabled() {
		autoStart := b.cfg.BrainLocal.AutoStart == nil || *b.cfg.BrainLocal.AutoStart
		if autoStart && rt.BrainLocal != nil && rt.BrainLocal.ModelID() != "" {
			if startErr := rt.BrainLocal.Start(); startErr != nil {
				event.RecordAudit(context.Background(), rt.Audit, rt.Logger, core.AuditEvent{
					Category: "brain_local",
					Type:     "brain_local_autostart_failed",
					Level:    "warning",
					Message:  "brain local model auto-start failed: " + startErr.Error(),
				})
			}
		}
		// In local mode, override the default backend with the local model backend.
		if rt.BrainLocal != nil {
			rt.Backend = backend.NewLocalModelBackend(rt.BrainLocal)
			rt.Backends = nil // no cloud backend registry needed
		}
	}
	// Cerebellum auto-starts only in cloud mode (CerebellumEnabled returns
	// false when BrainMode is "local").
	if b.cfg.CerebellumEnabled() && rt.Cerebellum != nil {
		autoStart := b.cfg.Cerebellum.AutoStart == nil || *b.cfg.Cerebellum.AutoStart
		if autoStart && rt.Cerebellum.ModelID() != "" {
			if startErr := rt.Cerebellum.Start(); startErr != nil {
				event.RecordAudit(context.Background(), rt.Audit, rt.Logger, core.AuditEvent{
					Category: core.AuditCategoryCerebellum,
					Type:     "cerebellum_autostart_failed",
					Level:    "warning",
					Message:  "cerebellum auto-start failed: " + startErr.Error(),
				})
			}
		}
	}

	// ── record initialization event ──────────────────────────────────
	event.RecordAudit(context.Background(), rt.Audit, rt.Logger, core.AuditEvent{
		Category: core.AuditCategoryConfig,
		Type:     "runtime_initialized",
		Level:    "info",
		AgentID:  agentID,
		Message:  "runtime initialized from config",
		Data:     map[string]any{"stateDir": stateDir},
	})

	return rt, nil
}

// configSamplingToLocal converts config.SamplingConfig to localmodel.SamplingParams.
func configSamplingToLocal(sc *config.SamplingConfig) *localmodel.SamplingParams {
	if sc == nil {
		return nil
	}
	return &localmodel.SamplingParams{
		Temp:           sc.Temp,
		TopP:           sc.TopP,
		TopK:           sc.TopK,
		MinP:           sc.MinP,
		TypicalP:       sc.TypicalP,
		RepeatLastN:    sc.RepeatLastN,
		PenaltyRepeat:  sc.PenaltyRepeat,
		PenaltyFreq:    sc.PenaltyFreq,
		PenaltyPresent: sc.PenaltyPresent,
	}
}
