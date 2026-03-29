package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/heartbeat"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"

	"github.com/kocort/kocort/utils"
)

func TestTaskSchedulerRunsScheduledAndIntervalTasks(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	scheduler, err := task.NewTaskScheduler(baseDir, config.TasksConfig{Enabled: utils.BoolPtr(true), MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Tasks:      scheduler,
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "done:" + runCtx.Request.Message})
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "done:" + runCtx.Request.Message}}}, nil
		}},
	}
	oneShot, err := rt.ScheduleTask(context.Background(), task.TaskScheduleRequest{
		AgentID:    "main",
		Message:    "one-shot",
		RunAt:      time.Now().UTC().Add(-time.Second),
		Deliver:    false,
		Channel:    "tasks",
		To:         "worker",
		SessionKey: session.BuildMainSessionKey("main"),
	})
	if err != nil {
		t.Fatalf("schedule one-shot task: %v", err)
	}
	interval, err := rt.ScheduleTask(context.Background(), task.TaskScheduleRequest{
		AgentID:         "main",
		Message:         "interval",
		RunAt:           time.Now().UTC().Add(-time.Second),
		IntervalSeconds: 60,
		Deliver:         false,
		Channel:         "tasks",
		To:              "worker",
		SessionKey:      session.BuildMainSessionKey("main"),
	})
	if err != nil {
		t.Fatalf("schedule interval task: %v", err)
	}
	scheduler.RunDueTasks(context.Background(), rt)
	waitForCondition(t, time.Second, func() bool {
		first := scheduler.Get(oneShot.ID)
		second := scheduler.Get(interval.ID)
		return first != nil && first.Status == core.TaskStatusCompleted &&
			second != nil && second.Status == core.TaskStatusScheduled && !second.NextRunAt.IsZero()
	})
	first := scheduler.Get(oneShot.ID)
	second := scheduler.Get(interval.ID)
	if first == nil || !strings.Contains(strings.TrimSpace(first.ResultText), "one-shot") {
		t.Fatalf("unexpected one-shot task state: %+v", first)
	}
	if second == nil || !second.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("expected interval task to be rescheduled, got %+v", second)
	}
}

func TestRuntimeTaskCancelPreservesCanceledStatus(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	scheduler, err := task.NewTaskScheduler(baseDir, config.TasksConfig{Enabled: utils.BoolPtr(true), MaxConcurrent: 1})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	started := make(chan struct{}, 1)
	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Tasks:      scheduler,
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return core.AgentRunResult{}, ctx.Err()
		}},
	}
	task, err := rt.ScheduleTask(context.Background(), task.TaskScheduleRequest{
		AgentID:    "main",
		Message:    "cancel-me",
		Deliver:    false,
		SessionKey: session.BuildMainSessionKey("main"),
	})
	if err != nil {
		t.Fatalf("schedule task: %v", err)
	}
	go func() {
		_, _ = rt.Run(context.Background(), core.AgentRunRequest{
			TaskID:     task.ID,
			AgentID:    "main",
			SessionKey: task.SessionKey,
			Message:    task.Message,
			Channel:    "tasks",
			To:         "worker",
			Deliver:    false,
			Timeout:    time.Second,
		})
	}()
	waitForCondition(t, time.Second, func() bool {
		select {
		case <-started:
			return true
		default:
			record := scheduler.Get(task.ID)
			return record != nil && record.Status == core.TaskStatusRunning
		}
	})
	canceled, err := rt.CancelTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	if canceled == nil || canceled.Status != core.TaskStatusCanceled {
		t.Fatalf("expected canceled task record, got %+v", canceled)
	}
	waitForCondition(t, time.Second, func() bool {
		record := scheduler.Get(task.ID)
		return record != nil && record.Status == core.TaskStatusCanceled
	})
}

func TestCronToolSchedulesReminderForCurrentSession(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-cron"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tasks:      tasks,
		Tools:      tool.NewToolRegistry(tool.NewCronTool()),
	}
	session, _ := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "webchat-user", "")
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime:  runtime,
		Request:  core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Channel: "webchat", To: "webchat-user"},
		Session:  session,
		Identity: identity,
	}
	result, err := runtime.ExecuteTool(context.Background(), runCtx, "cron", map[string]any{
		"action": "add",
		"job": map[string]any{
			"name": "laundry reminder",
			"schedule": map[string]any{
				"kind": "at",
				"at":   time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339),
			},
			"payload": map[string]any{
				"kind": "systemEvent",
				"text": "Reminder: 鎷胯。鏈嶏紒",
			},
		},
	})
	if err != nil {
		t.Fatalf("cron add: %v", err)
	}
	var payload struct {
		Status string          `json:"status"`
		Task   core.TaskRecord `json:"task"`
	}
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.Status != "scheduled" {
		t.Fatalf("expected scheduled status, got %q", payload.Status)
	}
	task := runtime.GetTask(context.Background(), payload.Task.ID)
	if task == nil {
		t.Fatalf("expected scheduled task")
	}
	if task.SessionKey != sessionKey || task.Channel != "webchat" || task.To != "webchat-user" || !task.Deliver {
		t.Fatalf("expected inferred session delivery target, got %+v", *task)
	}
	if strings.TrimSpace(task.Message) != "Reminder: 鎷胯。鏈嶏紒" {
		t.Fatalf("expected reminder text, got %q", task.Message)
	}
}

func TestCronToolDelayMinutesSchedulesOneShotReminder(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-cron"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tasks:      tasks,
		Tools:      tool.NewToolRegistry(tool.NewCronTool()),
	}
	session, _ := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "webchat-user", "")
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime:  runtime,
		Request:  core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Channel: "webchat", To: "webchat-user"},
		Session:  session,
		Identity: identity,
	}
	result, err := runtime.ExecuteTool(context.Background(), runCtx, "cron", map[string]any{
		"action":       "add",
		"delayMinutes": 5,
		"text":         "Reminder: 鎷胯。鏈嶏紒",
	})
	if err != nil {
		t.Fatalf("cron add: %v", err)
	}
	var payload struct {
		Status string          `json:"status"`
		Task   core.TaskRecord `json:"task"`
	}
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	task := runtime.GetTask(context.Background(), payload.Task.ID)
	if task == nil {
		t.Fatalf("expected scheduled task")
	}
	if task.IntervalSeconds != 0 {
		t.Fatalf("expected one-shot reminder, got interval=%d", task.IntervalSeconds)
	}
	if task.NextRunAt.IsZero() {
		t.Fatalf("expected one-shot reminder run time, got %+v", *task)
	}
	if task.PayloadKind != core.TaskPayloadKindSystemEvent || task.SessionTarget != core.TaskSessionTargetMain {
		t.Fatalf("expected one-shot reminder to route through heartbeat main session, got %+v", *task)
	}
}

func TestCronToolDelaySecondsWithMessageDefaultsToHeartbeatReminder(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-cron"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tasks:      tasks,
		Tools:      tool.NewToolRegistry(tool.NewCronTool()),
	}
	session, _ := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "webchat-user", "")
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime:  runtime,
		Request:  core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Channel: "webchat", To: "webchat-user"},
		Session:  session,
		Identity: identity,
	}
	result, err := runtime.ExecuteTool(context.Background(), runCtx, "cron", map[string]any{
		"action":       "add",
		"delaySeconds": 10,
		"message":      "鎻愰啋锛氳鎷胯。鏈嶏紒",
	})
	if err != nil {
		t.Fatalf("cron add: %v", err)
	}
	var payload struct {
		Status string          `json:"status"`
		Task   core.TaskRecord `json:"task"`
	}
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	task := runtime.GetTask(context.Background(), payload.Task.ID)
	if task == nil {
		t.Fatalf("expected scheduled task")
	}
	if task.PayloadKind != core.TaskPayloadKindSystemEvent || task.SessionTarget != core.TaskSessionTargetMain {
		t.Fatalf("expected flat reminder args to route through heartbeat main session, got %+v", *task)
	}
}

func TestCronToolSupportsEveryRecurringSchedule(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-cron"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tasks:      tasks,
		Tools:      tool.NewToolRegistry(tool.NewCronTool()),
	}
	session, _ := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "webchat-user", "")
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime:  runtime,
		Request:  core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Channel: "webchat", To: "webchat-user"},
		Session:  session,
		Identity: identity,
	}
	result, err := runtime.ExecuteTool(context.Background(), runCtx, "cron", map[string]any{
		"action": "add",
		"job": map[string]any{
			"name": "Repeat reminder",
			"schedule": map[string]any{
				"kind":    "every",
				"everyMs": 120000,
			},
			"payload": map[string]any{
				"kind": "systemEvent",
				"text": "Reminder: stretch",
			},
		},
	})
	if err != nil {
		t.Fatalf("cron add every: %v", err)
	}
	var payload struct {
		Task core.TaskRecord `json:"task"`
	}
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	task := runtime.GetTask(context.Background(), payload.Task.ID)
	if task == nil {
		t.Fatalf("expected scheduled task")
	}
	if task.ScheduleKind != core.TaskScheduleEvery {
		t.Fatalf("expected every schedule, got %+v", *task)
	}
	if task.ScheduleEveryMs != 120000 {
		t.Fatalf("expected everyMs=120000, got %+v", *task)
	}
	if task.NextRunAt.IsZero() || !task.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("expected future next run, got %+v", *task)
	}
}

func TestCronToolSupportsCronExpressionSchedule(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-cron"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tasks:      tasks,
		Tools:      tool.NewToolRegistry(tool.NewCronTool()),
	}
	session, _ := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "webchat-user", "")
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime:  runtime,
		Request:  core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Channel: "webchat", To: "webchat-user"},
		Session:  session,
		Identity: identity,
	}
	result, err := runtime.ExecuteTool(context.Background(), runCtx, "cron", map[string]any{
		"action": "add",
		"job": map[string]any{
			"name": "Cron reminder",
			"schedule": map[string]any{
				"kind": "cron",
				"expr": "*/5 * * * *",
				"tz":   "UTC",
			},
			"payload": map[string]any{
				"kind": "systemEvent",
				"text": "Reminder: water",
			},
		},
	})
	if err != nil {
		t.Fatalf("cron add cron expr: %v", err)
	}
	var payload struct {
		Task core.TaskRecord `json:"task"`
	}
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	task := runtime.GetTask(context.Background(), payload.Task.ID)
	if task == nil {
		t.Fatalf("expected scheduled task")
	}
	if task.ScheduleKind != core.TaskScheduleCron || strings.TrimSpace(task.ScheduleExpr) != "*/5 * * * *" {
		t.Fatalf("expected cron schedule, got %+v", *task)
	}
	if strings.TrimSpace(task.ScheduleTZ) != "UTC" {
		t.Fatalf("expected timezone UTC, got %+v", *task)
	}
	if task.NextRunAt.IsZero() || !task.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("expected future next run, got %+v", *task)
	}
}

func TestScheduledMainSystemEventTaskWithNextHeartbeatDoesNotWakeImmediately(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	audit, err := infra.NewAuditLog(baseDir)
	if err != nil {
		t.Fatalf("new audit log: %v", err)
	}
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	mem := &delivery.MemoryDeliverer{}
	runtime := &Runtime{
		Sessions:     store,
		Audit:        audit,
		SystemEvents: infra.NewSystemEventQueue(),
		Deliverer:    mem,
		ActiveRuns:   task.NewActiveRunRegistry(),
		Queue:        task.NewFollowupQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Tasks:        tasks,
	}
	runtime.Heartbeats = heartbeat.NewHeartbeatRunner(runtime)
	now := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	tasks.SetNow(func() time.Time { return now })
	task, err := tasks.Schedule(task.TaskScheduleRequest{
		AgentID:       "main",
		SessionKey:    session.BuildMainSessionKey("main"),
		Message:       "Reminder: pick up clothes",
		Deliver:       true,
		PayloadKind:   core.TaskPayloadKindSystemEvent,
		SessionTarget: core.TaskSessionTargetMain,
		WakeMode:      core.TaskWakeNextHeartbeat,
		ScheduleKind:  core.TaskScheduleAt,
		ScheduleAt:    now,
	})
	if err != nil {
		t.Fatalf("schedule task: %v", err)
	}

	tasks.RunDueTasks(context.Background(), runtime)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current := tasks.Get(task.ID)
		if current != nil && current.Status == core.TaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	current := tasks.Get(task.ID)
	if current == nil || current.Status != core.TaskStatusCompleted {
		t.Fatalf("expected completed task after enqueue, got %+v", current)
	}
	if !runtime.SystemEvents.Has(task.SessionKey) {
		t.Fatalf("expected queued system event for session %q", task.SessionKey)
	}
	events, err := audit.List(context.Background(), core.AuditQuery{TaskID: task.ID})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	for _, event := range events {
		if event.Type == "heartbeat_started" {
			t.Fatalf("expected next-heartbeat task to avoid immediate heartbeat wake, got %+v", events)
		}
	}
	if len(mem.Records) != 0 {
		t.Fatalf("expected no immediate outbound delivery before heartbeat, got %+v", mem.Records)
	}
}

func TestCronToolListsAndRemovesScheduledTasks(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-main"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tasks:      tasks,
		Tools:      tool.NewToolRegistry(tool.NewCronTool()),
	}
	task, err := runtime.ScheduleTask(context.Background(), task.TaskScheduleRequest{
		AgentID:    "main",
		SessionKey: sessionKey,
		Title:      "existing reminder",
		Message:    "Reminder: ping",
		RunAt:      time.Now().UTC().Add(2 * time.Minute),
		Deliver:    true,
	})
	if err != nil {
		t.Fatalf("schedule task: %v", err)
	}
	session, _ := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "", "", "")
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{Runtime: runtime, Request: core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, To: "owner"}, Session: session, Identity: identity}
	listResult, err := runtime.ExecuteTool(context.Background(), runCtx, "cron", map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("cron list: %v", err)
	}
	if !strings.Contains(listResult.Text, task.ID) {
		t.Fatalf("expected task id in list result, got %s", listResult.Text)
	}
	removeResult, err := runtime.ExecuteTool(context.Background(), runCtx, "cron", map[string]any{"action": "remove", "jobId": task.ID})
	if err != nil {
		t.Fatalf("cron remove: %v", err)
	}
	if !strings.Contains(removeResult.Text, `"status":"canceled"`) {
		t.Fatalf("expected canceled status, got %s", removeResult.Text)
	}
	updated := runtime.GetTask(context.Background(), task.ID)
	if updated == nil || updated.Status != core.TaskStatusCanceled {
		t.Fatalf("expected canceled task, got %+v", updated)
	}
}


func TestScheduledReminderRunsAndDeliversOneShotMessage(t *testing.T) {
	baseDir := t.TempDir()
	mem := &delivery.MemoryDeliverer{}
	rt, err := NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1", MaxTokens: 1024}},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:      "main",
				Default: true,
				Model:   config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		Tasks: config.TasksConfig{
			Enabled:       utils.BoolPtr(true),
			TickSeconds:   1,
			MaxConcurrent: 2,
		},
	}, config.RuntimeConfigParams{
		StateDir:  baseDir,
		AgentID:   "main",
		Deliverer: mem,
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.Backends = nil
	rt.Deliverer = mem
	rt.Backend = backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "Reminder delivered"})
		return core.AgentRunResult{
			Payloads: []core.ReplyPayload{{Text: "Reminder delivered"}},
		}, nil
	})

	task, err := rt.ScheduleTask(context.Background(), task.TaskScheduleRequest{
		Kind:       core.TaskKindScheduled,
		AgentID:    "main",
		SessionKey: session.BuildDirectSessionKey("main", "webchat", "webchat-user"),
		Title:      "Reminder",
		Message:    "Reminder: 鎷胯。鏈嶏紒",
		Channel:    "webchat",
		To:         "webchat-user",
		Deliver:    true,
		RunAt:      time.Now().UTC().Add(-1 * time.Second),
	})
	if err != nil {
		t.Fatalf("schedule task: %v", err)
	}

	rt.Tasks.RunDueTasks(context.Background(), rt)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := rt.GetTask(context.Background(), task.ID); got != nil && got.Status == core.TaskStatusCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	got := rt.GetTask(context.Background(), task.ID)
	if got == nil {
		t.Fatalf("expected scheduled task record")
	}
	if got.Status != core.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %+v", *got)
	}
	if len(mem.Records) == 0 {
		t.Fatalf("expected delivered reminder, got none")
	}
	last := mem.Records[len(mem.Records)-1]
	if last.Kind != core.ReplyKindFinal || strings.TrimSpace(last.Payload.Text) != "Reminder delivered" {
		t.Fatalf("unexpected delivered payload: %+v", last)
	}
}

func TestScheduledMainSystemEventTaskTriggersHeartbeatDelivery(t *testing.T) {
	baseDir := t.TempDir()
	mem := &delivery.MemoryDeliverer{}
	rt, err := NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1", MaxTokens: 1024}},
				},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: &config.AgentDefaultsConfig{
				Heartbeat: &config.AgentHeartbeatConfig{
					Every:  "30m",
					Target: "last",
				},
			},
			List: []config.AgentConfig{{
				ID:      "main",
				Default: true,
				Model:   config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		Tasks: config.TasksConfig{
			Enabled:       utils.BoolPtr(true),
			TickSeconds:   1,
			MaxConcurrent: 2,
		},
	}, config.RuntimeConfigParams{
		StateDir:  baseDir,
		AgentID:   "main",
		Deliverer: mem,
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(func() {
		if rt.Tasks != nil {
			rt.Tasks.Stop()
		}
		if rt.Heartbeats != nil {
			rt.Heartbeats.Stop()
		}
	})
	rt.Backends = nil
	rt.Deliverer = mem
	rt.Backend = backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		if !runCtx.Request.IsHeartbeat {
			t.Fatalf("expected heartbeat run for scheduled system event, got %+v", runCtx.Request)
		}
		if len(runCtx.Request.InternalEvents) == 0 || strings.TrimSpace(runCtx.Request.InternalEvents[0].Text) != "Reminder: 鎷胯。鏈嶏紒" {
			t.Fatalf("expected internal system event for reminder, got %+v", runCtx.Request.InternalEvents)
		}
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "Reminder delivered"})
		return core.AgentRunResult{
			Payloads: []core.ReplyPayload{{Text: "Reminder delivered"}},
		}, nil
	})

	task, err := rt.ScheduleTask(context.Background(), task.TaskScheduleRequest{
		Kind:          core.TaskKindScheduled,
		AgentID:       "main",
		SessionKey:    session.BuildDirectSessionKey("main", "webchat", "webchat-user"),
		Title:         "Reminder",
		Message:       "Reminder: 鎷胯。鏈嶏紒",
		Channel:       "webchat",
		To:            "webchat-user",
		Deliver:       true,
		PayloadKind:   core.TaskPayloadKindSystemEvent,
		SessionTarget: core.TaskSessionTargetMain,
		WakeMode:      core.TaskWakeNow,
		RunAt:         time.Now().UTC().Add(-1 * time.Second),
	})
	if err != nil {
		t.Fatalf("schedule task: %v", err)
	}

	rt.Tasks.RunDueTasks(context.Background(), rt)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mem.Records) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	got := rt.GetTask(context.Background(), task.ID)
	if got == nil {
		t.Fatalf("expected scheduled task record")
	}
	if got.Status != core.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %+v", *got)
	}
	if len(mem.Records) == 0 {
		t.Fatalf("expected delivered reminder via heartbeat, got none")
	}
	last := mem.Records[len(mem.Records)-1]
	if last.Kind != core.ReplyKindFinal || strings.TrimSpace(last.Payload.Text) != "Reminder delivered" {
		t.Fatalf("unexpected delivered payload: %+v", last)
	}
}

func TestScheduledMainSystemEventTaskSkipsHeartbeatAckOnlyReply(t *testing.T) {
	baseDir := t.TempDir()
	mem := &delivery.MemoryDeliverer{}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	rt, err := NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1", MaxTokens: 1024}},
				},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: &config.AgentDefaultsConfig{
				Heartbeat: &config.AgentHeartbeatConfig{
					Every:  "30m",
					Target: "last",
				},
			},
			List: []config.AgentConfig{{
				ID:      "main",
				Default: true,
				Model:   config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		Tasks: config.TasksConfig{
			Enabled:       utils.BoolPtr(true),
			TickSeconds:   1,
			MaxConcurrent: 2,
		},
	}, config.RuntimeConfigParams{
		StateDir:  baseDir,
		AgentID:   "main",
		Deliverer: mem,
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(func() {
		if rt.Tasks != nil {
			rt.Tasks.Stop()
		}
		if rt.Heartbeats != nil {
			rt.Heartbeats.Stop()
		}
	})
	rt.Backends = nil
	rt.Deliverer = mem
	rt.Backend = backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: heartbeat.HeartbeatToken})
		return core.AgentRunResult{
			Payloads: []core.ReplyPayload{{Text: heartbeat.HeartbeatToken}},
		}, nil
	})
	if _, err := rt.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "webchat-user", ""); err != nil {
		t.Fatalf("resolve session: %v", err)
	}

	scheduledTask, err := rt.ScheduleTask(context.Background(), task.TaskScheduleRequest{
		Kind:          core.TaskKindScheduled,
		AgentID:       "main",
		SessionKey:    sessionKey,
		Title:         "Reminder",
		Message:       "Reminder: 鎷胯。鏈嶏紒",
		Channel:       "webchat",
		To:            "webchat-user",
		Deliver:       true,
		PayloadKind:   core.TaskPayloadKindSystemEvent,
		SessionTarget: core.TaskSessionTargetMain,
		WakeMode:      core.TaskWakeNow,
		RunAt:         time.Now().UTC().Add(-1 * time.Second),
	})
	if err != nil {
		t.Fatalf("schedule task: %v", err)
	}

	rt.Tasks.RunDueTasks(context.Background(), rt)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mem.Records) > 0 {
			break
		}
		if got := rt.GetTask(context.Background(), scheduledTask.ID); got != nil && got.Status == core.TaskStatusCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(mem.Records) != 0 {
		t.Fatalf("expected heartbeat ack-only run to be suppressed, got %+v", mem.Records)
	}
}

func TestRunHeartbeatTurnUsesCronEventPrompt(t *testing.T) {
	store := storeForTests(t)
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                   "main",
				WorkspaceDir:         t.TempDir(),
				DefaultProvider:      "test",
				DefaultModel:         "test-model",
				UserTimezone:         "Asia/Shanghai",
				HeartbeatEvery:       "30m",
				HeartbeatTarget:      "last",
				HeartbeatAckMaxChars: 300,
			},
		}),
		Memory:       infra.NullMemoryProvider{},
		Deliverer:    &delivery.MemoryDeliverer{},
		SystemEvents: infra.NewSystemEventQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Queue:        task.NewFollowupQueue(),
		ActiveRuns:   task.NewActiveRunRegistry(),
	}
	runtime.SystemEvents.Enqueue("agent:main:webchat:direct:hb-user", "鎻愰啋锛氭嬁琛ｆ湇", "cron:test")
	runtime.Backend = fakeBackend{
		onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			if !strings.Contains(runCtx.Request.Message, "A scheduled reminder has been triggered") {
				t.Fatalf("expected cron event prompt, got %q", runCtx.Request.Message)
			}
			if !strings.Contains(runCtx.Request.Message, "鎻愰啋锛氭嬁琛ｆ湇") {
				t.Fatalf("expected event text in heartbeat prompt, got %q", runCtx.Request.Message)
			}
			runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "鎻愰啋锛氭嬁琛ｆ湇"})
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "鎻愰啋锛氭嬁琛ｆ湇"}}}, nil
		},
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "hb-user")
	_, err := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "hb-user", "")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if _, err := runtime.RunHeartbeatTurn(context.Background(), heartbeat.HeartbeatWakeRequest{
		Reason:     "cron:test",
		AgentID:    "main",
		SessionKey: sessionKey,
	}); err != nil {
		t.Fatalf("runHeartbeatTurn: %v", err)
	}
}
