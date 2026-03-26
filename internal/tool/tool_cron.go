package tool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/task"

	"github.com/kocort/kocort/utils"
)

const (
	reminderContextMessagesMax = 10
	reminderContextMarker      = "\n\nRecent context:\n"
)

type CronTool struct{}

func NewCronTool() *CronTool {
	return &CronTool{}
}

func (t *CronTool) Name() string {
	return "cron"
}

func (t *CronTool) Description() string {
	return "Manage cron jobs and wake events (use for reminders; when scheduling a reminder, write the systemEvent text as something that will read like a reminder when it fires, and mention that it is a reminder depending on the time gap between setting and firing; include recent context in reminder text if appropriate)."
}

func (t *CronTool) ToolRegistrationMeta() core.ToolRegistrationMeta {
	return core.ToolRegistrationMeta{OwnerOnly: true}
}

func (t *CronTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":          map[string]any{"type": "string", "enum": []string{"status", "list", "add", "remove", "run"}},
				"includeDisabled": map[string]any{"type": "boolean"},
				"job":             map[string]any{"type": "object", "additionalProperties": true},
				"jobId":           map[string]any{"type": "string"},
				"id":              map[string]any{"type": "string"},
				"text":            map[string]any{"type": "string"},
				"message":         map[string]any{"type": "string"},
				"contextMessages": map[string]any{"type": "number"},
				"delaySeconds":    map[string]any{"type": "number"},
				"delayMinutes":    map[string]any{"type": "number"},
				"everyMinutes":    map[string]any{"type": "number"},
				"everySeconds":    map[string]any{"type": "number"},
				"cronExpr":        map[string]any{"type": "string"},
				"timezone":        map[string]any{"type": "string"},
				"wakeMode":        map[string]any{"type": "string", "enum": []string{"now", "next-heartbeat"}},
				"failureAlert":    map[string]any{"type": "object", "additionalProperties": true},
				"schedule":        map[string]any{"type": "object", "additionalProperties": true},
				"payload":         map[string]any{"type": "object", "additionalProperties": true},
				"sessionTarget":   map[string]any{"type": "string"},
			},
			"required":             []string{"action"},
			"additionalProperties": true,
		},
	}
}

func (t *CronTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	action, err := ReadStringParam(args, "action", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "status":
		return t.status(toolCtx)
	case "list":
		return t.list(toolCtx)
	case "add":
		return t.add(ctx, toolCtx, args)
	case "remove":
		return t.remove(ctx, toolCtx, args)
	case "run":
		return t.run(ctx, toolCtx, args)
	default:
		return core.ToolResult{}, ToolInputError{Message: fmt.Sprintf("unsupported cron action %q", action)}
	}
}

func (t *CronTool) status(toolCtx ToolContext) (core.ToolResult, error) {
	if toolCtx.Runtime == nil || toolCtx.Runtime.GetTasks() == nil {
		return core.ToolResult{}, core.ErrTaskSchedulerNotConfigured
	}
	summary := toolCtx.Runtime.GetTasks().Summary()
	return JSONResult(map[string]any{
		"enabled":       summary.Enabled,
		"total":         summary.Total,
		"byStatus":      summary.ByStatus,
		"maxConcurrent": summary.MaxConcurrent,
	})
}

func (t *CronTool) list(toolCtx ToolContext) (core.ToolResult, error) {
	if toolCtx.Runtime == nil {
		return core.ToolResult{}, fmt.Errorf("runtime is not configured")
	}
	tasks := toolCtx.Runtime.ListTasks(context.Background())
	filtered := make([]core.TaskRecord, 0, len(tasks))
	for _, task := range tasks {
		if task.Kind != core.TaskKindScheduled {
			continue
		}
		if !cronTaskVisibleToRun(toolCtx.Run, task) {
			continue
		}
		filtered = append(filtered, task)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
	})
	return JSONResult(map[string]any{"tasks": filtered})
}

func (t *CronTool) add(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	if toolCtx.Runtime == nil {
		return core.ToolResult{}, fmt.Errorf("runtime is not configured")
	}
	job, err := normalizeCronAddArgs(args)
	if err != nil {
		return core.ToolResult{}, err
	}
	if strings.TrimSpace(job.Text) != "" && job.ContextMessages > 0 {
		job.Text = appendReminderContext(toolCtx, job.Text, job.ContextMessages)
	}
	if strings.EqualFold(strings.TrimSpace(job.SessionTarget), string(core.TaskSessionTargetMain)) &&
		!strings.EqualFold(strings.TrimSpace(job.PayloadKind), string(core.TaskPayloadKindSystemEvent)) {
		return core.ToolResult{}, ToolInputError{Message: `sessionTarget="main" requires payload.kind="systemEvent"`}
	}
	if strings.EqualFold(strings.TrimSpace(job.SessionTarget), string(core.TaskSessionTargetIsolated)) &&
		!strings.EqualFold(strings.TrimSpace(job.PayloadKind), string(core.TaskPayloadKindAgentTurn)) {
		return core.ToolResult{}, ToolInputError{Message: `sessionTarget="isolated" requires payload.kind="agentTurn"`}
	}
	if strings.EqualFold(strings.TrimSpace(job.DeliveryMode), "webhook") {
		return core.ToolResult{}, ToolInputError{Message: `delivery.mode="webhook" is not supported by this runtime yet`}
	}
	if strings.EqualFold(strings.TrimSpace(job.FailureAlertMode), "webhook") {
		return core.ToolResult{}, ToolInputError{Message: `failureAlert.mode="webhook" is not supported by this runtime yet`}
	}
	schedule, err := cronScheduleFromJob(job, time.Now().UTC())
	if err != nil {
		return core.ToolResult{}, err
	}
	message := cronTaskMessageFromJob(job)
	if strings.TrimSpace(message) == "" {
		return core.ToolResult{}, ToolInputError{Message: "cron add requires payload.text or payload.message"}
	}
	target, deliver := normalizeCronDelivery(job.Delivery, toolCtx.Run)
	switch strings.TrimSpace(strings.ToLower(job.DeliveryMode)) {
	case "none":
		deliver = false
	case "announce":
		deliver = true
	}
	req := task.TaskScheduleRequest{
		Kind:                   core.TaskKindScheduled,
		AgentID:                utils.NonEmpty(job.AgentID, toolCtx.Run.Identity.ID),
		SessionKey:             utils.NonEmpty(job.SessionKey, toolCtx.Run.Session.SessionKey),
		Title:                  utils.NonEmpty(job.Name, "Reminder"),
		Message:                message,
		Channel:                target.Channel,
		To:                     target.To,
		AccountID:              target.AccountID,
		ThreadID:               target.ThreadID,
		Deliver:                deliver,
		PayloadKind:            core.TaskPayloadKind(strings.TrimSpace(job.PayloadKind)),
		SessionTarget:          core.TaskSessionTarget(strings.TrimSpace(job.SessionTarget)),
		WakeMode:               cronWakeModeFromJob(job),
		WorkspaceDir:           utils.NonEmpty(job.WorkspaceDir, toolCtx.Run.WorkspaceDir),
		ScheduleKind:           schedule.Kind,
		ScheduleAt:             schedule.At,
		ScheduleEveryMs:        schedule.EveryMs,
		ScheduleAnchorMs:       schedule.AnchorMs,
		ScheduleExpr:           schedule.Expr,
		ScheduleTZ:             schedule.TZ,
		ScheduleStaggerMs:      schedule.StaggerMs,
		IntervalSeconds:        schedule.IntervalSeconds,
		RunAt:                  schedule.At,
		DeliveryMode:           strings.TrimSpace(job.DeliveryMode),
		DeliveryBestEffort:     job.DeliveryBestEffort,
		FailureAlertAfter:      job.FailureAlertAfter,
		FailureAlertCooldownMs: job.FailureAlertCooldownMs,
		FailureAlertChannel:    job.FailureAlertChannel,
		FailureAlertTo:         job.FailureAlertTo,
		FailureAlertAccountID:  job.FailureAlertAccountID,
		FailureAlertMode:       job.FailureAlertMode,
	}
	task, err := toolCtx.Runtime.ScheduleTask(ctx, req)
	if err != nil {
		return core.ToolResult{}, err
	}
	return JSONResult(map[string]any{
		"status": "scheduled",
		"task":   task,
	})
}

func (t *CronTool) remove(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	taskID := cronTaskIDArg(args)
	if taskID == "" {
		return core.ToolResult{}, ToolInputError{Message: "jobId required (id accepted for compatibility)"}
	}
	record, err := toolCtx.Runtime.CancelTask(ctx, taskID)
	if err != nil {
		return core.ToolResult{}, err
	}
	if record == nil {
		return JSONResult(map[string]any{"status": "missing", "jobId": taskID})
	}
	return JSONResult(map[string]any{"status": "canceled", "task": record})
}

func (t *CronTool) run(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	taskID := cronTaskIDArg(args)
	if taskID == "" {
		return core.ToolResult{}, ToolInputError{Message: "jobId required (id accepted for compatibility)"}
	}
	task := toolCtx.Runtime.GetTask(ctx, taskID)
	if task == nil {
		return JSONResult(map[string]any{"status": "missing", "jobId": taskID})
	}
	if !cronTaskVisibleToRun(toolCtx.Run, *task) {
		return core.ToolResult{}, fmt.Errorf("task %q is not visible from this session", taskID)
	}
	result, err := toolCtx.Runtime.Run(ctx, core.AgentRunRequest{
		TaskID:            task.ID,
		Message:           task.Message,
		SessionKey:        task.SessionKey,
		AgentID:           utils.NonEmpty(task.AgentID, toolCtx.Run.Identity.ID),
		Channel:           task.Channel,
		To:                task.To,
		AccountID:         task.AccountID,
		ThreadID:          task.ThreadID,
		Deliver:           task.Deliver,
		WorkspaceOverride: task.WorkspaceDir,
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	return JSONResult(map[string]any{
		"status": "ran",
		"jobId":  task.ID,
		"result": map[string]any{
			"runId":      result.RunID,
			"stopReason": result.StopReason,
			"payloads":   result.Payloads,
		},
	})
}

type normalizedCronJob = task.NormalizedCronJob

func normalizeCronAddArgs(args map[string]any) (normalizedCronJob, error) {
	return task.NormalizeCronAddArgs(args)
}

func readCronJobMap(args map[string]any) map[string]any {
	return task.ReadCronJobMap(args)
}

type normalizedTaskSchedule = task.NormalizedTaskSchedule

func cronScheduleFromJob(job normalizedCronJob, now time.Time) (normalizedTaskSchedule, error) {
	return task.CronScheduleFromJob(job, now)
}

func cronTaskMessageFromJob(job normalizedCronJob) string {
	return task.CronTaskMessageFromJob(job)
}

func normalizeCronDelivery(explicit *core.DeliveryContext, runCtx AgentRunContext) (core.DeliveryContext, bool) {
	if explicit != nil && (strings.TrimSpace(explicit.Channel) != "" || strings.TrimSpace(explicit.To) != "" || strings.TrimSpace(explicit.AccountID) != "" || strings.TrimSpace(explicit.ThreadID) != "") {
		return core.DeliveryContext{
			Channel:   strings.TrimSpace(explicit.Channel),
			To:        strings.TrimSpace(explicit.To),
			AccountID: strings.TrimSpace(explicit.AccountID),
			ThreadID:  strings.TrimSpace(explicit.ThreadID),
		}, true
	}
	if inferred := inferCronDeliveryFromSessionKey(runCtx.Session.SessionKey); inferred != nil {
		return *inferred, true
	}
	return core.DeliveryContext{
		Channel:   strings.TrimSpace(runCtx.Request.Channel),
		To:        strings.TrimSpace(runCtx.Request.To),
		AccountID: strings.TrimSpace(runCtx.Request.AccountID),
		ThreadID:  strings.TrimSpace(runCtx.Request.ThreadID),
	}, strings.TrimSpace(runCtx.Request.Channel) != "" || strings.TrimSpace(runCtx.Request.To) != ""
}

func cronWakeModeFromJob(job normalizedCronJob) core.TaskWakeMode {
	return task.CronWakeModeFromJob(job)
}

func inferCronDeliveryFromSessionKey(sessionKey string) *core.DeliveryContext {
	return task.InferCronDeliveryFromSessionKey(sessionKey)
}

func appendReminderContext(toolCtx ToolContext, baseText string, limit int) string {
	if limit <= 0 || toolCtx.Runtime == nil || toolCtx.Runtime.GetSessions() == nil {
		return strings.TrimSpace(baseText)
	}
	history, err := toolCtx.Runtime.GetSessions().LoadTranscript(toolCtx.Run.Session.SessionKey)
	if err != nil || len(history) == 0 {
		return strings.TrimSpace(baseText)
	}
	lines := make([]string, 0, limit)
	for i := len(history) - 1; i >= 0 && len(lines) < limit; i-- {
		msg := history[i]
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		label := "User"
		if role == "assistant" {
			label = "Assistant"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", label, truncateCronText(text, 220)))
	}
	if len(lines) == 0 {
		return strings.TrimSpace(baseText)
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.TrimSpace(baseText) + reminderContextMarker + strings.Join(lines, "\n")
}

func truncateCronText(text string, maxLen int) string {
	return task.TruncateCronText(text, maxLen)
}

func cronTaskVisibleToRun(runCtx AgentRunContext, task core.TaskRecord) bool {
	sessionKey := strings.TrimSpace(runCtx.Session.SessionKey)
	if sessionKey != "" && strings.TrimSpace(task.SessionKey) == sessionKey {
		return true
	}
	return strings.TrimSpace(task.AgentID) == strings.TrimSpace(runCtx.Identity.ID)
}

func cronTaskIDArg(args map[string]any) string {
	return task.CronTaskIDArg(args)
}
