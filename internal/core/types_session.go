package core

import "time"

// ---------------------------------------------------------------------------
// Value Objects — Session
// ---------------------------------------------------------------------------

type SessionEntry struct {
	SessionID                  string            `json:"sessionId"`
	Label                      string            `json:"label,omitempty"`
	UpdatedAt                  time.Time         `json:"updatedAt"`
	LastChannel                string            `json:"lastChannel,omitempty"`
	LastTo                     string            `json:"lastTo,omitempty"`
	LastAccountID              string            `json:"lastAccountId,omitempty"`
	LastThreadID               string            `json:"lastThreadId,omitempty"`
	DeliveryContext            *DeliveryContext  `json:"deliveryContext,omitempty"`
	ThinkingLevel              string            `json:"thinkingLevel,omitempty"`
	FastMode                   bool              `json:"fastMode,omitempty"`
	VerboseLevel               string            `json:"verboseLevel,omitempty"`
	ReasoningLevel             string            `json:"reasoningLevel,omitempty"`
	ResponseUsage              string            `json:"responseUsage,omitempty"`
	ElevatedLevel              string            `json:"elevatedLevel,omitempty"`
	ProviderOverride           string            `json:"providerOverride,omitempty"`
	ModelOverride              string            `json:"modelOverride,omitempty"`
	AuthProfileOverride        string            `json:"authProfileOverride,omitempty"`
	SessionFile                string            `json:"sessionFile,omitempty"`
	CLIType                    string            `json:"backendKind,omitempty"`
	CLISessionIDs              map[string]string `json:"cliSessionIds,omitempty"`
	ClaudeCLISessionID         string            `json:"claudeCliSessionId,omitempty"`
	OpenAIPreviousID           string            `json:"openaiPreviousResponseId,omitempty"`
	ACP                        *AcpSessionMeta   `json:"acp,omitempty"`
	SpawnedBy                  string            `json:"spawnedBy,omitempty"`
	SpawnMode                  string            `json:"spawnMode,omitempty"`
	SpawnDepth                 int               `json:"spawnDepth,omitempty"`
	SubagentRole               string            `json:"subagentRole,omitempty"`
	SubagentControlScope       string            `json:"subagentControlScope,omitempty"`
	SkillsSnapshot             *SkillSnapshot    `json:"skillsSnapshot,omitempty"`
	Usage                      map[string]any    `json:"usage,omitempty"`
	ContextTokens              int               `json:"contextTokens,omitempty"`
	ActiveProvider             string            `json:"activeProvider,omitempty"`
	ActiveModel                string            `json:"activeModel,omitempty"`
	CompactionCount            int               `json:"compactionCount,omitempty"`
	LastModelCallAt            time.Time         `json:"lastModelCallAt,omitempty"`
	MemoryFlushAt              time.Time         `json:"memoryFlushAt,omitempty"`
	MemoryFlushCompactionCount int               `json:"memoryFlushCompactionCount,omitempty"`
	ResetReason                string            `json:"resetReason,omitempty"`
	LastActivityReason         string            `json:"lastActivityReason,omitempty"`
	LastHeartbeatText          string            `json:"lastHeartbeatText,omitempty"`
	LastHeartbeatSentAt        time.Time         `json:"lastHeartbeatSentAt,omitempty"`
	LastChatType               ChatType          `json:"lastChatType,omitempty"`
	ForkedFromParent           bool              `json:"forkedFromParent,omitempty"`
}

type SessionResolution struct {
	SessionID        string
	SessionKey       string
	Entry            *SessionEntry
	IsNew            bool
	PersistedThink   string
	PersistedVerbose string
	Fresh            bool
}

// ---------------------------------------------------------------------------
// Value Objects — Delivery
// ---------------------------------------------------------------------------

type DeliveryContext struct {
	Channel   string `json:"channel,omitempty"`
	To        string `json:"to,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	ThreadID  string `json:"threadId,omitempty"`
}

type DeliveryTarget struct {
	SessionKey           string
	Channel              string
	To                   string
	AccountID            string
	ThreadID             string
	RunID                string
	SkipTranscriptMirror bool
}

type DeliveryRecord struct {
	Kind    ReplyKind
	Payload ReplyPayload
	Target  DeliveryTarget
}

type Attachment struct {
	Type     string `json:"type,omitempty"`
	Name     string
	MIMEType string
	Content  []byte
}

type ReplyPayload struct {
	Text         string         `json:"text"`
	MediaURL     string         `json:"mediaUrl,omitempty"`
	MediaURLs    []string       `json:"mediaUrls,omitempty"`
	ChannelData  map[string]any `json:"channelData,omitempty"`
	ReplyToID    string         `json:"replyToId,omitempty"`
	AudioAsVoice bool           `json:"audioAsVoice,omitempty"`
	IsError      bool           `json:"isError,omitempty"`
	IsReasoning  bool           `json:"isReasoning,omitempty"`
}

type MirroredTranscript struct {
	Text      string
	MediaURLs []string
}

// ---------------------------------------------------------------------------
// Value Objects — Transcript
// ---------------------------------------------------------------------------

type TranscriptMessage struct {
	ID               string         `json:"id,omitempty"`
	RunID            string         `json:"runId,omitempty"`
	Type             string         `json:"type,omitempty"`
	Role             string         `json:"role"`
	Text             string         `json:"text,omitempty"`
	Summary          string         `json:"summary,omitempty"`
	Timestamp        time.Time      `json:"timestamp"`
	ToolCallID       string         `json:"toolCallId,omitempty"`
	ToolName         string         `json:"toolName,omitempty"`
	Args             map[string]any `json:"args,omitempty"`
	Partial          bool           `json:"partial,omitempty"`
	Final            bool           `json:"final,omitempty"`
	Event            string         `json:"event,omitempty"`
	FirstKeptEntryID string         `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int            `json:"tokensBefore,omitempty"`
	Instructions     string         `json:"instructions,omitempty"`
	MediaURL         string         `json:"mediaUrl,omitempty"`
	MediaURLs        []string       `json:"mediaUrls,omitempty"`
}

// ---------------------------------------------------------------------------
// Value Objects — Chat API
// ---------------------------------------------------------------------------

type ChatSendRequest struct {
	AgentID                 string         `json:"agentId,omitempty"`
	SessionKey              string         `json:"sessionKey,omitempty"`
	RunID                   string         `json:"runId,omitempty"`
	Message                 string         `json:"message"`
	Channel                 string         `json:"channel,omitempty"`
	To                      string         `json:"to,omitempty"`
	AccountID               string         `json:"accountId,omitempty"`
	ThreadID                string         `json:"threadId,omitempty"`
	ChatType                ChatType       `json:"chatType,omitempty"`
	TimeoutMs               int            `json:"timeoutMs,omitempty"`
	Stop                    bool           `json:"stop,omitempty"`
	Deliver                 *bool          `json:"deliver,omitempty"`
	Attachments             []Attachment   `json:"attachments,omitempty"`
	ThinkingLevel           string         `json:"thinkingLevel,omitempty"`
	VerboseLevel            string         `json:"verboseLevel,omitempty"`
	SessionModelOverride    string         `json:"sessionModelOverride,omitempty"`
	WorkspaceOverride       string         `json:"workspaceOverride,omitempty"`
	ExtraSystemPrompt       string         `json:"extraSystemPrompt,omitempty"`
	SystemInputProvenance   map[string]any `json:"systemInputProvenance,omitempty"`
	SystemProvenanceReceipt string         `json:"systemProvenanceReceipt,omitempty"`
}

type ChatSendResponse struct {
	RunID          string                `json:"runId"`
	SessionKey     string                `json:"sessionKey"`
	SessionID      string                `json:"sessionId"`
	SkillsSnapshot *SkillSnapshotSummary `json:"skillsSnapshot,omitempty"`
	Payloads       []ReplyPayload        `json:"payloads,omitempty"`
	Queued         bool                  `json:"queued,omitempty"`
	QueueDepth     int                   `json:"queueDepth,omitempty"`
	Messages       []TranscriptMessage   `json:"messages,omitempty"`
	Aborted        bool                  `json:"aborted,omitempty"`
	AbortedRunIDs  []string              `json:"abortedRunIds,omitempty"`
	ClearedQueue   int                   `json:"clearedQueue,omitempty"`
}

type ChatHistoryResponse struct {
	SessionKey     string                `json:"sessionKey"`
	SessionID      string                `json:"sessionId,omitempty"`
	SkillsSnapshot *SkillSnapshotSummary `json:"skillsSnapshot,omitempty"`
	Messages       []TranscriptMessage   `json:"messages"`
	Total          int                   `json:"total,omitempty"`
	HasMore        bool                  `json:"hasMore,omitempty"`
	NextBefore     int                   `json:"nextBefore,omitempty"`
}

type ChatCancelRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	RunID      string `json:"runId,omitempty"`
}

type ChatCancelResponse struct {
	SessionKey     string                `json:"sessionKey"`
	SkillsSnapshot *SkillSnapshotSummary `json:"skillsSnapshot,omitempty"`
	Aborted        bool                  `json:"aborted,omitempty"`
	RunIDs         []string              `json:"runIds,omitempty"`
	ClearedQueued  int                   `json:"clearedQueued,omitempty"`
	Payloads       []ReplyPayload        `json:"payloads,omitempty"`
	Messages       []TranscriptMessage   `json:"messages,omitempty"`
}
