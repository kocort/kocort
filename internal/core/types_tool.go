package core

import (
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// Value Objects — Tool
// ---------------------------------------------------------------------------

type ToolCall struct {
	Name string
	Args map[string]any
}

type ToolCallRecord struct {
	Name   string
	Args   map[string]any
	Result ToolResult
}

type ToolPlan struct {
	ToolCall *ToolCall
	Final    []ReplyPayload
}

type ToolPlannerState struct {
	UserMessage string
	ToolCalls   []ToolCallRecord
}

type ToolResult struct {
	Text      string
	JSON      json.RawMessage
	MediaURL  string
	MediaURLs []string
}

type ToolExecutionFailure struct {
	ToolName    string
	Message     string
	VisibleText string
	HistoryText string
	Recoverable bool
}

func (e *ToolExecutionFailure) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return "tool execution failed"
}

type ToolRegistrationMeta struct {
	PluginID         string
	OptionalPlugin   bool
	Elevated         bool
	OwnerOnly        bool
	AllowedProviders []string
	AllowedChannels  []string
	DefaultTimeoutMs int
}

type OpenAIFunctionToolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ---------------------------------------------------------------------------
// Config types referenced by core types (e.g. AgentIdentity.ToolLoopDetection)
// ---------------------------------------------------------------------------

type ToolLoopDetectionDetectorConfig struct {
	GenericRepeat       *bool `json:"genericRepeat,omitempty"`
	KnownPollNoProgress *bool `json:"knownPollNoProgress,omitempty"`
	PingPong            *bool `json:"pingPong,omitempty"`
}

type ToolLoopDetectionConfig struct {
	Enabled                       *bool                           `json:"enabled,omitempty"`
	HistorySize                   int                             `json:"historySize,omitempty"`
	WarningThreshold              int                             `json:"warningThreshold,omitempty"`
	CriticalThreshold             int                             `json:"criticalThreshold,omitempty"`
	GlobalCircuitBreakerThreshold int                             `json:"globalCircuitBreakerThreshold,omitempty"`
	Detectors                     ToolLoopDetectionDetectorConfig `json:"detectors,omitempty"`
}

// ToolInputError represents an invalid parameter supplied to a tool call.
type ToolInputError struct {
	Message string
}

func (e ToolInputError) Error() string {
	return e.Message
}

// ---------------------------------------------------------------------------
// Value Objects — Model Selection
// ---------------------------------------------------------------------------

type ModelSelection struct {
	Provider       string
	Model          string
	ThinkLevel     string
	AllowedKeys    map[string]struct{}
	AllowAny       bool
	Fallbacks      []ModelCandidate
	StoredOverride bool

	// ContextWindow is the selected model's context-window size in tokens.
	// Zero means unknown / use defaults.
	ContextWindow int
	// MaxOutputTokens is the selected model's maximum output tokens.
	// Zero means unknown / use defaults.
	MaxOutputTokens int
}

type ModelCandidate struct {
	Provider string
	Model    string
}

// ---------------------------------------------------------------------------
// Value Objects — Memory / Context
// ---------------------------------------------------------------------------

type MemoryHit struct {
	ID       string
	Source   string
	Path     string
	Snippet  string
	Score    float64
	FromLine int
	ToLine   int
}

type ContextSourceHit struct {
	SourceID string
	Type     string
	Path     string
	Location string
	Snippet  string
	Score    float64
}

type ContextSourceStatus struct {
	ID            string
	Type          string
	Enabled       bool
	Available     bool
	Path          string
	LastIndexedAt time.Time
	ItemCount     int
	LastError     string
}
