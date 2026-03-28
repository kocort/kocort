package types

// Core API request and response types.

import (
	"time"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

// ─────────────────────────────────────────────────────────────────────────────
// Chat types
// ─────────────────────────────────────────────────────────────────────────────

// ChatSendRequest represents a chat message send request.
type ChatSendRequest struct {
	SessionKey  string                  `json:"sessionKey"`
	RunID       string                  `json:"runId,omitempty"`
	Message     string                  `json:"message"`
	Channel     string                  `json:"channel,omitempty"`
	To          string                  `json:"to,omitempty"`
	TimeoutMs   int                     `json:"timeoutMs,omitempty"`
	Stop        bool                    `json:"stop,omitempty"`
	Attachments []ChatAttachmentRequest `json:"attachments,omitempty"`
}

// ChatCancelRequest represents a chat cancellation request.
type ChatCancelRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	RunID      string `json:"runId,omitempty"`
}

// ChatBootstrapResponse represents the response for chat bootstrap.
type ChatBootstrapResponse struct {
	SessionKey string                   `json:"sessionKey"`
	History    core.ChatHistoryResponse `json:"history"`
}

// ChatAttachmentRequest represents a file attachment in a chat message.
type ChatAttachmentRequest struct {
	Type     string `json:"type,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	FileName string `json:"fileName,omitempty"`
	Content  string `json:"content,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Task types
// ─────────────────────────────────────────────────────────────────────────────

// TasksResponse represents the response for task listing.
type TasksResponse struct {
	Tasks []core.TaskRecord `json:"tasks"`
}

// TaskCreateRequest represents a task creation request.
type TaskCreateRequest struct {
	AgentID                string    `json:"agentId,omitempty"`
	SessionKey             string    `json:"sessionKey,omitempty"`
	Title                  string    `json:"title"`
	Message                string    `json:"message"`
	Channel                string    `json:"channel,omitempty"`
	To                     string    `json:"to,omitempty"`
	AccountID              string    `json:"accountId,omitempty"`
	ThreadID               string    `json:"threadId,omitempty"`
	Deliver                bool      `json:"deliver,omitempty"`
	DeliveryMode           string    `json:"deliveryMode,omitempty"`
	DeliveryBestEffort     bool      `json:"deliveryBestEffort,omitempty"`
	PayloadKind            string    `json:"payloadKind,omitempty"`
	SessionTarget          string    `json:"sessionTarget,omitempty"`
	WakeMode               string    `json:"wakeMode,omitempty"`
	FailureAlertAfter      int       `json:"failureAlertAfter,omitempty"`
	FailureAlertCooldownMs int64     `json:"failureAlertCooldownMs,omitempty"`
	FailureAlertChannel    string    `json:"failureAlertChannel,omitempty"`
	FailureAlertTo         string    `json:"failureAlertTo,omitempty"`
	FailureAlertAccountID  string    `json:"failureAlertAccountId,omitempty"`
	FailureAlertMode       string    `json:"failureAlertMode,omitempty"`
	ScheduleKind           string    `json:"scheduleKind,omitempty"`
	ScheduleAt             time.Time `json:"scheduleAt,omitempty"`
	ScheduleEveryMs        int64     `json:"scheduleEveryMs,omitempty"`
	ScheduleAnchorMs       int64     `json:"scheduleAnchorMs,omitempty"`
	ScheduleExpr           string    `json:"scheduleExpr,omitempty"`
	ScheduleTZ             string    `json:"scheduleTz,omitempty"`
	ScheduleStaggerMs      int64     `json:"scheduleStaggerMs,omitempty"`
	IntervalSeconds        int       `json:"intervalSeconds,omitempty"`
	RunAt                  time.Time `json:"runAt,omitempty"`
}

// TaskActionRequest represents a task action request (cancel/delete).
type TaskActionRequest struct {
	ID string `json:"id"`
}

// TaskUpdateRequest represents a task update request.
type TaskUpdateRequest struct {
	ID                     string    `json:"id"`
	AgentID                string    `json:"agentId,omitempty"`
	SessionKey             string    `json:"sessionKey,omitempty"`
	Title                  string    `json:"title"`
	Message                string    `json:"message"`
	Channel                string    `json:"channel,omitempty"`
	To                     string    `json:"to,omitempty"`
	AccountID              string    `json:"accountId,omitempty"`
	ThreadID               string    `json:"threadId,omitempty"`
	Deliver                bool      `json:"deliver,omitempty"`
	DeliveryMode           string    `json:"deliveryMode,omitempty"`
	DeliveryBestEffort     bool      `json:"deliveryBestEffort,omitempty"`
	PayloadKind            string    `json:"payloadKind,omitempty"`
	SessionTarget          string    `json:"sessionTarget,omitempty"`
	WakeMode               string    `json:"wakeMode,omitempty"`
	FailureAlertAfter      int       `json:"failureAlertAfter,omitempty"`
	FailureAlertCooldownMs int64     `json:"failureAlertCooldownMs,omitempty"`
	FailureAlertChannel    string    `json:"failureAlertChannel,omitempty"`
	FailureAlertTo         string    `json:"failureAlertTo,omitempty"`
	FailureAlertAccountID  string    `json:"failureAlertAccountId,omitempty"`
	FailureAlertMode       string    `json:"failureAlertMode,omitempty"`
	ScheduleKind           string    `json:"scheduleKind,omitempty"`
	ScheduleAt             time.Time `json:"scheduleAt,omitempty"`
	ScheduleEveryMs        int64     `json:"scheduleEveryMs,omitempty"`
	ScheduleAnchorMs       int64     `json:"scheduleAnchorMs,omitempty"`
	ScheduleExpr           string    `json:"scheduleExpr,omitempty"`
	ScheduleTZ             string    `json:"scheduleTz,omitempty"`
	ScheduleStaggerMs      int64     `json:"scheduleStaggerMs,omitempty"`
	IntervalSeconds        int       `json:"intervalSeconds,omitempty"`
	RunAt                  time.Time `json:"runAt,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Brain types
// ─────────────────────────────────────────────────────────────────────────────

// BrainState represents the brain configuration state.
type BrainState struct {
	DefaultAgent string                       `json:"defaultAgent"`
	Agents       config.AgentsConfig          `json:"agents"`
	Models       config.ModelsConfig          `json:"models"`
	Providers    []core.ProviderHealthSummary `json:"providers,omitempty"`
	SystemPrompt string                       `json:"systemPrompt,omitempty"`
	ModelRecords []BrainModelRecord           `json:"modelRecords,omitempty"`
	ModelPresets []BrainModelPreset           `json:"modelPresets,omitempty"`
	BrainMode    string                       `json:"brainMode"`
	BrainLocal   *LocalModelState             `json:"brainLocal,omitempty"`
	Cerebellum   *CerebellumState             `json:"cerebellum,omitempty"`
}

// LocalModelState represents the state of a local model manager instance.
// Used by both brain-local and cerebellum.
type LocalModelState struct {
	Enabled          bool                        `json:"enabled"`
	Status           string                      `json:"status"`
	ModelID          string                      `json:"modelId,omitempty"`
	ModelsDir        string                      `json:"modelsDir,omitempty"`
	Models           []CerebellumModelInfo       `json:"models"`
	Catalog          []CerebellumModelPreset     `json:"catalog,omitempty"`
	LastError        string                      `json:"lastError,omitempty"`
	DownloadProgress *CerebellumDownloadProgress `json:"downloadProgress,omitempty"`
	AutoStart        bool                        `json:"autoStart,omitempty"`
	Sampling         *SamplingParams             `json:"sampling,omitempty"`
	Threads          int                         `json:"threads"`
	ContextSize      int                         `json:"contextSize"`
	GpuLayers        int                         `json:"gpuLayers"`
}

// CerebellumState represents the state of the local cerebellum (小脑).
type CerebellumState struct {
	Enabled          bool                        `json:"enabled"`
	Status           string                      `json:"status"`
	ModelID          string                      `json:"modelId,omitempty"`
	ModelsDir        string                      `json:"modelsDir,omitempty"`
	Models           []CerebellumModelInfo       `json:"models"`
	Catalog          []CerebellumModelPreset     `json:"catalog,omitempty"`
	LastError        string                      `json:"lastError,omitempty"`
	DownloadProgress *CerebellumDownloadProgress `json:"downloadProgress,omitempty"`
	AutoStart        bool                        `json:"autoStart,omitempty"`
	Sampling         *SamplingParams             `json:"sampling,omitempty"`
	Threads          int                         `json:"threads"`
	ContextSize      int                         `json:"contextSize"`
	GpuLayers        int                         `json:"gpuLayers"`
}

// SamplingParams describes the sampling parameters for local model inference.
type SamplingParams struct {
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

// LocalModelParamsUpdateRequest represents a request to update local model parameters.
type LocalModelParamsUpdateRequest struct {
	Sampling    *SamplingParams `json:"sampling,omitempty"`
	Threads     *int            `json:"threads,omitempty"`
	ContextSize *int            `json:"contextSize,omitempty"`
	GpuLayers   *int            `json:"gpuLayers,omitempty"`
}

// CerebellumDownloadProgress tracks an ongoing model download.
type CerebellumDownloadProgress struct {
	PresetID        string `json:"presetId"`
	Filename        string `json:"filename"`
	TotalBytes      int64  `json:"totalBytes"`
	DownloadedBytes int64  `json:"downloadedBytes"`
	Active          bool   `json:"active"`
	Canceled        bool   `json:"canceled,omitempty"`
	Error           string `json:"error,omitempty"`
}

// CerebellumModelInfo describes an available local model.
type CerebellumModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size string `json:"size,omitempty"`
}

// CerebellumModelSelectRequest represents a cerebellum model selection request.
type CerebellumModelSelectRequest struct {
	ModelID string `json:"modelId"`
}

// CerebellumDownloadRequest represents a request to download a model from the catalog.
type CerebellumDownloadRequest struct {
	PresetID string `json:"presetId"`
}

// ModelPresetDefaults describes default runtime/sampling parameters for a model preset.
type ModelPresetDefaults struct {
	Threads     int             `json:"threads,omitempty"`
	ContextSize int             `json:"contextSize,omitempty"`
	GpuLayers   int             `json:"gpuLayers,omitempty"`
	Sampling    *SamplingParams `json:"sampling,omitempty"`
}

// LocalizedText stores Chinese and English UI text.
type LocalizedText struct {
	Zh string `json:"zh,omitempty"`
	En string `json:"en,omitempty"`
}

// CerebellumModelPreset describes a recommended model in the catalog.
type CerebellumModelPreset struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description *LocalizedText       `json:"description,omitempty"`
	Size        string               `json:"size,omitempty"`
	DownloadURL string               `json:"downloadUrl,omitempty"`
	Filename    string               `json:"filename,omitempty"`
	Defaults    *ModelPresetDefaults `json:"defaults,omitempty"`
}

// CerebellumHelpRequest represents a cerebellum help query.
type CerebellumHelpRequest struct {
	Query   string `json:"query"`
	Context string `json:"context,omitempty"`
}

// CerebellumHelpResponse represents the cerebellum's help response.
type CerebellumHelpResponse struct {
	Answer     string         `json:"answer"`
	Suggestion map[string]any `json:"suggestion,omitempty"`
}

// BrainSaveRequest represents a brain save request.
type BrainSaveRequest struct {
	Agents       *config.AgentsConfig `json:"agents,omitempty"`
	Models       *config.ModelsConfig `json:"models,omitempty"`
	SystemPrompt *string              `json:"systemPrompt,omitempty"`
}

// BrainModelRecord represents a brain model record.
type BrainModelRecord struct {
	Key           string `json:"key"`
	ProviderID    string `json:"providerId"`
	ModelID       string `json:"modelId"`
	DisplayName   string `json:"displayName,omitempty"`
	BaseURL       string `json:"baseUrl,omitempty"`
	API           string `json:"api,omitempty"`
	APIKey        string `json:"apiKey,omitempty"`
	Reasoning     bool   `json:"reasoning,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"`
	MaxTokens     int    `json:"maxTokens,omitempty"`
	IsDefault     bool   `json:"isDefault,omitempty"`
	IsFallback    bool   `json:"isFallback,omitempty"`
	Ready         bool   `json:"ready,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

// BrainModelUpsertRequest represents a brain model upsert request.
type BrainModelUpsertRequest struct {
	ExistingProviderID string `json:"existingProviderId,omitempty"`
	ExistingModelID    string `json:"existingModelId,omitempty"`
	PresetID           string `json:"presetId,omitempty"`
	ProviderID         string `json:"providerId,omitempty"`
	ModelID            string `json:"modelId,omitempty"`
	DisplayName        string `json:"displayName,omitempty"`
	BaseURL            string `json:"baseUrl,omitempty"`
	API                string `json:"api,omitempty"`
	APIKey             string `json:"apiKey,omitempty"`
	Reasoning          *bool  `json:"reasoning,omitempty"`
	ContextWindow      int    `json:"contextWindow,omitempty"`
	MaxTokens          int    `json:"maxTokens,omitempty"`
}

// BrainModelDeleteRequest represents a brain model delete request.
type BrainModelDeleteRequest struct {
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
}

// BrainModelAssignRequest represents a brain model assign request.
type BrainModelAssignRequest struct {
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

// BrainModeSwitchRequest represents a request to switch brain mode.
type BrainModeSwitchRequest struct {
	Mode string `json:"mode"` // "cloud" or "local"
}

// BrainLocalModelSelectRequest represents a request to select a brain local model.
type BrainLocalModelSelectRequest struct {
	ModelID string `json:"modelId"`
}

// BrainLocalDownloadRequest represents a request to download a brain local model.
type BrainLocalDownloadRequest struct {
	PresetID string `json:"presetId"`
}

// LocalModelDeleteRequest represents a request to delete a downloaded local model.
type LocalModelDeleteRequest struct {
	ModelID string `json:"modelId"`
}

// BrainPresetModel represents the model details within a preset.
type BrainPresetModel struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	Reasoning     bool   `json:"reasoning,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"`
	MaxTokens     int    `json:"maxTokens,omitempty"`
}

// BrainModelPreset represents a provider preset with multiple model options.
type BrainModelPreset struct {
	ID          string                  `json:"id"`
	Label       string                  `json:"label"`
	LabelZh     string                  `json:"labelZh,omitempty"`
	Free        bool                    `json:"free,omitempty"`
	BaseURL     string                  `json:"baseUrl"`
	API         string                  `json:"api"`
	Models      []BrainPresetModel      `json:"models"`
	AuthKind    string                  `json:"authKind,omitempty"`
	OAuthConfig *BrainPresetOAuthConfig `json:"oauthConfig,omitempty"`
}

// BrainPresetOAuthConfig holds OAuth configuration exposed to the frontend.
type BrainPresetOAuthConfig struct {
	DeviceCodeURL string `json:"deviceCodeUrl"`
	TokenURL      string `json:"tokenUrl"`
	ClientID      string `json:"clientId"`
	Scope         string `json:"scope"`
}

// ─────────────────────────────────────────────────────────────────────────────
// OAuth types
// ─────────────────────────────────────────────────────────────────────────────

// OAuthDeviceCodeStartRequest initiates a device-code OAuth flow.
type OAuthDeviceCodeStartRequest struct {
	PresetID string `json:"presetId"`
}

// OAuthDeviceCodeStartResponse is returned after starting a device-code flow.
type OAuthDeviceCodeStartResponse struct {
	SessionID       string `json:"sessionId"`
	UserCode        string `json:"userCode"`
	VerificationURL string `json:"verificationUrl"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}

// OAuthDeviceCodePollRequest polls for the device-code token.
type OAuthDeviceCodePollRequest struct {
	SessionID string `json:"sessionId"`
}

// OAuthDeviceCodePollResponse returns the poll result.
type OAuthDeviceCodePollResponse struct {
	Status      string `json:"status"` // "pending", "success", "error", "expired"
	AccessToken string `json:"accessToken,omitempty"`
	BaseURL     string `json:"baseUrl,omitempty"`
	Error       string `json:"error,omitempty"`
}

// OAuthCredential represents a stored OAuth credential for a provider.
type OAuthCredential struct {
	ProviderID   string `json:"providerId"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt"`
}

// OAuthStatusResponse returns the OAuth authentication status for presets.
type OAuthStatusResponse struct {
	Authenticated map[string]bool `json:"authenticated"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Capabilities types
// ─────────────────────────────────────────────────────────────────────────────

// CapabilityTool represents a tool capability.
type CapabilityTool struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	PluginID    string                    `json:"pluginId,omitempty"`
	Optional    bool                      `json:"optional,omitempty"`
	Elevated    bool                      `json:"elevated,omitempty"`
	OwnerOnly   bool                      `json:"ownerOnly,omitempty"`
	Allowed     bool                      `json:"allowed"`
	Meta        core.ToolRegistrationMeta `json:"meta,omitempty"`
}

// CapabilityPlugin represents a plugin capability.
type CapabilityPlugin struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// CapabilitiesState represents the capabilities state.
type CapabilitiesState struct {
	Skills            core.SkillStatusReport `json:"skills"`
	Tools             []CapabilityTool       `json:"tools"`
	Plugins           []CapabilityPlugin     `json:"plugins"`
	Config            config.SkillsConfig    `json:"skillsConfig"`
	HeartbeatsEnabled bool                   `json:"heartbeatsEnabled"`
}

// CapabilitiesSaveRequest represents a capabilities save request.
type CapabilitiesSaveRequest struct {
	Skills            *config.SkillsConfig  `json:"skills,omitempty"`
	Plugins           *config.PluginsConfig `json:"plugins,omitempty"`
	ToolToggles       map[string]bool       `json:"toolToggles,omitempty"`
	HeartbeatsEnabled *bool                 `json:"heartbeatsEnabled,omitempty"`
}

// SkillInstallRequest represents a skill install request.
type SkillInstallRequest struct {
	SkillName string `json:"skillName"`
	InstallID string `json:"installId,omitempty"`
	TimeoutMs int64  `json:"timeoutMs,omitempty"`
}

// ACPSessionsState represents ACP session snapshots.
type ACPSessionsState struct {
	Sessions []acp.AcpSessionStatus `json:"sessions"`
}

// ACPResumeResults represents ACP resume results.
type ACPResumeResults struct {
	Results []acp.AcpSessionResumeResult `json:"results"`
}

// ACPSessionResumeRequest represents an ACP resume request.
type ACPSessionResumeRequest struct {
	SessionKey string `json:"sessionKey"`
	BackendID  string `json:"backendId,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ACPSessionControlRequest represents an ACP control request.
type ACPSessionControlRequest struct {
	SessionKey string `json:"sessionKey"`
	BackendID  string `json:"backendId,omitempty"`
	Action     string `json:"action"`
	Key        string `json:"key,omitempty"`
	Value      string `json:"value,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Data types
// ─────────────────────────────────────────────────────────────────────────────

// DataState represents the data state.
type DataState struct {
	DefaultAgent string             `json:"defaultAgent"`
	Workspace    string             `json:"workspace"`
	SystemPrompt string             `json:"systemPrompt,omitempty"`
	Files        []ContextFileState `json:"files"`
}

// DataSaveRequest represents a data save request.
type DataSaveRequest struct {
	SystemPrompt *string            `json:"systemPrompt,omitempty"`
	Files        []ContextFilePatch `json:"files,omitempty"`
}

// ContextFileState represents a context file state.
type ContextFileState struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
}

// ContextFilePatch represents a context file patch.
type ContextFilePatch struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Sandbox types
// ─────────────────────────────────────────────────────────────────────────────

// SandboxState represents the sandbox state.
type SandboxState struct {
	DefaultAgent string                 `json:"defaultAgent"`
	Agents       []AgentWorkdirSnapshot `json:"agents"`
}

// AgentWorkdirSnapshot represents an agent directory snapshot.
type AgentWorkdirSnapshot struct {
	AgentID        string   `json:"agentId"`
	WorkspaceDir   string   `json:"workspaceDir,omitempty"` // default tool working directory (always accessible)
	AgentDir       string   `json:"agentDir,omitempty"`
	SandboxEnabled *bool    `json:"sandboxEnabled,omitempty"` // sandbox restriction toggle (default: false)
	SandboxDirs    []string `json:"sandboxDirs,omitempty"`    // sandbox directories (configurable from frontend)
}

// SandboxSaveRequest represents a sandbox save request.
type SandboxSaveRequest struct {
	Agents []AgentSandboxPatch `json:"agents,omitempty"`
}

// AgentSandboxPatch represents an agent sandbox directory patch.
type AgentSandboxPatch struct {
	AgentID        string   `json:"agentId"`
	SandboxEnabled *bool    `json:"sandboxEnabled,omitempty"`
	SandboxDirs    []string `json:"sandboxDirs"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Channels types
// ─────────────────────────────────────────────────────────────────────────────

// ChannelsState represents the channels state.
type ChannelsState struct {
	Config       config.ChannelsConfig               `json:"config"`
	Integrations []adapter.ChannelIntegrationSummary `json:"integrations"`
	Schemas      []adapter.ChannelDriverSchema       `json:"schemas"`
}

// ChannelsSaveRequest represents a channels save request.
type ChannelsSaveRequest struct {
	Channels config.ChannelsConfig `json:"channels"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Environment types
// ─────────────────────────────────────────────────────────────────────────────

// EnvironmentState represents the environment state.
type EnvironmentState struct {
	Environment config.EnvironmentConfig `json:"environment"`
	Resolved    map[string]string        `json:"resolved,omitempty"`
	Masked      map[string]string        `json:"masked,omitempty"`
}

// EnvironmentSaveRequest represents an environment save request.
type EnvironmentSaveRequest struct {
	Environment config.EnvironmentConfig `json:"environment"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Network / Proxy types
// ─────────────────────────────────────────────────────────────────────────────

// NetworkState represents the current network proxy configuration.
type NetworkState struct {
	UseSystemProxy bool   `json:"useSystemProxy"`
	ProxyURL       string `json:"proxyUrl"`
	Language       string `json:"language"`
}

// NetworkSaveRequest represents a request to update the proxy configuration.
type NetworkSaveRequest struct {
	UseSystemProxy bool   `json:"useSystemProxy"`
	ProxyURL       string `json:"proxyUrl"`
	Language       string `json:"language"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Setup / Onboarding types
// ─────────────────────────────────────────────────────────────────────────────

// SetupStatusResponse indicates whether onboarding is needed.
type SetupStatusResponse struct {
	NeedsSetup bool `json:"needsSetup"`
	HasModels  bool `json:"hasModels"`
}

// SetupCompleteRequest marks onboarding as done.
type SetupCompleteRequest struct {
	Completed bool `json:"completed"`
}
