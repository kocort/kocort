// Package core defines shared types, enums, value objects, and interfaces
// used across the kocort runtime. This package has zero internal dependencies
// and serves as the foundational type system for the entire project.
package core

// ---------------------------------------------------------------------------
// Enums — Queue / Routing
// ---------------------------------------------------------------------------

type Lane string

const (
	LaneDefault  Lane = "default"
	LaneSubagent Lane = "subagent"
	LaneNested   Lane = "nested"
)

type QueueMode string

const (
	QueueModeQueue        QueueMode = "queue"
	QueueModeFollowup     QueueMode = "followup"
	QueueModeCollect      QueueMode = "collect"
	QueueModeSteer        QueueMode = "steer"
	QueueModeSteerBacklog QueueMode = "steer-backlog"
	QueueModeInterrupt    QueueMode = "interrupt"
)

type QueueDropPolicy string

const (
	QueueDropOld       QueueDropPolicy = "old"
	QueueDropNew       QueueDropPolicy = "new"
	QueueDropSummarize QueueDropPolicy = "summarize"
)

type QueueDedupeMode string

const (
	QueueDedupeMessageID QueueDedupeMode = "message-id"
	QueueDedupePrompt    QueueDedupeMode = "prompt"
	QueueDedupeNone      QueueDedupeMode = "none"
)

type ReplyKind string

const (
	ReplyKindTool  ReplyKind = "tool"
	ReplyKindBlock ReplyKind = "block"
	ReplyKindFinal ReplyKind = "final"
)

type ActiveRunQueueAction string

const (
	ActiveRunRunNow          ActiveRunQueueAction = "run-now"
	ActiveRunEnqueueFollowup ActiveRunQueueAction = "enqueue-followup"
	ActiveRunDrop            ActiveRunQueueAction = "drop"
)

// ---------------------------------------------------------------------------
// Enums — Session
// ---------------------------------------------------------------------------

type SessionToolsVisibility string

const (
	SessionVisibilitySelf  SessionToolsVisibility = "self"
	SessionVisibilityTree  SessionToolsVisibility = "tree"
	SessionVisibilityAgent SessionToolsVisibility = "agent"
	SessionVisibilityAll   SessionToolsVisibility = "all"
)

type ChatType string

const (
	ChatTypeDirect ChatType = "direct"
	ChatTypeGroup  ChatType = "group"
	ChatTypeThread ChatType = "thread"
	ChatTypeTopic  ChatType = "topic"
)

// ---------------------------------------------------------------------------
// Enums — Task
// ---------------------------------------------------------------------------

type TaskKind string

const (
	TaskKindScheduled TaskKind = "scheduled"
	TaskKindSubagent  TaskKind = "subagent"
)

type TaskPayloadKind string

const (
	TaskPayloadKindSystemEvent TaskPayloadKind = "systemEvent"
	TaskPayloadKindAgentTurn   TaskPayloadKind = "agentTurn"
)

type TaskSessionTarget string

const (
	TaskSessionTargetMain     TaskSessionTarget = "main"
	TaskSessionTargetIsolated TaskSessionTarget = "isolated"
)

type TaskWakeMode string

const (
	TaskWakeNow           TaskWakeMode = "now"
	TaskWakeNextHeartbeat TaskWakeMode = "next-heartbeat"
)

type TaskScheduleKind string

const (
	TaskScheduleAt    TaskScheduleKind = "at"
	TaskScheduleEvery TaskScheduleKind = "every"
	TaskScheduleCron  TaskScheduleKind = "cron"
)

type TaskStatus string

const (
	TaskStatusScheduled TaskStatus = "scheduled"
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCanceled  TaskStatus = "canceled"
)

// ---------------------------------------------------------------------------
// Enums — Audit
// ---------------------------------------------------------------------------

type AuditCategory string

const (
	AuditCategoryTool        AuditCategory = "tool"
	AuditCategoryDelivery    AuditCategory = "delivery"
	AuditCategoryTask        AuditCategory = "task"
	AuditCategorySandbox     AuditCategory = "sandbox"
	AuditCategoryEnvironment AuditCategory = "environment"
	AuditCategoryConfig      AuditCategory = "config"
	AuditCategoryModel       AuditCategory = "model"
	AuditCategoryRuntime     AuditCategory = "runtime"
	AuditCategoryChannel     AuditCategory = "channel"
	AuditCategoryCerebellum  AuditCategory = "cerebellum"
)

// ---------------------------------------------------------------------------
// Enums — Command Backend
// ---------------------------------------------------------------------------

type CommandBackendInputMode string

const (
	CommandBackendInputArg   CommandBackendInputMode = "arg"
	CommandBackendInputStdin CommandBackendInputMode = "stdin"
)

type CommandBackendOutputMode string

const (
	CommandBackendOutputText  CommandBackendOutputMode = "text"
	CommandBackendOutputJSON  CommandBackendOutputMode = "json"
	CommandBackendOutputJSONL CommandBackendOutputMode = "jsonl"
)

// ---------------------------------------------------------------------------
// Enums — ACP
// ---------------------------------------------------------------------------

type AcpRuntimePromptMode string

const (
	AcpPromptModePrompt AcpRuntimePromptMode = "prompt"
	AcpPromptModeSteer  AcpRuntimePromptMode = "steer"
)

type AcpRuntimeSessionMode string

const (
	AcpSessionModePersistent AcpRuntimeSessionMode = "persistent"
	AcpSessionModeOneShot    AcpRuntimeSessionMode = "oneshot"
)

type AcpRuntimeControl string

const (
	AcpControlSetMode         AcpRuntimeControl = "session/set_mode"
	AcpControlSetConfigOption AcpRuntimeControl = "session/set_config_option"
	AcpControlStatus          AcpRuntimeControl = "session/status"
)
