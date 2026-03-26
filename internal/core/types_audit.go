package core

import "time"

// ---------------------------------------------------------------------------
// Value Objects — Audit
// ---------------------------------------------------------------------------

type AuditEvent struct {
	ID         string
	OccurredAt time.Time
	Category   AuditCategory
	Type       string
	Level      string
	AgentID    string
	SessionKey string
	RunID      string
	TaskID     string
	ToolName   string
	Channel    string
	Message    string
	Data       map[string]any
}

type AuditQuery struct {
	Category   AuditCategory
	Type       string
	Level      string
	Text       string
	SessionKey string
	RunID      string
	TaskID     string
	Limit      int
	StartTime  time.Time // filter events after this time
	EndTime    time.Time // filter events before this time
}

type AuditListRequest struct {
	Category   AuditCategory `json:"category,omitempty"`
	Type       string        `json:"type,omitempty"`
	Level      string        `json:"level,omitempty"`
	Text       string        `json:"text,omitempty"`
	SessionKey string        `json:"sessionKey,omitempty"`
	RunID      string        `json:"runId,omitempty"`
	TaskID     string        `json:"taskId,omitempty"`
	Limit      int           `json:"limit,omitempty"`
	StartTime  time.Time     `json:"startTime,omitempty"`
	EndTime    time.Time     `json:"endTime,omitempty"`
}

type AuditListResponse struct {
	Events []AuditEvent `json:"events"`
}

// ---------------------------------------------------------------------------
// Value Objects — Dashboard
// ---------------------------------------------------------------------------

type RuntimeHealthSnapshot struct {
	Healthy             bool            `json:"healthy"`
	GatewayEnabled      bool            `json:"gatewayEnabled"`
	WebchatEnabled      bool            `json:"webchatEnabled"`
	SessionCount        int             `json:"sessionCount"`
	SessionRootCount    int             `json:"sessionRootCount,omitempty"`
	SpawnedSessionCount int             `json:"spawnedSessionCount,omitempty"`
	SubagentCount       int             `json:"subagentCount"`
	ConfiguredAgent     string          `json:"configuredAgent,omitempty"`
	Components          map[string]bool `json:"components,omitempty"`
	StateDir            string          `json:"stateDir,omitempty"`
}

type ActiveRunSummary struct {
	Total          int            `json:"total"`
	BySession      map[string]int `json:"bySession,omitempty"`
	CancelableRuns int            `json:"cancelableRuns,omitempty"`
}

type DeliveryQueueSummary struct {
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

type ProviderHealthSummary struct {
	Provider    string `json:"provider"`
	BackendKind string `json:"backendKind,omitempty"`
	Configured  bool   `json:"configured"`
	Ready       bool   `json:"ready"`
	ModelCount  int    `json:"modelCount,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

type DashboardSnapshot struct {
	OccurredAt       time.Time               `json:"occurredAt"`
	Runtime          RuntimeHealthSnapshot   `json:"runtime"`
	ActiveRuns       ActiveRunSummary        `json:"activeRuns"`
	DeliveryQueue    DeliveryQueueSummary    `json:"deliveryQueue"`
	Tasks            TaskSchedulerSummary    `json:"tasks"`
	Providers        []ProviderHealthSummary `json:"providers,omitempty"`
	BrainMode        string                  `json:"brainMode,omitempty"`
	BrainLocalStatus string                  `json:"brainLocalStatus,omitempty"`
	CerebellumStatus string                  `json:"cerebellumStatus,omitempty"`
}
