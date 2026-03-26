package core

import "time"

// ---------------------------------------------------------------------------
// Value Objects — Task
// ---------------------------------------------------------------------------

type TaskRecord struct {
	ID                     string            `json:"id"`
	Kind                   TaskKind          `json:"kind"`
	Status                 TaskStatus        `json:"status"`
	AgentID                string            `json:"agentId,omitempty"`
	SessionKey             string            `json:"sessionKey,omitempty"`
	RunID                  string            `json:"runId,omitempty"`
	ParentRunID            string            `json:"parentRunId,omitempty"`
	Title                  string            `json:"title,omitempty"`
	Message                string            `json:"message,omitempty"`
	Channel                string            `json:"channel,omitempty"`
	To                     string            `json:"to,omitempty"`
	AccountID              string            `json:"accountId,omitempty"`
	ThreadID               string            `json:"threadId,omitempty"`
	Deliver                bool              `json:"deliver,omitempty"`
	DeliveryMode           string            `json:"deliveryMode,omitempty"`
	DeliveryBestEffort     bool              `json:"deliveryBestEffort,omitempty"`
	PayloadKind            TaskPayloadKind   `json:"payloadKind,omitempty"`
	SessionTarget          TaskSessionTarget `json:"sessionTarget,omitempty"`
	WakeMode               TaskWakeMode      `json:"wakeMode,omitempty"`
	FailureAlertAfter      int               `json:"failureAlertAfter,omitempty"`
	FailureAlertCooldownMs int64             `json:"failureAlertCooldownMs,omitempty"`
	FailureAlertChannel    string            `json:"failureAlertChannel,omitempty"`
	FailureAlertTo         string            `json:"failureAlertTo,omitempty"`
	FailureAlertAccountID  string            `json:"failureAlertAccountId,omitempty"`
	FailureAlertMode       string            `json:"failureAlertMode,omitempty"`
	WorkspaceDir           string            `json:"workspaceDir,omitempty"`
	ScheduleKind           TaskScheduleKind  `json:"scheduleKind,omitempty"`
	ScheduleAt             time.Time         `json:"scheduleAt,omitempty"`
	ScheduleEveryMs        int64             `json:"scheduleEveryMs,omitempty"`
	ScheduleAnchorMs       int64             `json:"scheduleAnchorMs,omitempty"`
	ScheduleExpr           string            `json:"scheduleExpr,omitempty"`
	ScheduleTZ             string            `json:"scheduleTz,omitempty"`
	ScheduleStaggerMs      int64             `json:"scheduleStaggerMs,omitempty"`
	IntervalSeconds        int               `json:"intervalSeconds,omitempty"`
	NextRunAt              time.Time         `json:"nextRunAt,omitempty"`
	LastRunAt              time.Time         `json:"lastRunAt,omitempty"`
	CompletedAt            time.Time         `json:"completedAt,omitempty"`
	CanceledAt             time.Time         `json:"canceledAt,omitempty"`
	ResultText             string            `json:"resultText,omitempty"`
	LastError              string            `json:"lastError,omitempty"`
	ConsecutiveErrors      int               `json:"consecutiveErrors,omitempty"`
	LastFailureAlertAt     time.Time         `json:"lastFailureAlertAt,omitempty"`
	CreatedAt              time.Time         `json:"createdAt"`
	UpdatedAt              time.Time         `json:"updatedAt"`
}

// ---------------------------------------------------------------------------
// Value Objects — Task Scheduler Summary (Dashboard)
// ---------------------------------------------------------------------------

type TaskSchedulerSummary struct {
	Enabled       bool           `json:"enabled"`
	Total         int            `json:"total"`
	ByStatus      map[string]int `json:"byStatus,omitempty"`
	MaxConcurrent int            `json:"maxConcurrent,omitempty"`
}
