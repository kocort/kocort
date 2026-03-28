// Package config defines all configuration types and loading logic for kocort.
// This package depends only on internal/core for shared type references.
package config

import (
	"encoding/json"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// AppConfig is the root configuration for the entire kocort runtime.
//
// Directory path convention:
//   - Most directory fields accept relative paths which are resolved against configDir.
//   - SandboxDirs is the exception: it must always use absolute paths.
type AppConfig struct {
	StateDir       string            `json:"stateDir,omitempty"` // relative to configDir; state directory (sessions, transcripts, audit); defaults to configDir
	Models         ModelsConfig      `json:"models"`
	Logging        LoggingConfig     `json:"logging,omitempty"`
	Tools          ToolsConfig       `json:"tools,omitempty"`
	Plugins        PluginsConfig     `json:"plugins,omitempty"`
	Skills         SkillsConfig      `json:"skills,omitempty"`
	Agents         AgentsConfig      `json:"agents,omitempty"`
	Session        SessionConfig     `json:"session,omitempty"`
	ACP            AcpConfigLite     `json:"acp,omitempty"`
	Gateway        GatewayConfig     `json:"gateway,omitempty"`
	Channels       ChannelsConfig    `json:"channels,omitempty"`
	Memory         MemoryConfig      `json:"memory,omitempty"`
	Data           DataConfig        `json:"data,omitempty"`
	Env            EnvironmentConfig `json:"environment,omitempty"`
	Tasks          TasksConfig       `json:"tasks,omitempty"`
	BrainMode      string            `json:"brainMode,omitempty"` // "cloud" (default) or "local"
	BrainLocal     BrainLocalConfig  `json:"brainLocal,omitempty"`
	Cerebellum     CerebellumConfig  `json:"cerebellum,omitempty"`
	Network        NetworkConfig     `json:"network,omitempty"`
	SetupCompleted bool              `json:"setupCompleted,omitempty"`
}

// NetworkConfig configures global network settings such as HTTP proxy.
type NetworkConfig struct {
	UseSystemProxy *bool  `json:"useSystemProxy,omitempty"`
	ProxyURL       string `json:"proxyUrl,omitempty"`
	Language       string `json:"language,omitempty"`
}

func (c NetworkConfig) UseSystemProxyEnabled() bool {
	return c.UseSystemProxy == nil || *c.UseSystemProxy
}

func (c NetworkConfig) LanguageOrDefault() string {
	language := strings.TrimSpace(strings.ToLower(c.Language))
	switch language {
	case "system", "en", "zh":
		return language
	default:
		return "system"
	}
}

func (c NetworkConfig) EffectiveProxyURL() string {
	proxyURL := strings.TrimSpace(c.ProxyURL)
	if proxyURL != "" {
		return proxyURL
	}
	if c.UseSystemProxyEnabled() {
		return "__SYSTEM__"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Data / Environment / Tasks
// ---------------------------------------------------------------------------

type DataConfig struct {
	Entries map[string]DataSourceConfig `json:"entries,omitempty"`
}

type DataSourceConfig struct {
	Enabled *bool    `json:"enabled,omitempty"`
	Type    string   `json:"type,omitempty"`
	Path    string   `json:"path,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Tables  []string `json:"tables,omitempty"`
	MaxRows int      `json:"maxRows,omitempty"`
}

type EnvironmentEntryConfig struct {
	Value    string `json:"value,omitempty"`
	FromEnv  string `json:"fromEnv,omitempty"`
	Masked   *bool  `json:"masked,omitempty"`
	Required *bool  `json:"required,omitempty"`
}

type EnvironmentConfig struct {
	Strict  *bool                             `json:"strict,omitempty"`
	Entries map[string]EnvironmentEntryConfig `json:"entries,omitempty"`
}

type TasksConfig struct {
	Enabled       *bool `json:"enabled,omitempty"`
	TickSeconds   int   `json:"tickSeconds,omitempty"`
	MaxConcurrent int   `json:"maxConcurrent,omitempty"`
}

// BrainLocalConfig configures the brain's (大脑) local model for offline operation.
// Active when BrainMode is "local".
type BrainLocalConfig struct {
	ModelID     string          `json:"modelId,omitempty"`
	ModelsDir   string          `json:"modelsDir,omitempty"`   // relative to configDir
	Threads     int             `json:"threads,omitempty"`     // inference threads (default: 4)
	ContextSize int             `json:"contextSize,omitempty"` // model context size (default: 4096)
	GpuLayers   int             `json:"gpuLayers,omitempty"`   // GPU layers to offload (0=CPU only, -1=all, default: 0)
	AutoStart   *bool           `json:"autoStart,omitempty"`   // auto-start on runtime boot (default: true when enabled)
	Sampling    *SamplingConfig `json:"sampling,omitempty"`
}

// CerebellumConfig configures the local cerebellum (小脑) lightweight model.
// The cerebellum is only active when the brain uses cloud models.
type CerebellumConfig struct {
	Enabled     *bool           `json:"enabled,omitempty"`
	ModelID     string          `json:"modelId,omitempty"`
	ModelsDir   string          `json:"modelsDir,omitempty"`   // relative to configDir
	Threads     int             `json:"threads,omitempty"`     // inference threads (default: 4)
	ContextSize int             `json:"contextSize,omitempty"` // model context size (default: 2048)
	GpuLayers   int             `json:"gpuLayers,omitempty"`   // GPU layers to offload (0=CPU only, -1=all, default: 0)
	AutoStart   *bool           `json:"autoStart,omitempty"`   // auto-start on runtime boot (default: true when enabled)
	Sampling    *SamplingConfig `json:"sampling,omitempty"`
}

// SamplingConfig configures the sampling parameters for local model inference.
type SamplingConfig struct {
	Temp           float32 `json:"temp"`
	TopP           float32 `json:"topP"`
	TopK           int     `json:"topK"`
	MinP           float32 `json:"minP"`
	TypicalP       float32 `json:"typicalP"`
	RepeatLastN    int     `json:"repeatLastN"`
	PenaltyRepeat  float32 `json:"penaltyRepeat"`
	PenaltyFreq    float32 `json:"penaltyFreq"`
	PenaltyPresent float32 `json:"penaltyPresent"`
}

// BrainLocalEnabled returns true if brain mode is "local".
func (c AppConfig) BrainLocalEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.BrainMode), "local")
}

// CerebellumEnabled returns true if the cerebellum is enabled.
// In pure-local brain mode the cerebellum is always disabled because all
// inference runs on the local GGUF model and safety review is skipped.
func (c AppConfig) CerebellumEnabled() bool {
	if c.BrainLocalEnabled() {
		return false
	}
	return c.Cerebellum.Enabled == nil || *c.Cerebellum.Enabled
}

// CerebellumEffectivelyEnabled is an alias for CerebellumEnabled.
// Kept for backward compatibility.
func (c AppConfig) CerebellumEffectivelyEnabled() bool {
	return c.CerebellumEnabled()
}

type LoggingConfig struct {
	Enabled    *bool  `json:"enabled,omitempty"`
	Console    *bool  `json:"console,omitempty"`
	File       string `json:"file,omitempty"` // relative to configDir
	Level      string `json:"level,omitempty"`
	MaxSizeMB  int    `json:"maxSizeMB,omitempty"`  // max size per log file in MB (default: 100)
	MaxAgeDays int    `json:"maxAgeDays,omitempty"` // max days to retain log files (default: 0 = no cleanup)
}

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

type ModelsConfig struct {
	Providers    map[string]ProviderConfig `json:"providers"`
	ModelAliases map[string]string         `json:"modelAliases,omitempty"`
}

type ProviderConfig struct {
	BaseURL          string                     `json:"baseUrl"`
	APIKey           string                     `json:"apiKey"`
	AlternateAPIKeys []string                   `json:"alternateApiKeys,omitempty"`
	API              string                     `json:"api"`
	Models           []ProviderModelConfig      `json:"models"`
	Command          *core.CommandBackendConfig `json:"command,omitempty"`
	HistoryTurnLimit int                        `json:"historyTurnLimit,omitempty"`
}

type ProviderModelConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
}

// ---------------------------------------------------------------------------
// Agent Configuration
// ---------------------------------------------------------------------------

type AgentHeartbeatConfig struct {
	Every            string                           `json:"every,omitempty"`
	Session          string                           `json:"session,omitempty"`
	Prompt           string                           `json:"prompt,omitempty"`
	Target           string                           `json:"target,omitempty"`
	DirectPolicy     string                           `json:"directPolicy,omitempty"`
	To               string                           `json:"to,omitempty"`
	AccountID        string                           `json:"accountId,omitempty"`
	Model            string                           `json:"model,omitempty"`
	AckMaxChars      int                              `json:"ackMaxChars,omitempty"`
	SuppressToolErr  *bool                            `json:"suppressToolErrorWarnings,omitempty"`
	LightContext     *bool                            `json:"lightContext,omitempty"`
	IsolatedSession  *bool                            `json:"isolatedSession,omitempty"`
	IncludeReasoning *bool                            `json:"includeReasoning,omitempty"`
	ActiveHours      *AgentHeartbeatActiveHoursConfig `json:"activeHours,omitempty"`
}

type AgentHeartbeatActiveHoursConfig struct {
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type AgentCompactionMemoryFlushConfig struct {
	Enabled             *bool  `json:"enabled,omitempty"`
	SoftThresholdTokens int    `json:"softThresholdTokens,omitempty"`
	SystemPrompt        string `json:"systemPrompt,omitempty"`
	Prompt              string `json:"prompt,omitempty"`
}

type AgentCompactionConfig struct {
	ReserveTokensFloor int                               `json:"reserveTokensFloor,omitempty"`
	MemoryFlush        *AgentCompactionMemoryFlushConfig `json:"memoryFlush,omitempty"`
}

type AgentContextPruningSoftTrimConfig struct {
	MaxChars  int `json:"maxChars,omitempty"`
	HeadChars int `json:"headChars,omitempty"`
	TailChars int `json:"tailChars,omitempty"`
}

type AgentContextPruningHardClearConfig struct {
	Enabled     *bool  `json:"enabled,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

type AgentContextPruningToolsConfig struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type AgentContextPruningConfig struct {
	Mode                 string                              `json:"mode,omitempty"`
	TTL                  string                              `json:"ttl,omitempty"`
	KeepLastAssistants   int                                 `json:"keepLastAssistants,omitempty"`
	SoftTrimRatio        float64                             `json:"softTrimRatio,omitempty"`
	HardClearRatio       float64                             `json:"hardClearRatio,omitempty"`
	MinPrunableToolChars int                                 `json:"minPrunableToolChars,omitempty"`
	SoftTrim             AgentContextPruningSoftTrimConfig   `json:"softTrim,omitempty"`
	HardClear            *AgentContextPruningHardClearConfig `json:"hardClear,omitempty"`
	Tools                AgentContextPruningToolsConfig      `json:"tools,omitempty"`
}

type AgentModelConfig struct {
	Primary   string   `json:"primary,omitempty"`
	Fallbacks []string `json:"fallbacks,omitempty"`
}

func (c *AgentModelConfig) UnmarshalJSON(data []byte) error {
	type alias AgentModelConfig
	var rawString string
	if err := json.Unmarshal(data, &rawString); err == nil {
		c.Primary = strings.TrimSpace(rawString)
		c.Fallbacks = nil
		return nil
	}
	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Primary = strings.TrimSpace(raw.Primary)
	c.Fallbacks = append([]string{}, raw.Fallbacks...)
	return nil
}

type AgentToolPolicyConfig struct {
	Allow         []string                      `json:"allow,omitempty"`
	AlsoAllow     []string                      `json:"alsoAllow,omitempty"`
	Deny          []string                      `json:"deny,omitempty"`
	Profile       string                        `json:"profile,omitempty"`
	Elevated      *ToolElevatedConfig           `json:"elevated,omitempty"`
	Sandbox       *ToolSandboxConfig            `json:"sandbox,omitempty"`
	LoopDetection *core.ToolLoopDetectionConfig `json:"loopDetection,omitempty"`
}

type AgentMemorySearchConfig struct {
	Enabled    *bool                        `json:"enabled,omitempty"`
	Provider   string                       `json:"provider,omitempty"`
	Fallback   string                       `json:"fallback,omitempty"`
	Model      string                       `json:"model,omitempty"`
	Sources    []string                     `json:"sources,omitempty"`
	ExtraPaths []string                     `json:"extraPaths,omitempty"`
	Query      AgentMemorySearchQueryConfig `json:"query,omitempty"`
	Store      AgentMemorySearchStoreConfig `json:"store,omitempty"`
	Cache      AgentMemorySearchCacheConfig `json:"cache,omitempty"`
	Sync       AgentMemorySearchSyncConfig  `json:"sync,omitempty"`
	Chunking   AgentMemoryChunkingConfig    `json:"chunking,omitempty"`
}

type AgentMemorySearchQueryConfig struct {
	MaxResults int                           `json:"maxResults,omitempty"`
	MinScore   float64                       `json:"minScore,omitempty"`
	Hybrid     AgentMemorySearchHybridConfig `json:"hybrid,omitempty"`
}

type AgentMemorySearchHybridConfig struct {
	Enabled             *bool   `json:"enabled,omitempty"`
	VectorWeight        float64 `json:"vectorWeight,omitempty"`
	TextWeight          float64 `json:"textWeight,omitempty"`
	CandidateMultiplier int     `json:"candidateMultiplier,omitempty"`
}

type AgentMemorySearchStoreConfig struct {
	Path   string                        `json:"path,omitempty"`
	Vector AgentMemorySearchVectorConfig `json:"vector,omitempty"`
}

type AgentMemorySearchVectorConfig struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	ExtensionPath string `json:"extensionPath,omitempty"`
}

type AgentMemorySearchCacheConfig struct {
	Enabled    *bool `json:"enabled,omitempty"`
	MaxEntries int   `json:"maxEntries,omitempty"`
}

type AgentMemorySearchSyncConfig struct {
	OnSearch        *bool `json:"onSearch,omitempty"`
	OnSessionStart  *bool `json:"onSessionStart,omitempty"`
	Watch           *bool `json:"watch,omitempty"`
	WatchDebounceMs int   `json:"watchDebounceMs,omitempty"`
}

type AgentMemoryChunkingConfig struct {
	Tokens  int `json:"tokens,omitempty"`
	Overlap int `json:"overlap,omitempty"`
}

type AgentSubagentConfig struct {
	AllowAgents             []string         `json:"allowAgents,omitempty"`
	Model                   AgentModelConfig `json:"model,omitempty"`
	Thinking                string           `json:"thinking,omitempty"`
	MaxSpawnDepth           int              `json:"maxSpawnDepth,omitempty"`
	MaxChildrenPerAgent     int              `json:"maxChildrenPerAgent,omitempty"`
	ArchiveAfterMinutes     *int             `json:"archiveAfterMinutes,omitempty"`
	TimeoutSeconds          int              `json:"timeoutSeconds,omitempty"`
	AttachmentsEnabled      *bool            `json:"attachmentsEnabled,omitempty"`
	AttachmentMaxFiles      int              `json:"attachmentMaxFiles,omitempty"`
	AttachmentMaxFileBytes  int              `json:"attachmentMaxFileBytes,omitempty"`
	AttachmentMaxTotalBytes int              `json:"attachmentMaxTotalBytes,omitempty"`
	RetainAttachmentsOnKeep *bool            `json:"retainAttachmentsOnKeep,omitempty"`
}

type AgentRuntimeACPConfig struct {
	Agent   string `json:"agent,omitempty"`
	Backend string `json:"backend,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
}

type AgentRuntimeConfig struct {
	Type string                 `json:"type,omitempty"`
	ACP  *AgentRuntimeACPConfig `json:"acp,omitempty"`
}

type AgentIdentityConfig struct {
	Name          string            `json:"name,omitempty"`
	PersonaPrompt string            `json:"personaPrompt,omitempty"`
	Emoji         string            `json:"emoji,omitempty"`
	Theme         string            `json:"theme,omitempty"`
	Avatar        string            `json:"avatar,omitempty"`
	OwnerLine     string            `json:"ownerLine,omitempty"`
	ModelAliases  map[string]string `json:"modelAliases,omitempty"`
}

type AgentDefaultsConfig struct {
	Model           AgentModelConfig           `json:"model,omitempty"`
	Workspace       string                     `json:"workspace,omitempty"`      // relative to configDir; agent context files (SYSTEM.md, IDENTITY.md etc)
	AgentDir        string                     `json:"agentDir,omitempty"`       // relative to configDir; agent private state directory
	SandboxEnabled  *bool                      `json:"sandboxEnabled,omitempty"` // enable sandbox directory restriction (default: false)
	SandboxDirs     []string                   `json:"sandboxDirs,omitempty"`    // ABSOLUTE paths only; sandbox directories for tool operations boundary
	UserTimezone    string                     `json:"userTimezone,omitempty"`
	TimeoutSeconds  int                        `json:"timeoutSeconds,omitempty"`
	ThinkingDefault string                     `json:"thinkingDefault,omitempty"`
	Heartbeat       *AgentHeartbeatConfig      `json:"heartbeat,omitempty"`
	Compaction      *AgentCompactionConfig     `json:"compaction,omitempty"`
	ContextPruning  *AgentContextPruningConfig `json:"contextPruning,omitempty"`
	Skills          []string                   `json:"skills,omitempty"`
	MemorySearch    AgentMemorySearchConfig    `json:"memorySearch,omitempty"`
	Subagents       AgentSubagentConfig        `json:"subagents,omitempty"`
	Tools           AgentToolPolicyConfig      `json:"tools,omitempty"`
	Runtime         *AgentRuntimeConfig        `json:"runtime,omitempty"`
	Identity        *AgentIdentityConfig       `json:"identity,omitempty"`
}

type AgentConfig struct {
	ID              string                     `json:"id"`
	Default         bool                       `json:"default,omitempty"`
	Name            string                     `json:"name,omitempty"`
	Workspace       string                     `json:"workspace,omitempty"`      // relative to configDir; agent context files (SYSTEM.md, IDENTITY.md etc)
	AgentDir        string                     `json:"agentDir,omitempty"`       // relative to configDir; agent private state directory
	SandboxEnabled  *bool                      `json:"sandboxEnabled,omitempty"` // enable sandbox directory restriction (default: false)
	SandboxDirs     []string                   `json:"sandboxDirs,omitempty"`    // ABSOLUTE paths only; sandbox directories for tool operations boundary
	Model           AgentModelConfig           `json:"model,omitempty"`
	Heartbeat       *AgentHeartbeatConfig      `json:"heartbeat,omitempty"`
	Compaction      *AgentCompactionConfig     `json:"compaction,omitempty"`
	ContextPruning  *AgentContextPruningConfig `json:"contextPruning,omitempty"`
	Skills          []string                   `json:"skills,omitempty"`
	MemorySearch    AgentMemorySearchConfig    `json:"memorySearch,omitempty"`
	Subagents       AgentSubagentConfig        `json:"subagents,omitempty"`
	Tools           AgentToolPolicyConfig      `json:"tools,omitempty"`
	Runtime         *AgentRuntimeConfig        `json:"runtime,omitempty"`
	Identity        *AgentIdentityConfig       `json:"identity,omitempty"`
	UserTimezone    string                     `json:"userTimezone,omitempty"`
	TimeoutSeconds  int                        `json:"timeoutSeconds,omitempty"`
	ThinkingDefault string                     `json:"thinkingDefault,omitempty"`
}

type AgentsConfig struct {
	Defaults *AgentDefaultsConfig `json:"defaults,omitempty"`
	List     []AgentConfig        `json:"list,omitempty"`
}

// ---------------------------------------------------------------------------
// Tools Configuration
// ---------------------------------------------------------------------------

type ToolElevatedConfig struct {
	Enabled   *bool               `json:"enabled,omitempty"`
	AllowFrom map[string][]string `json:"allowFrom,omitempty"`
}

type ToolSandboxConfig struct {
	Mode                   string `json:"mode,omitempty"`
	WorkspaceAccess        string `json:"workspaceAccess,omitempty"`
	SessionToolsVisibility string `json:"sessionToolsVisibility,omitempty"`
	Scope                  string `json:"scope,omitempty"`
	WorkspaceRoot          string `json:"workspaceRoot,omitempty"` // relative to configDir
}

type ToolExecConfig struct {
	AllowBackground *bool `json:"allowBackground,omitempty"`
	BackgroundMs    int   `json:"backgroundMs,omitempty"`
	TimeoutSec      int   `json:"timeoutSec,omitempty"`
}

type ToolsConfig struct {
	Exec          *ToolExecConfig               `json:"exec,omitempty"`
	Elevated      *ToolElevatedConfig           `json:"elevated,omitempty"`
	Sandbox       *ToolSandboxConfig            `json:"sandbox,omitempty"`
	LoopDetection *core.ToolLoopDetectionConfig `json:"loopDetection,omitempty"`

	// Browser configuration
	BrowserDriverDir      string `json:"browserDriverDir,omitempty"`      // relative to configDir; path to playwright driver directory
	BrowserAutoInstall    bool   `json:"browserAutoInstall,omitempty"`    // auto-install driver if missing
	BrowserUseSystem      bool   `json:"browserUseSystem,omitempty"`      // use system-installed browser (Chrome/Edge)
	BrowserChannel        string `json:"browserChannel,omitempty"`        // explicit channel: "chrome", "msedge"
	BrowserSkipInstall    bool   `json:"browserSkipInstall,omitempty"`    // skip downloading browsers during install
	BrowserHeadless       *bool  `json:"browserHeadless,omitempty"`       // run browser in headless mode; nil = true (default headless)
	BrowserPersistSession bool   `json:"browserPersistSession,omitempty"` // persist browser session (cookies, localStorage, etc.)
	BrowserUserDataDir    string `json:"browserUserDataDir,omitempty"`    // user data dir for persistent sessions; relative to configDir
}

// ---------------------------------------------------------------------------
// Plugins
// ---------------------------------------------------------------------------

type PluginHooksConfig struct {
	AllowPromptInjection *bool `json:"allowPromptInjection,omitempty"`
}

type PluginEntryConfig struct {
	Enabled *bool              `json:"enabled,omitempty"`
	Hooks   *PluginHooksConfig `json:"hooks,omitempty"`
	APIKey  string             `json:"apiKey,omitempty"`
	Env     map[string]string  `json:"env,omitempty"`
	Config  map[string]any     `json:"config,omitempty"`
}

type PluginLoadConfig struct {
	Paths []string `json:"paths,omitempty"`
}

type PluginsConfig struct {
	Enabled *bool                        `json:"enabled,omitempty"`
	Allow   []string                     `json:"allow,omitempty"`
	Deny    []string                     `json:"deny,omitempty"`
	Load    PluginLoadConfig             `json:"load,omitempty"`
	Entries map[string]PluginEntryConfig `json:"entries,omitempty"`
}

// ---------------------------------------------------------------------------
// Skills
// ---------------------------------------------------------------------------

type SkillsConfig struct {
	AllowBundled []string                   `json:"allowBundled,omitempty"`
	Load         SkillsLoadConfig           `json:"load,omitempty"`
	Install      SkillsInstallConfigLite    `json:"install,omitempty"`
	Limits       SkillsLimitsConfig         `json:"limits,omitempty"`
	Entries      map[string]SkillConfigLite `json:"entries,omitempty"`
}

type SkillsLoadConfig struct {
	ExtraDirs       []string `json:"extraDirs,omitempty"`
	Watch           *bool    `json:"watch,omitempty"`
	WatchDebounceMs int      `json:"watchDebounceMs,omitempty"`
}

type SkillsLimitsConfig struct {
	MaxCandidatesPerRoot     int `json:"maxCandidatesPerRoot,omitempty"`
	MaxSkillsLoadedPerSource int `json:"maxSkillsLoadedPerSource,omitempty"`
	MaxSkillsInPrompt        int `json:"maxSkillsInPrompt,omitempty"`
	MaxSkillsPromptChars     int `json:"maxSkillsPromptChars,omitempty"`
	MaxSkillFileBytes        int `json:"maxSkillFileBytes,omitempty"`
}

type SkillsInstallConfigLite struct {
	PreferBrew  *bool  `json:"preferBrew,omitempty"`
	NodeManager string `json:"nodeManager,omitempty"`
}

type SkillConfigLite struct {
	Enabled *bool             `json:"enabled,omitempty"`
	APIKey  string            `json:"apiKey,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Config  map[string]any    `json:"config,omitempty"`
}

// ---------------------------------------------------------------------------
// Session
// ---------------------------------------------------------------------------

type SessionAgentToAgentConfig struct {
	Enabled bool     `json:"enabled,omitempty"`
	Allow   []string `json:"allow,omitempty"`
}

type SessionResetConfig struct {
	Mode        string `json:"mode,omitempty"`
	AtHour      int    `json:"atHour,omitempty"`
	IdleMinutes int    `json:"idleMinutes,omitempty"`
}

type SessionResetByTypeConfig struct {
	Direct *SessionResetConfig `json:"direct,omitempty"`
	DM     *SessionResetConfig `json:"dm,omitempty"`
	Group  *SessionResetConfig `json:"group,omitempty"`
	Thread *SessionResetConfig `json:"thread,omitempty"`
}

type SessionMaintenanceConfig struct {
	Mode                  string `json:"mode,omitempty"`
	PruneAfter            string `json:"pruneAfter,omitempty"`
	MaxEntries            int    `json:"maxEntries,omitempty"`
	RotateBytes           string `json:"rotateBytes,omitempty"`
	ResetArchiveRetention string `json:"resetArchiveRetention,omitempty"`
}

type SessionConfig struct {
	MainKey             string                        `json:"mainKey,omitempty"`
	DMScope             string                        `json:"dmScope,omitempty"`
	ParentForkMaxTokens int                           `json:"parentForkMaxTokens,omitempty"`
	ResetTriggers       []string                      `json:"resetTriggers,omitempty"`
	IdleMinutes         int                           `json:"idleMinutes,omitempty"`
	Reset               *SessionResetConfig           `json:"reset,omitempty"`
	ResetByType         *SessionResetByTypeConfig     `json:"resetByType,omitempty"`
	ResetByChannel      map[string]SessionResetConfig `json:"resetByChannel,omitempty"`
	Maintenance         *SessionMaintenanceConfig     `json:"maintenance,omitempty"`
	ToolsVisibility     core.SessionToolsVisibility   `json:"toolsVisibility,omitempty"`
	AgentToAgent        SessionAgentToAgentConfig     `json:"agentToAgent,omitempty"`
	SendPolicy          *SessionSendPolicyConfig      `json:"sendPolicy,omitempty"`
}

// SessionSendPolicyConfig mirrors session.SendPolicyConfig for JSON config.
type SessionSendPolicyConfig struct {
	DefaultAction string                  `json:"defaultAction,omitempty"` // "allow" or "deny"
	Rules         []SessionSendPolicyRule `json:"rules,omitempty"`
}

// SessionSendPolicyRule is a single allow/deny rule.
type SessionSendPolicyRule struct {
	Action    string `json:"action"`
	Channel   string `json:"channel,omitempty"`
	ChatType  string `json:"chatType,omitempty"`
	KeyPrefix string `json:"keyPrefix,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ---------------------------------------------------------------------------
// ACP
// ---------------------------------------------------------------------------

type AcpRuntimeConfigLite struct {
	TTLMinutes int `json:"ttlMinutes,omitempty"`
}

type AcpConfigLite struct {
	Enabled bool                 `json:"enabled,omitempty"`
	Backend string               `json:"backend,omitempty"`
	Runtime AcpRuntimeConfigLite `json:"runtime,omitempty"`
}

// ---------------------------------------------------------------------------
// Gateway
// ---------------------------------------------------------------------------

type GatewayAuthConfig struct {
	Mode  string `json:"mode,omitempty"`
	Token string `json:"token,omitempty"`
}

type GatewayWebchatConfig struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	AllowedOrigins []string `json:"allowedOrigins,omitempty"`
	BasePath       string   `json:"basePath,omitempty"`
}

type GatewayConfig struct {
	Enabled bool                  `json:"enabled,omitempty"`
	Bind    string                `json:"bind,omitempty"`
	Port    int                   `json:"port,omitempty"`
	Auth    *GatewayAuthConfig    `json:"auth,omitempty"`
	Webchat *GatewayWebchatConfig `json:"webchat,omitempty"`
}

// ---------------------------------------------------------------------------
// Channels
// ---------------------------------------------------------------------------

type ChannelDefaultsConfig struct {
	DefaultAgent   string                            `json:"defaultAgent,omitempty"`
	DefaultAccount string                            `json:"defaultAccount,omitempty"`
	AllowFrom      []string                          `json:"allowFrom,omitempty"`
	TextChunkLimit int                               `json:"textChunkLimit,omitempty"`
	ChunkMode      string                            `json:"chunkMode,omitempty"`
	Heartbeat      *ChannelHeartbeatVisibilityConfig `json:"heartbeat,omitempty"`
}

type ChannelHeartbeatVisibilityConfig struct {
	ShowOK       *bool `json:"showOk,omitempty"`
	ShowAlerts   *bool `json:"showAlerts,omitempty"`
	UseIndicator *bool `json:"useIndicator,omitempty"`
}

type ChannelConfig struct {
	Enabled        *bool                             `json:"enabled,omitempty"`
	DefaultTo      string                            `json:"defaultTo,omitempty"`
	DefaultAccount string                            `json:"defaultAccount,omitempty"`
	Agent          string                            `json:"agent,omitempty"`
	InboundToken   string                            `json:"inboundToken,omitempty"`
	AllowFrom      []string                          `json:"allowFrom,omitempty"`
	TextChunkLimit int                               `json:"textChunkLimit,omitempty"`
	ChunkMode      string                            `json:"chunkMode,omitempty"`
	Heartbeat      *ChannelHeartbeatVisibilityConfig `json:"heartbeat,omitempty"`
	Accounts       map[string]any                    `json:"accounts,omitempty"`
	Config         map[string]any                    `json:"config,omitempty"`
}

type ChannelsConfig struct {
	Defaults *ChannelDefaultsConfig   `json:"defaults,omitempty"`
	Entries  map[string]ChannelConfig `json:"entries,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for ChannelsConfig.
// If the JSON value is not an object (e.g. an array from a legacy or
// misconfigured channels.json), it logs a warning and returns a zero
// value instead of returning an error that would block startup.
func (c *ChannelsConfig) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		return nil
	}
	// If the JSON is not an object, skip it gracefully.
	if len(trimmed) == 0 || trimmed[0] != '{' {
		*c = ChannelsConfig{}
		return nil
	}
	// Use an alias to avoid infinite recursion.
	type alias ChannelsConfig
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		*c = ChannelsConfig{}
		return nil
	}
	*c = ChannelsConfig(tmp)
	return nil
}

// ---------------------------------------------------------------------------
// Memory
// ---------------------------------------------------------------------------

type MemoryConfig struct {
	Backend   string           `json:"backend,omitempty"`
	Citations string           `json:"citations,omitempty"`
	QMD       *MemoryQMDConfig `json:"qmd,omitempty"`
}

type MemoryQMDConfig struct {
	Command    string                 `json:"command,omitempty"`
	SearchMode string                 `json:"searchMode,omitempty"`
	Paths      []MemoryQMDIndexPath   `json:"paths,omitempty"`
	Limits     *MemoryQMDLimitsConfig `json:"limits,omitempty"`
}

type MemoryQMDIndexPath struct {
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

type MemoryQMDLimitsConfig struct {
	MaxResults       int `json:"maxResults,omitempty"`
	MaxSnippetChars  int `json:"maxSnippetChars,omitempty"`
	MaxInjectedChars int `json:"maxInjectedChars,omitempty"`
	TimeoutMs        int `json:"timeoutMs,omitempty"`
}

// ---------------------------------------------------------------------------
// Runtime Init Params
// ---------------------------------------------------------------------------

type RuntimeConfigParams struct {
	AgentID    string
	StateDir   string
	Provider   string
	Model      string
	Deliverer  core.Deliverer
	Memory     core.MemoryProvider
	ToolPolicy core.SessionToolsVisibility
	A2A        core.AgentToAgentPolicy
	ConfigLoad ConfigLoadOptions
}

type ConfigLoadOptions struct {
	ConfigDir          string
	ConfigPath         string
	ModelsConfigPath   string
	ChannelsConfigPath string
}
