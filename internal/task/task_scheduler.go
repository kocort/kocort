package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

// TaskRuntime is the interface required by TaskScheduler.Start and
// TaskScheduler.RunDueTasks to interact with the broader runtime.
type TaskRuntime interface {
	HandleScheduledSystemEvent(ctx context.Context, task core.TaskRecord) error
	Run(ctx context.Context, req core.AgentRunRequest) (core.AgentRunResult, error)
	ActiveRunsTotalCount() int
}

// TaskScheduleRequest describes parameters for scheduling or updating a task.
type TaskScheduleRequest struct {
	Kind                   core.TaskKind
	AgentID                string
	SessionKey             string
	Title                  string
	Message                string
	Channel                string
	To                     string
	AccountID              string
	ThreadID               string
	Deliver                bool
	DeliveryMode           string
	DeliveryBestEffort     bool
	PayloadKind            core.TaskPayloadKind
	SessionTarget          core.TaskSessionTarget
	WakeMode               core.TaskWakeMode
	FailureAlertAfter      int
	FailureAlertCooldownMs int64
	FailureAlertChannel    string
	FailureAlertTo         string
	FailureAlertAccountID  string
	FailureAlertMode       string
	WorkspaceDir           string
	RunID                  string
	ParentRunID            string
	ScheduleKind           core.TaskScheduleKind
	ScheduleAt             time.Time
	ScheduleEveryMs        int64
	ScheduleAnchorMs       int64
	ScheduleExpr           string
	ScheduleTZ             string
	ScheduleStaggerMs      int64
	IntervalSeconds        int
	RunAt                  time.Time
}

// TaskScheduler manages a set of scheduled tasks and ticks them when due.
type TaskScheduler struct {
	mu              sync.Mutex
	stateDir        string
	tasks           map[string]*core.TaskRecord
	ticker          *time.Ticker
	stop            chan struct{}
	now             func() time.Time
	maxConcurrent   int
	onEvent         func(core.TaskRecord, string, map[string]any)
	failureNotifyFn func(ctx context.Context, task core.TaskRecord) error
}

// SetNow overrides the time source (for testing).
func (s *TaskScheduler) SetNow(fn func() time.Time) {
	s.now = fn
}

// NewTaskScheduler creates a TaskScheduler persisting state under stateDir.
func NewTaskScheduler(stateDir string, cfg config.TasksConfig) (*TaskScheduler, error) {
	base := filepath.Join(strings.TrimSpace(stateDir), "tasks")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	s := &TaskScheduler{
		stateDir:      base,
		tasks:         map[string]*core.TaskRecord{},
		stop:          make(chan struct{}),
		now:           func() time.Time { return time.Now().UTC() },
		maxConcurrent: cfg.MaxConcurrent,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *TaskScheduler) Schedule(req TaskScheduleRequest) (core.TaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := session.RandomToken(10)
	if err != nil {
		return core.TaskRecord{}, err
	}
	now := s.now()
	record := core.TaskRecord{
		ID:                     id,
		Kind:                   nonEmptyTaskKind(req.Kind, core.TaskKindScheduled),
		Status:                 core.TaskStatusScheduled,
		AgentID:                session.NormalizeAgentID(req.AgentID),
		SessionKey:             strings.TrimSpace(req.SessionKey),
		RunID:                  strings.TrimSpace(req.RunID),
		ParentRunID:            strings.TrimSpace(req.ParentRunID),
		Title:                  strings.TrimSpace(req.Title),
		Message:                strings.TrimSpace(req.Message),
		Channel:                strings.TrimSpace(req.Channel),
		To:                     strings.TrimSpace(req.To),
		AccountID:              strings.TrimSpace(req.AccountID),
		ThreadID:               strings.TrimSpace(req.ThreadID),
		Deliver:                req.Deliver,
		DeliveryMode:           strings.TrimSpace(req.DeliveryMode),
		DeliveryBestEffort:     req.DeliveryBestEffort,
		PayloadKind:            nonEmptyTaskPayloadKind(req.PayloadKind, core.TaskPayloadKindAgentTurn),
		SessionTarget:          nonEmptyTaskSessionTarget(req.SessionTarget, core.TaskSessionTargetIsolated),
		WakeMode:               nonEmptyTaskWakeMode(req.WakeMode, core.TaskWakeNow),
		FailureAlertAfter:      taskMaxInt(req.FailureAlertAfter, 0),
		FailureAlertCooldownMs: taskMaxInt64(req.FailureAlertCooldownMs, 0),
		FailureAlertChannel:    strings.TrimSpace(req.FailureAlertChannel),
		FailureAlertTo:         strings.TrimSpace(req.FailureAlertTo),
		FailureAlertAccountID:  strings.TrimSpace(req.FailureAlertAccountID),
		FailureAlertMode:       strings.TrimSpace(req.FailureAlertMode),
		WorkspaceDir:           strings.TrimSpace(req.WorkspaceDir),
		ScheduleKind:           req.ScheduleKind,
		ScheduleAt:             req.ScheduleAt,
		ScheduleEveryMs:        req.ScheduleEveryMs,
		ScheduleAnchorMs:       req.ScheduleAnchorMs,
		ScheduleExpr:           strings.TrimSpace(req.ScheduleExpr),
		ScheduleTZ:             strings.TrimSpace(req.ScheduleTZ),
		ScheduleStaggerMs:      req.ScheduleStaggerMs,
		IntervalSeconds:        req.IntervalSeconds,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if record.ScheduleAt.IsZero() &&
		!req.RunAt.IsZero() &&
		req.ScheduleKind != core.TaskScheduleEvery &&
		req.ScheduleKind != core.TaskScheduleCron &&
		req.ScheduleEveryMs <= 0 &&
		req.IntervalSeconds <= 0 &&
		strings.TrimSpace(req.ScheduleExpr) == "" {
		record.ScheduleAt = req.RunAt
	}
	if err := NormalizeTaskSchedule(&record, now); err != nil {
		return core.TaskRecord{}, err
	}
	runAt, err := ComputeTaskNextRunAt(record, now)
	if err != nil {
		return core.TaskRecord{}, err
	}
	if !req.RunAt.IsZero() && (record.ScheduleKind == core.TaskScheduleEvery || record.ScheduleKind == core.TaskScheduleCron) {
		runAt = req.RunAt.UTC()
	}
	if runAt.IsZero() {
		runAt = now
	}
	record.NextRunAt = runAt
	s.tasks[record.ID] = &record
	err = s.persistLocked()
	if err == nil {
		s.emitEventLocked(record, "scheduled", nil)
	}
	return record, err
}

func (s *TaskScheduler) MarkQueued(taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(taskID)]
	if record == nil {
		return nil
	}
	now := s.now()
	record.Status = core.TaskStatusQueued
	record.UpdatedAt = now
	if record.NextRunAt.IsZero() {
		record.NextRunAt = now
	}
	err := s.persistLocked()
	if err == nil {
		s.emitEventLocked(*record, "queued", nil)
	}
	return err
}

func (s *TaskScheduler) RegisterSubagent(record SubagentRunRecord, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	task := core.TaskRecord{
		ID:           record.RunID,
		Kind:         core.TaskKindSubagent,
		Status:       core.TaskStatusRunning,
		AgentID:      session.NormalizeAgentID(agentID),
		SessionKey:   record.ChildSessionKey,
		RunID:        record.RunID,
		ParentRunID:  record.RequesterSessionKey,
		Title:        utils.NonEmpty(strings.TrimSpace(record.Label), "Subagent Task"),
		Message:      strings.TrimSpace(record.Task),
		WorkspaceDir: strings.TrimSpace(record.WorkspaceDir),
		CreatedAt:    nonZeroTime(record.CreatedAt, now),
		UpdatedAt:    now,
		LastRunAt:    nonZeroTime(record.StartedAt, now),
	}
	s.tasks[task.ID] = &task
	err := s.persistLocked()
	if err == nil {
		s.emitEventLocked(task, "subagent_registered", nil)
	}
	return err
}

func (s *TaskScheduler) CompleteSubagent(runID string, result core.AgentRunResult, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(runID)]
	if record == nil {
		return nil
	}
	now := s.now()
	record.UpdatedAt = now
	record.CompletedAt = now
	record.LastRunAt = now
	record.ResultText = strings.TrimSpace(ExtractFinalText(result))
	if err != nil {
		record.Status = core.TaskStatusFailed
		record.LastError = strings.TrimSpace(err.Error())
	} else {
		record.Status = core.TaskStatusCompleted
		record.LastError = ""
	}
	persistErr := s.persistLocked()
	if persistErr == nil {
		s.emitEventLocked(*record, string(record.Status), map[string]any{
			"error":      record.LastError,
			"resultText": record.ResultText,
		})
	}
	return persistErr
}

func (s *TaskScheduler) MarkRunStarted(taskID, runID, sessionKey string) error {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(taskID)]
	if record == nil {
		return nil
	}
	now := s.now()
	record.Status = core.TaskStatusRunning
	record.RunID = strings.TrimSpace(runID)
	record.SessionKey = utils.NonEmpty(strings.TrimSpace(sessionKey), record.SessionKey)
	record.LastRunAt = now
	record.UpdatedAt = now
	err := s.persistLocked()
	if err == nil {
		s.emitEventLocked(*record, "running", map[string]any{"runId": record.RunID})
	}
	return err
}

func (s *TaskScheduler) MarkRunFinished(taskID string, result core.AgentRunResult, err error, nextRunAt time.Time) error {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(taskID)]
	if record == nil {
		return nil
	}
	now := s.now()
	record.UpdatedAt = now
	record.CompletedAt = now
	record.ResultText = strings.TrimSpace(ExtractFinalText(result))
	if record.Status == core.TaskStatusCanceled {
		if err != nil {
			record.LastError = strings.TrimSpace(err.Error())
		}
		persistErr := s.persistLocked()
		if persistErr == nil {
			s.emitEventLocked(*record, "canceled", map[string]any{"error": record.LastError})
		}
		return persistErr
	}
	if result.Queued {
		record.Status = core.TaskStatusQueued
		record.LastError = ""
		record.CompletedAt = time.Time{}
		record.ConsecutiveErrors = 0
		if record.NextRunAt.IsZero() {
			record.NextRunAt = now
		}
		return s.persistLocked()
	}
	if err != nil {
		record.Status = core.TaskStatusFailed
		record.LastError = strings.TrimSpace(err.Error())
		record.ConsecutiveErrors++
	} else if record.ScheduleKind == core.TaskScheduleEvery || record.ScheduleKind == core.TaskScheduleCron || record.IntervalSeconds > 0 {
		if nextRunAt.IsZero() {
			var nextErr error
			nextRunAt, nextErr = ComputeTaskNextRunAt(*record, now)
			if nextErr != nil {
				record.Status = core.TaskStatusFailed
				record.LastError = strings.TrimSpace(nextErr.Error())
				nextRunAt = time.Time{}
			}
		}
		if nextRunAt.After(now) {
			record.Status = core.TaskStatusScheduled
			record.NextRunAt = nextRunAt
			record.LastError = ""
			record.ConsecutiveErrors = 0
		} else {
			record.Status = core.TaskStatusCompleted
			record.NextRunAt = time.Time{}
			record.LastError = ""
			record.ConsecutiveErrors = 0
		}
	} else {
		record.Status = core.TaskStatusCompleted
		record.NextRunAt = time.Time{}
		record.LastError = ""
		record.ConsecutiveErrors = 0
	}
	persistErr := s.persistLocked()
	if persistErr == nil {
		s.emitEventLocked(*record, string(record.Status), map[string]any{
			"error":      record.LastError,
			"resultText": record.ResultText,
		})
	}
	return persistErr
}

func (s *TaskScheduler) Cancel(id string) (core.TaskRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(id)]
	if record == nil {
		return core.TaskRecord{}, false, nil
	}
	now := s.now()
	record.Status = core.TaskStatusCanceled
	record.CanceledAt = now
	record.UpdatedAt = now
	if err := s.persistLocked(); err != nil {
		return core.TaskRecord{}, false, err
	}
	copy := *record
	s.emitEventLocked(copy, "canceled", nil)
	return copy, true, nil
}

func (s *TaskScheduler) Update(id string, req TaskScheduleRequest) (core.TaskRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(id)]
	if record == nil {
		return core.TaskRecord{}, false, nil
	}
	now := s.now()
	if strings.TrimSpace(req.AgentID) != "" {
		record.AgentID = session.NormalizeAgentID(req.AgentID)
	}
	if strings.TrimSpace(req.SessionKey) != "" {
		record.SessionKey = strings.TrimSpace(req.SessionKey)
	}
	if strings.TrimSpace(req.Title) != "" {
		record.Title = strings.TrimSpace(req.Title)
	}
	if strings.TrimSpace(req.Message) != "" {
		record.Message = strings.TrimSpace(req.Message)
	}
	if strings.TrimSpace(req.Channel) != "" {
		record.Channel = strings.TrimSpace(req.Channel)
	}
	if strings.TrimSpace(req.To) != "" {
		record.To = strings.TrimSpace(req.To)
	}
	if strings.TrimSpace(req.AccountID) != "" {
		record.AccountID = strings.TrimSpace(req.AccountID)
	}
	if strings.TrimSpace(req.ThreadID) != "" {
		record.ThreadID = strings.TrimSpace(req.ThreadID)
	}
	if req.IntervalSeconds >= 0 {
		record.IntervalSeconds = req.IntervalSeconds
		if record.ScheduleKind == core.TaskScheduleEvery && req.ScheduleEveryMs <= 0 {
			record.ScheduleEveryMs = int64(req.IntervalSeconds) * int64(time.Second/time.Millisecond)
		}
	}
	if req.ScheduleKind != "" {
		record.ScheduleKind = req.ScheduleKind
	}
	if !req.ScheduleAt.IsZero() {
		record.ScheduleAt = req.ScheduleAt
	}
	if req.ScheduleEveryMs > 0 {
		record.ScheduleEveryMs = req.ScheduleEveryMs
	}
	if req.ScheduleAnchorMs > 0 {
		record.ScheduleAnchorMs = req.ScheduleAnchorMs
	}
	if strings.TrimSpace(req.ScheduleExpr) != "" {
		record.ScheduleExpr = strings.TrimSpace(req.ScheduleExpr)
	}
	if strings.TrimSpace(req.ScheduleTZ) != "" {
		record.ScheduleTZ = strings.TrimSpace(req.ScheduleTZ)
	}
	if req.ScheduleStaggerMs >= 0 {
		record.ScheduleStaggerMs = req.ScheduleStaggerMs
	}
	if !req.RunAt.IsZero() &&
		req.ScheduleKind != core.TaskScheduleEvery &&
		req.ScheduleKind != core.TaskScheduleCron &&
		req.ScheduleEveryMs <= 0 &&
		req.IntervalSeconds <= 0 &&
		strings.TrimSpace(req.ScheduleExpr) == "" {
		record.ScheduleAt = req.RunAt
	}
	if err := NormalizeTaskSchedule(record, now); err != nil {
		return core.TaskRecord{}, false, err
	}
	if nextRunAt, err := ComputeTaskNextRunAt(*record, now); err == nil {
		record.NextRunAt = nextRunAt
	} else {
		return core.TaskRecord{}, false, err
	}
	if !req.RunAt.IsZero() && (record.ScheduleKind == core.TaskScheduleEvery || record.ScheduleKind == core.TaskScheduleCron) {
		record.NextRunAt = req.RunAt.UTC()
	}
	record.Deliver = req.Deliver
	if strings.TrimSpace(req.DeliveryMode) != "" {
		record.DeliveryMode = strings.TrimSpace(req.DeliveryMode)
	}
	record.DeliveryBestEffort = req.DeliveryBestEffort
	if req.PayloadKind != "" {
		record.PayloadKind = req.PayloadKind
	}
	if req.SessionTarget != "" {
		record.SessionTarget = req.SessionTarget
	}
	if req.WakeMode != "" {
		record.WakeMode = req.WakeMode
	}
	if req.FailureAlertAfter >= 0 {
		record.FailureAlertAfter = req.FailureAlertAfter
	}
	if req.FailureAlertCooldownMs >= 0 {
		record.FailureAlertCooldownMs = req.FailureAlertCooldownMs
	}
	if strings.TrimSpace(req.FailureAlertChannel) != "" || req.FailureAlertChannel == "" {
		record.FailureAlertChannel = strings.TrimSpace(req.FailureAlertChannel)
	}
	if strings.TrimSpace(req.FailureAlertTo) != "" || req.FailureAlertTo == "" {
		record.FailureAlertTo = strings.TrimSpace(req.FailureAlertTo)
	}
	if strings.TrimSpace(req.FailureAlertAccountID) != "" || req.FailureAlertAccountID == "" {
		record.FailureAlertAccountID = strings.TrimSpace(req.FailureAlertAccountID)
	}
	if strings.TrimSpace(req.FailureAlertMode) != "" || req.FailureAlertMode == "" {
		record.FailureAlertMode = strings.TrimSpace(req.FailureAlertMode)
	}
	if strings.TrimSpace(req.WorkspaceDir) != "" {
		record.WorkspaceDir = strings.TrimSpace(req.WorkspaceDir)
	}
	record.UpdatedAt = now
	if record.Status == core.TaskStatusCompleted || record.Status == core.TaskStatusFailed || record.Status == core.TaskStatusCanceled {
		record.Status = core.TaskStatusScheduled
		record.CompletedAt = time.Time{}
		record.CanceledAt = time.Time{}
		record.LastError = ""
	}
	if err := s.persistLocked(); err != nil {
		return core.TaskRecord{}, false, err
	}
	copy := *record
	s.emitEventLocked(copy, "updated", nil)
	return copy, true, nil
}

func (s *TaskScheduler) Delete(id string) (core.TaskRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(id)]
	if record == nil {
		return core.TaskRecord{}, false, nil
	}
	copy := *record
	delete(s.tasks, strings.TrimSpace(id))
	if err := s.persistLocked(); err != nil {
		return core.TaskRecord{}, false, err
	}
	s.emitEventLocked(copy, "deleted", nil)
	return copy, true, nil
}

// CancelWithRun cancels a task by ID and, if runs is non-nil, also cancels
// any associated active run. Returns the canceled record or nil when not found.
func (s *TaskScheduler) CancelWithRun(id string, runs *ActiveRunRegistry) (*core.TaskRecord, error) {
	record, ok, err := s.Cancel(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if runs != nil && strings.TrimSpace(record.SessionKey) != "" && strings.TrimSpace(record.RunID) != "" {
		_ = runs.CancelRun(record.SessionKey, record.RunID) // best-effort; failure is non-critical
	}
	return &record, nil
}

// DeleteWithRun deletes a task by ID and, if runs is non-nil, also cancels
// any associated active run. Returns the deleted record or nil when not found.
func (s *TaskScheduler) DeleteWithRun(id string, runs *ActiveRunRegistry) (*core.TaskRecord, error) {
	record, ok, err := s.Delete(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if runs != nil && strings.TrimSpace(record.SessionKey) != "" && strings.TrimSpace(record.RunID) != "" {
		_ = runs.CancelRun(record.SessionKey, record.RunID) // best-effort; failure is non-critical
	}
	return &record, nil
}

func (s *TaskScheduler) MarkFailureAlertSent(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(id)]
	if record == nil {
		return nil
	}
	record.LastFailureAlertAt = at.UTC()
	record.UpdatedAt = s.now()
	return s.persistLocked()
}

func (s *TaskScheduler) Get(id string) *core.TaskRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.tasks[strings.TrimSpace(id)]
	if record == nil {
		return nil
	}
	copy := *record
	return &copy
}

func (s *TaskScheduler) List() []core.TaskRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.TaskRecord, 0, len(s.tasks))
	for _, item := range s.tasks {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}
	return out
}

func (s *TaskScheduler) Due(now time.Time) []core.TaskRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []core.TaskRecord
	for _, item := range s.tasks {
		if item == nil {
			continue
		}
		if item.Status != core.TaskStatusScheduled && item.Status != core.TaskStatusQueued {
			continue
		}
		if item.NextRunAt.IsZero() || !item.NextRunAt.After(now) {
			out = append(out, *item)
		}
	}
	return out
}

// Start begins the ticker loop that runs due tasks via the provided TaskRuntime.
func (s *TaskScheduler) Start(ctx context.Context, rt TaskRuntime, cfg config.TasksConfig) {
	if s == nil || rt == nil {
		return
	}
	enabled := cfg.Enabled == nil || *cfg.Enabled
	if !enabled {
		return
	}
	tick := time.Second
	if cfg.TickSeconds > 0 {
		tick = time.Duration(cfg.TickSeconds) * time.Second
	}
	s.mu.Lock()
	if s.ticker != nil {
		s.mu.Unlock()
		return
	}
	s.ticker = time.NewTicker(tick)
	s.mu.Unlock()
	go func() {
		for {
			select {
			case <-ctx.Done():
				s.Stop()
				return
			case <-s.stop:
				return
			case <-s.ticker.C:
				s.RunDueTasks(context.Background(), rt)
			}
		}
	}()
}

func (s *TaskScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

func (s *TaskScheduler) Summary() core.TaskSchedulerSummary {
	if s == nil {
		return core.TaskSchedulerSummary{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	summary := core.TaskSchedulerSummary{
		Enabled:       true,
		ByStatus:      map[string]int{},
		MaxConcurrent: s.maxConcurrent,
	}
	for _, item := range s.tasks {
		if item == nil {
			continue
		}
		summary.Total++
		summary.ByStatus[string(item.Status)]++
	}
	return summary
}

func (s *TaskScheduler) SetEventSink(fn func(core.TaskRecord, string, map[string]any)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onEvent = fn
}

// SetFailureNotifier registers a callback invoked when a task exceeds its
// failure threshold.  Using a callback (rather than a method on TaskRuntime)
// lets the runtime inject its deliverer and audit logger without adding
// MaybeNotifyTaskFailure to the TaskRuntime interface.
func (s *TaskScheduler) SetFailureNotifier(fn func(ctx context.Context, task core.TaskRecord) error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureNotifyFn = fn
}

// RunDueTasks fires all due tasks via the provided TaskRuntime.
func (s *TaskScheduler) RunDueTasks(ctx context.Context, rt TaskRuntime) {
	if s == nil || rt == nil {
		return
	}
	now := s.now()
	due := s.Due(now)
	if len(due) == 0 {
		return
	}
	for _, task := range due {
		if s.maxConcurrent > 0 && rt.ActiveRunsTotalCount() >= s.maxConcurrent {
			return
		}
		_ = s.MarkQueued(task.ID) // best-effort; failure is non-critical
		req := core.AgentRunRequest{
			TaskID:            task.ID,
			Message:           task.Message,
			SessionKey:        task.SessionKey,
			AgentID:           utils.NonEmpty(task.AgentID, session.DefaultAgentID),
			Channel:           task.Channel,
			To:                task.To,
			AccountID:         task.AccountID,
			ThreadID:          task.ThreadID,
			Deliver:           task.Deliver,
			WorkspaceOverride: task.WorkspaceDir,
		}
		if task.PayloadKind == core.TaskPayloadKindSystemEvent && task.SessionTarget == core.TaskSessionTargetMain {
			go func(taskRecord core.TaskRecord) {
				err := rt.HandleScheduledSystemEvent(ctx, taskRecord)
				_ = s.MarkRunFinished(taskRecord.ID, core.AgentRunResult{ // best-effort; failure is non-critical
					Payloads: []core.ReplyPayload{{Text: strings.TrimSpace(taskRecord.Message)}},
				}, err, time.Time{})
				if err != nil {
					if updated := s.Get(taskRecord.ID); updated != nil && s.failureNotifyFn != nil {
						_ = s.failureNotifyFn(ctx, *updated) // best-effort; failure is non-critical
					}
				}
			}(task)
			continue
		}
		go func(nextReq core.AgentRunRequest, taskRecord core.TaskRecord) {
			result, err := rt.Run(ctx, nextReq)
			_ = s.MarkRunFinished(taskRecord.ID, result, err, time.Time{}) // best-effort; failure is non-critical
			if err != nil {
				if updated := s.Get(taskRecord.ID); updated != nil && s.failureNotifyFn != nil {
					_ = s.failureNotifyFn(ctx, *updated) // best-effort; failure is non-critical
				}
			}
		}(req, task)
	}
}

func (s *TaskScheduler) load() error {
	path := filepath.Join(s.stateDir, "tasks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var items []core.TaskRecord
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	for _, item := range items {
		copy := item
		s.tasks[item.ID] = &copy
	}
	return nil
}

func (s *TaskScheduler) persistLocked() error {
	items := make([]core.TaskRecord, 0, len(s.tasks))
	for _, item := range s.tasks {
		if item == nil {
			continue
		}
		items = append(items, *item)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.stateDir, "tasks.json"), data, 0o644)
}

func nonEmptyTaskKind(kind core.TaskKind, fallback core.TaskKind) core.TaskKind {
	if strings.TrimSpace(string(kind)) == "" {
		return fallback
	}
	return kind
}

func nonEmptyTaskPayloadKind(kind core.TaskPayloadKind, fallback core.TaskPayloadKind) core.TaskPayloadKind {
	if strings.TrimSpace(string(kind)) == "" {
		return fallback
	}
	return kind
}

func nonEmptyTaskSessionTarget(target core.TaskSessionTarget, fallback core.TaskSessionTarget) core.TaskSessionTarget {
	if strings.TrimSpace(string(target)) == "" {
		return fallback
	}
	return target
}

func nonEmptyTaskWakeMode(mode core.TaskWakeMode, fallback core.TaskWakeMode) core.TaskWakeMode {
	if strings.TrimSpace(string(mode)) == "" {
		return fallback
	}
	return mode
}

func (s *TaskScheduler) String() string {
	return fmt.Sprintf("TaskScheduler(tasks=%d)", len(s.List()))
}

func (s *TaskScheduler) emitEventLocked(record core.TaskRecord, eventType string, data map[string]any) {
	if s == nil || s.onEvent == nil {
		return
	}
	s.onEvent(record, strings.TrimSpace(eventType), utils.CloneAnyMap(data))
}
