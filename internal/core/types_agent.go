package core

import "time"

// ---------------------------------------------------------------------------
// Value Objects — Agent Identity & Configuration
// ---------------------------------------------------------------------------

type AgentIdentity struct {
	ID                                 string
	Name                               string
	PersonaPrompt                      string
	Emoji                              string
	Theme                              string
	Avatar                             string
	WorkspaceDir                       string
	AgentDir                           string
	SandboxEnabled                     bool     // whether sandbox directory restriction is active
	SandboxDirs                        []string // sandbox directories for tool operations boundary
	DefaultProvider                    string
	DefaultModel                       string
	ModelAllowlist                     []string
	ModelFallbacks                     []string
	ThinkingDefault                    string
	UserTimezone                       string
	TimeoutSeconds                     int
	ToolProfile                        string
	ToolAllowlist                      []string
	ToolDenylist                       []string
	ToolLoopDetection                  ToolLoopDetectionConfig
	SkillFilter                        []string
	MemorySources                      []string
	MemoryExtraPaths                   []string
	MemoryEnabled                      bool
	MemoryProvider                     string
	MemoryFallback                     string
	MemoryModel                        string
	MemoryStorePath                    string
	MemoryVectorEnabled                bool
	MemoryVectorExtensionPath          string
	MemoryCacheEnabled                 bool
	MemoryCacheMaxEntries              int
	MemorySyncOnSearch                 bool
	MemorySyncOnSessionStart           bool
	MemorySyncWatch                    bool
	MemorySyncWatchDebounceMs          int
	MemoryChunkTokens                  int
	MemoryChunkOverlap                 int
	MemoryQueryMaxResults              int
	MemoryQueryMinScore                float64
	MemoryHybridEnabled                bool
	MemoryHybridVectorWeight           float64
	MemoryHybridTextWeight             float64
	MemoryHybridCandidateFactor        int
	MemoryCitationsMode                string
	RuntimeType                        string
	RuntimeAgent                       string
	RuntimeBackend                     string
	RuntimeMode                        string
	RuntimeCwd                         string
	ElevatedEnabled                    bool
	ElevatedAllowFrom                  map[string][]string
	SandboxMode                        string
	SandboxWorkspaceAccess             string
	SandboxSessionVisibility           string
	SandboxScope                       string
	SandboxWorkspaceRoot               string
	SubagentAllowAgents                []string
	SubagentModelPrimary               string
	SubagentModelFallbacks             []string
	SubagentThinking                   string
	SubagentMaxSpawnDepth              int
	SubagentMaxChildren                int
	SubagentArchiveAfterMinutes        int
	SubagentTimeoutSeconds             int
	SubagentAttachmentsEnabled         bool
	SubagentAttachmentMaxFiles         int
	SubagentAttachmentMaxFileBytes     int
	SubagentAttachmentMaxTotalBytes    int
	SubagentRetainAttachmentsOnKeep    bool
	HeartbeatEvery                     string
	HeartbeatSession                   string
	HeartbeatPrompt                    string
	HeartbeatTarget                    string
	HeartbeatDirectPolicy              string
	HeartbeatTo                        string
	HeartbeatAccountID                 string
	HeartbeatModel                     string
	HeartbeatAckMaxChars               int
	HeartbeatSuppressToolErr           bool
	HeartbeatLightContext              bool
	HeartbeatIsolatedSession           bool
	HeartbeatIncludeReasoning          bool
	HeartbeatActiveHoursStart          string
	HeartbeatActiveHoursEnd            string
	HeartbeatActiveHoursTimezone       string
	CompactionReserveTokensFloor       int
	MemoryFlushEnabled                 bool
	MemoryFlushSoftThresholdTokens     int
	MemoryFlushSystemPrompt            string
	MemoryFlushPrompt                  string
	ContextPruningMode                 string
	ContextPruningTTL                  time.Duration
	ContextPruningKeepLastAssistants   int
	ContextPruningSoftTrimRatio        float64
	ContextPruningHardClearRatio       float64
	ContextPruningMinPrunableToolChars int
	ContextPruningSoftTrimMaxChars     int
	ContextPruningSoftTrimHeadChars    int
	ContextPruningSoftTrimTailChars    int
	ContextPruningHardClearEnabled     bool
	ContextPruningHardClearPlaceholder string
	ContextPruningAllowTools           []string
	ContextPruningDenyTools            []string
	OwnerLine                          string
	ModelAliases                       map[string]string
}

// ---------------------------------------------------------------------------
// Value Objects — Agent Run
// ---------------------------------------------------------------------------

type AgentRunRequest struct {
	RunID                   string
	TaskID                  string
	Message                 string
	SessionKey              string
	SessionID               string
	AgentID                 string
	Thinking                string
	Verbose                 string
	Timeout                 time.Duration
	Deliver                 bool
	Channel                 string
	To                      string
	AccountID               string
	ThreadID                string
	ChatType                ChatType
	Lane                    Lane
	SpawnedBy               string
	SpawnDepth              int
	MaxSpawnDepth           int
	WorkspaceOverride       string
	ExtraSystemPrompt       string
	InternalEvents          []TranscriptMessage
	Attachments             []Attachment
	IsHeartbeat             bool
	ShouldFollowup          bool
	QueueMode               QueueMode
	QueueDebounce           time.Duration
	QueueCap                int
	QueueDropPolicy         QueueDropPolicy
	QueueDedupeMode         QueueDedupeMode
	SessionProviderOverride string
	SessionModelOverride    string
	UserTimezone            string
	HeartbeatLightContext   bool
	HeartbeatEvery          string
	HeartbeatPrompt         string
	HeartbeatTarget         string
	HeartbeatModel          string
	HeartbeatAckMaxChars    int
	IsMaintenance           bool
	IsSubagentAnnouncement  bool
}

type AgentRunState struct {
	SuccessfulCronAdds int
	LastToolError      *ToolExecutionFailure
	// Yielded is set to true by the sessions_yield tool to signal the pipeline
	// to stop the model call loop and end the current turn.
	Yielded      bool
	YieldMessage string
}

type AgentRunResult struct {
	RunID              string
	Queued             bool
	QueueDepth         int
	Payloads           []ReplyPayload
	Events             []AgentEvent
	Usage              map[string]any
	StartedAt          time.Time
	FinishedAt         time.Time
	StopReason         string
	SuccessfulCronAdds int
	Meta               map[string]any
}

type AgentEvent struct {
	RunID      string
	Seq        int
	Stream     string
	OccurredAt time.Time
	SessionKey string
	Data       map[string]any
}

type AgentToAgentPolicy struct {
	Enabled bool
	Allow   []string
}
