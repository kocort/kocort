// Pure cron/task argument parsing and schedule helpers extracted from
// runtime/tool_cron.go.
//
// None of these functions reference *Runtime, ToolContext, or AgentRunContext.
package task

import (
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	ReminderContextMessagesMax = 10
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// NormalizedCronJob holds the parsed/normalized arguments for a cron add
// operation before they are submitted to the task scheduler.
type NormalizedCronJob struct {
	Name                   string
	AgentID                string
	SessionKey             string
	SessionTarget          string
	PayloadKind            string
	WakeMode               string
	Text                   string
	Message                string
	Schedule               map[string]any
	Delivery               *core.DeliveryContext
	DeliveryMode           string
	DeliveryBestEffort     bool
	FailureAlertAfter      int
	FailureAlertCooldownMs int64
	FailureAlertChannel    string
	FailureAlertTo         string
	FailureAlertAccountID  string
	FailureAlertMode       string
	ContextMessages        int
	WorkspaceDir           string
}

// NormalizedTaskSchedule holds the resolved schedule parameters for a cron
// job, ready for submission to the task scheduler.
type NormalizedTaskSchedule struct {
	Kind            core.TaskScheduleKind
	At              time.Time
	EveryMs         int64
	AnchorMs        int64
	Expr            string
	TZ              string
	StaggerMs       int64
	IntervalSeconds int
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

// NormalizeCronAddArgs parses the raw tool-call arguments for a cron "add"
// action into a NormalizedCronJob.
func NormalizeCronAddArgs(args map[string]any) (NormalizedCronJob, error) {
	jobMap := ReadCronJobMap(args)
	if len(jobMap) == 0 {
		return NormalizedCronJob{}, core.ToolInputError{Message: "job required"}
	}
	job := NormalizedCronJob{
		Name:            strings.TrimSpace(utils.ReadString(jobMap["name"])),
		AgentID:         strings.TrimSpace(utils.ReadString(jobMap["agentId"])),
		SessionKey:      strings.TrimSpace(utils.ReadString(jobMap["sessionKey"])),
		SessionTarget:   strings.TrimSpace(utils.ReadString(jobMap["sessionTarget"])),
		WakeMode:        strings.TrimSpace(utils.ReadString(jobMap["wakeMode"])),
		ContextMessages: utils.ClampInt(utils.ReadNumber(args["contextMessages"]), 0, ReminderContextMessagesMax),
		WorkspaceDir:    strings.TrimSpace(utils.ReadString(jobMap["workspaceDir"])),
	}
	if payload, ok := utils.ReadMap(jobMap["payload"]); ok {
		job.PayloadKind = strings.TrimSpace(utils.ReadString(payload["kind"]))
		job.Text = strings.TrimSpace(utils.ReadString(payload["text"]))
		job.Message = strings.TrimSpace(utils.ReadString(payload["message"]))
		if job.Text == "" && strings.EqualFold(strings.TrimSpace(utils.ReadString(payload["kind"])), "systemevent") {
			job.Text = strings.TrimSpace(utils.ReadString(payload["message"]))
		}
	}
	if job.Message == "" {
		job.Message = strings.TrimSpace(utils.ReadString(jobMap["message"]))
	}
	if job.Text == "" {
		job.Text = strings.TrimSpace(utils.ReadString(jobMap["text"]))
	}
	if schedule, ok := utils.ReadMap(jobMap["schedule"]); ok {
		job.Schedule = schedule
	}
	if delivery, ok := utils.ReadMap(jobMap["delivery"]); ok {
		job.DeliveryMode = strings.TrimSpace(strings.ToLower(utils.ReadString(delivery["mode"])))
		job.DeliveryBestEffort = utils.ReadBool(delivery["bestEffort"])
		job.Delivery = &core.DeliveryContext{
			Channel:   strings.TrimSpace(utils.ReadString(delivery["channel"])),
			To:        strings.TrimSpace(utils.ReadString(delivery["to"])),
			AccountID: strings.TrimSpace(utils.ReadString(delivery["accountId"])),
			ThreadID:  strings.TrimSpace(utils.ReadString(delivery["threadId"])),
		}
	}
	if failureAlertRaw, ok := jobMap["failureAlert"]; ok {
		if failureAlertRaw != nil {
			if alert, ok := utils.ReadMap(failureAlertRaw); ok {
				job.FailureAlertAfter = int(utils.ReadNumber(alert["after"]))
				job.FailureAlertCooldownMs = int64(utils.ReadNumber(alert["cooldownMs"]))
				job.FailureAlertChannel = strings.TrimSpace(utils.ReadString(alert["channel"]))
				job.FailureAlertTo = strings.TrimSpace(utils.ReadString(alert["to"]))
				job.FailureAlertAccountID = strings.TrimSpace(utils.ReadString(alert["accountId"]))
				job.FailureAlertMode = strings.TrimSpace(strings.ToLower(utils.ReadString(alert["mode"])))
			}
		}
	}
	if job.Schedule == nil {
		job.Schedule = map[string]any{}
		if at := strings.TrimSpace(utils.ReadString(args["at"])); at != "" {
			job.Schedule["kind"] = "at"
			job.Schedule["at"] = at
		}
		if utils.ReadNumber(args["delayMinutes"]) > 0 {
			job.Schedule["kind"] = "at"
			job.Schedule["at"] = time.Now().UTC().Add(time.Duration(utils.ReadNumber(args["delayMinutes"]) * float64(time.Minute))).Format(time.RFC3339)
		}
		if utils.ReadNumber(args["delaySeconds"]) > 0 {
			job.Schedule["kind"] = "at"
			job.Schedule["at"] = time.Now().UTC().Add(time.Duration(utils.ReadNumber(args["delaySeconds"]) * float64(time.Second))).Format(time.RFC3339)
		}
		if utils.ReadNumber(args["everyMinutes"]) > 0 {
			job.Schedule["kind"] = "every"
			job.Schedule["everyMs"] = int64(utils.ReadNumber(args["everyMinutes"]) * float64(time.Minute/time.Millisecond))
		}
		if utils.ReadNumber(args["everySeconds"]) > 0 {
			job.Schedule["kind"] = "every"
			job.Schedule["everyMs"] = int64(utils.ReadNumber(args["everySeconds"]) * float64(time.Second/time.Millisecond))
		}
		if expr := strings.TrimSpace(utils.ReadString(args["cronExpr"])); expr != "" {
			job.Schedule["kind"] = "cron"
			job.Schedule["expr"] = expr
			if tz := strings.TrimSpace(utils.ReadString(args["timezone"])); tz != "" {
				job.Schedule["tz"] = tz
			}
		}
	}
	if job.PayloadKind == "" {
		if strings.TrimSpace(job.Text) != "" || strings.TrimSpace(job.Message) != "" {
			job.PayloadKind = string(core.TaskPayloadKindSystemEvent)
		} else {
			job.PayloadKind = string(core.TaskPayloadKindAgentTurn)
		}
	}
	if job.SessionTarget == "" {
		if strings.EqualFold(job.PayloadKind, string(core.TaskPayloadKindSystemEvent)) {
			job.SessionTarget = string(core.TaskSessionTargetMain)
		} else {
			job.SessionTarget = string(core.TaskSessionTargetIsolated)
		}
	}
	return job, nil
}

// ReadCronJobMap extracts the job map from the raw tool call arguments,
// falling back to top-level keys if no "job" key is present.
func ReadCronJobMap(args map[string]any) map[string]any {
	if job, ok := utils.ReadMap(args["job"]); ok && len(job) > 0 {
		return job
	}
	out := map[string]any{}
	for _, key := range []string{
		"name", "schedule", "sessionTarget", "wakeMode", "payload", "delivery", "failureAlert", "agentId", "sessionKey", "message", "text", "workspaceDir",
	} {
		if value, ok := args[key]; ok && value != nil {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Schedule resolution
// ---------------------------------------------------------------------------

// CronScheduleFromJob resolves the schedule parameters from a NormalizedCronJob.
func CronScheduleFromJob(job NormalizedCronJob, now time.Time) (NormalizedTaskSchedule, error) {
	schedule := job.Schedule
	if len(schedule) == 0 {
		return NormalizedTaskSchedule{}, core.ToolInputError{Message: "schedule required"}
	}
	kind := strings.TrimSpace(strings.ToLower(utils.ReadString(schedule["kind"])))
	switch kind {
	case "at":
		if atMs := utils.ReadNumber(schedule["atMs"]); atMs > 0 {
			return NormalizedTaskSchedule{Kind: core.TaskScheduleAt, At: time.UnixMilli(int64(atMs)).UTC()}, nil
		}
		at := strings.TrimSpace(utils.ReadString(schedule["at"]))
		if at == "" {
			return NormalizedTaskSchedule{}, core.ToolInputError{Message: `schedule.at required for kind="at"`}
		}
		parsed, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return NormalizedTaskSchedule{}, core.ToolInputError{Message: fmt.Sprintf("invalid schedule.at: %v", err)}
		}
		return NormalizedTaskSchedule{Kind: core.TaskScheduleAt, At: parsed.UTC()}, nil
	case "every":
		everyMs := utils.ReadNumber(schedule["everyMs"])
		if everyMs <= 0 {
			return NormalizedTaskSchedule{}, core.ToolInputError{Message: `schedule.everyMs required for kind="every"`}
		}
		everyMillis := int64(everyMs)
		anchorMs := int64(utils.ReadNumber(schedule["anchorMs"]))
		if anchorMs <= 0 {
			anchorMs = now.UTC().UnixMilli()
		}
		seconds := int(everyMillis / int64(time.Second/time.Millisecond))
		if seconds <= 0 {
			seconds = 1
		}
		return NormalizedTaskSchedule{
			Kind:            core.TaskScheduleEvery,
			EveryMs:         everyMillis,
			AnchorMs:        anchorMs,
			IntervalSeconds: seconds,
		}, nil
	case "cron":
		expr := strings.TrimSpace(utils.ReadString(schedule["expr"]))
		if expr == "" {
			return NormalizedTaskSchedule{}, core.ToolInputError{Message: `schedule.expr required for kind="cron"`}
		}
		tz := strings.TrimSpace(utils.ReadString(schedule["tz"]))
		staggerMs := int64(utils.ReadNumber(schedule["staggerMs"]))
		return NormalizedTaskSchedule{
			Kind:      core.TaskScheduleCron,
			Expr:      expr,
			TZ:        tz,
			StaggerMs: staggerMs,
		}, nil
	default:
		return NormalizedTaskSchedule{}, core.ToolInputError{Message: fmt.Sprintf("unsupported schedule.kind %q", kind)}
	}
}

// ---------------------------------------------------------------------------
// Small pure helpers
// ---------------------------------------------------------------------------

// CronTaskMessageFromJob returns the effective message text from a job.
func CronTaskMessageFromJob(job NormalizedCronJob) string {
	if strings.TrimSpace(job.Text) != "" {
		return strings.TrimSpace(job.Text)
	}
	return strings.TrimSpace(job.Message)
}

// CronWakeModeFromJob resolves the wake mode from a NormalizedCronJob.
func CronWakeModeFromJob(job NormalizedCronJob) core.TaskWakeMode {
	switch strings.TrimSpace(strings.ToLower(job.WakeMode)) {
	case string(core.TaskWakeNextHeartbeat):
		return core.TaskWakeNextHeartbeat
	case string(core.TaskWakeNow):
		return core.TaskWakeNow
	default:
		return core.TaskWakeNow
	}
}

// CronTaskIDArg extracts the task/job ID from the tool call arguments.
func CronTaskIDArg(args map[string]any) string {
	if id := strings.TrimSpace(utils.ReadString(args["jobId"])); id != "" {
		return id
	}
	return strings.TrimSpace(utils.ReadString(args["id"]))
}

// TruncateCronText truncates text to maxLen characters with an ellipsis.
func TruncateCronText(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return text[:maxLen]
	}
	return strings.TrimRight(text[:maxLen-3], " ") + "..."
}

// InferCronDeliveryFromSessionKey infers a delivery target from a session
// key pattern like "agent:<id>:<channel>:<kind>:<to>".
func InferCronDeliveryFromSessionKey(sessionKey string) *core.DeliveryContext {
	sessionKey = strings.TrimSpace(strings.ToLower(sessionKey))
	if sessionKey == "" {
		return nil
	}
	if idx := strings.LastIndex(sessionKey, ":thread:"); idx > 0 {
		sessionKey = strings.TrimSpace(sessionKey[:idx])
	}
	parts := strings.Split(sessionKey, ":")
	if len(parts) < 5 || parts[0] != "agent" {
		return nil
	}
	channel := strings.TrimSpace(parts[2])
	kind := strings.TrimSpace(parts[3])
	switch kind {
	case "direct", "group", "topic":
		to := strings.TrimSpace(strings.Join(parts[4:], ":"))
		if channel == "" || to == "" {
			return nil
		}
		return &core.DeliveryContext{Channel: channel, To: to}
	default:
		return nil
	}
}
