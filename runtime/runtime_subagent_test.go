package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
	toolfn "github.com/kocort/kocort/internal/tool"

	"github.com/kocort/kocort/utils"
)

func TestSpawnSubagentRegistersVerifiedMetadata(t *testing.T) {
	registry := task.NewSubagentRegistry()
	result, err := task.SpawnSubagent(context.Background(), registry, task.SubagentSpawnRequest{
		RequesterSessionKey: "agent:main:main",
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "summarize logs",
		MaxSpawnDepth:       3,
		MaxChildren:         2,
		CurrentDepth:        0,
		WorkspaceDir:        "/workspace",
	})
	if err != nil {
		t.Fatalf("spawn subagent: %v", err)
	}
	if result.Status != "accepted" {
		t.Fatalf("expected accepted, got %+v", result)
	}
	if !session.IsSubagentSessionKey(result.ChildSessionKey) {
		t.Fatalf("expected subagent session key, got %s", result.ChildSessionKey)
	}
	if result.SpawnedBy != "agent:main:main" {
		t.Fatalf("unexpected spawnedBy: %s", result.SpawnedBy)
	}
	if result.Lane != core.LaneSubagent {
		t.Fatalf("unexpected lane: %s", result.Lane)
	}
}

func TestSpawnSubagentRejectsSandboxEscape(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                  "main",
				WorkspaceDir:        filepath.Join(baseDir, "workspace-main"),
				SubagentAllowAgents: []string{"worker"},
				SandboxMode:         "all",
			},
			"worker": {
				ID:           "worker",
				WorkspaceDir: filepath.Join(baseDir, "workspace-worker"),
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
	}

	_, err = rt.SpawnSubagent(context.Background(), task.SubagentSpawnRequest{
		RequesterSessionKey: session.BuildMainSessionKey("main"),
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "do work",
	})
	if err == nil || !strings.Contains(err.Error(), "sandboxed requester cannot spawn") {
		t.Fatalf("expected sandbox escape rejection, got %v", err)
	}
}

func TestSpawnSubagentRegistersAndCompletesTaskRecord(t *testing.T) {
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
				ID:                  "main",
				PersonaPrompt:       "You are Kocort.",
				WorkspaceDir:        filepath.Join(baseDir, "workspace-main"),
				DefaultProvider:     "openai",
				DefaultModel:        "gpt-4.1",
				SubagentAllowAgents: []string{"worker"},
			},
			"worker": {
				ID:              "worker",
				PersonaPrompt:   "You are a worker.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
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
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "child ok"})
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "child ok"}}}, nil
		}},
	}
	result, err := rt.SpawnSubagent(context.Background(), task.SubagentSpawnRequest{
		RequesterSessionKey: session.BuildMainSessionKey("main"),
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "do work",
		MaxSpawnDepth:       3,
		MaxChildren:         2,
		CurrentDepth:        0,
	})
	if err != nil {
		t.Fatalf("spawn subagent: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		record := scheduler.Get(result.RunID)
		return record != nil && (record.Status == core.TaskStatusCompleted || record.Status == core.TaskStatusFailed)
	})
	record := scheduler.Get(result.RunID)
	if record == nil {
		t.Fatal("expected task record for subagent run")
	}
	if record.Kind != core.TaskKindSubagent || record.Status != core.TaskStatusCompleted || strings.TrimSpace(record.ResultText) != "child ok" {
		t.Fatalf("unexpected subagent task record: %+v", record)
	}
}

func TestRuntimeDefersSubagentAnnouncementUntilDescendantsSettle(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	registry := task.NewSubagentRegistry()
	runtime := &Runtime{
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
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "parent-ack"})
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "parent-ack"}}}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  registry,
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}
	parentSession := "agent:main:subagent:parent"
	if err := store.Upsert("agent:main:subagent:child-1", core.SessionEntry{SessionID: "sess-child-1"}); err != nil {
		t.Fatalf("seed child-1 session: %v", err)
	}
	if err := store.Upsert("agent:main:subagent:child-2", core.SessionEntry{SessionID: "sess-child-2"}); err != nil {
		t.Fatalf("seed child-2 session: %v", err)
	}
	first := task.SubagentRunRecord{
		RunID:                    "run-child-1",
		ChildSessionKey:          "agent:main:subagent:child-1",
		RequesterSessionKey:      parentSession,
		Task:                     "child one",
		Cleanup:                  "session",
		SpawnMode:                "session",
		SpawnDepth:               2,
		ExpectsCompletionMessage: true,
		CreatedAt:                time.Now().UTC(),
		StartedAt:                time.Now().UTC(),
	}
	second := task.SubagentRunRecord{
		RunID:                    "run-child-2",
		ChildSessionKey:          "agent:main:subagent:child-2",
		RequesterSessionKey:      parentSession,
		Task:                     "child two",
		Cleanup:                  "session",
		SpawnMode:                "session",
		SpawnDepth:               2,
		ExpectsCompletionMessage: true,
		CreatedAt:                time.Now().UTC(),
		StartedAt:                time.Now().UTC(),
	}
	registry.Register(first)
	registry.Register(second)

	runtime.handleSubagentLifecycleCompletion(context.Background(), core.AgentRunRequest{
		RunID:      first.RunID,
		Lane:       core.LaneSubagent,
		SpawnedBy:  parentSession,
		SpawnDepth: 2,
		SessionKey: first.ChildSessionKey,
		Message:    "child one done",
	}, core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "child one done"}}}, nil)

	firstState := registry.Get(first.RunID)
	if firstState == nil || firstState.CompletionDeferredAt.IsZero() {
		t.Fatalf("expected first child completion to be deferred, got %+v", firstState)
	}
	if !firstState.CompletionMessageSentAt.IsZero() {
		t.Fatalf("expected first child completion unsent, got %+v", firstState)
	}

	runtime.handleSubagentLifecycleCompletion(context.Background(), core.AgentRunRequest{
		RunID:      second.RunID,
		Lane:       core.LaneSubagent,
		SpawnedBy:  parentSession,
		SpawnDepth: 2,
		SessionKey: second.ChildSessionKey,
		Message:    "child two done",
	}, core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "child two done"}}}, nil)

	firstState = registry.Get(first.RunID)
	secondState := registry.Get(second.RunID)
	if firstState == nil || firstState.CompletionMessageSentAt.IsZero() {
		t.Fatalf("expected first child announcement flushed after descendants settled, got %+v", firstState)
	}
	if secondState == nil || secondState.CompletionMessageSentAt.IsZero() {
		t.Fatalf("expected second child announcement sent, got %+v", secondState)
	}
}

func TestSubagentRegistryPersistsArchivedRunsAndSweepsOrphans(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	registry := task.NewSubagentRegistry()
	archivePath := filepath.Join(baseDir, "subagents", "runs.json")
	registry.SetArchivePath(archivePath)
	registry.Register(task.SubagentRunRecord{
		RunID:               "run-archived",
		ChildSessionKey:     "agent:main:subagent:archived",
		RequesterSessionKey: "agent:main:main",
		Task:                "archive me",
		CreatedAt:           time.Now().UTC().Add(-2 * time.Minute),
		StartedAt:           time.Now().UTC().Add(-2 * time.Minute),
		EndedAt:             time.Now().UTC().Add(-time.Minute),
		Outcome:             &task.SubagentRunOutcome{Status: task.SubagentOutcomeOK},
		ArchiveAt:           time.Now().UTC().Add(-time.Second),
	})
	registry.SweepExpired()
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archived runs: %v", err)
	}
	if !strings.Contains(string(data), "run-archived") {
		t.Fatalf("expected archived run persisted, got %s", string(data))
	}

	active := task.NewActiveRunRegistry()
	registry.Register(task.SubagentRunRecord{
		RunID:               "run-orphan",
		ChildSessionKey:     "agent:main:subagent:orphan",
		RequesterSessionKey: "agent:main:main",
		Task:                "orphan me",
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
	})
	registry.SweepOrphans(store, active)
	orphan := registry.Get("run-orphan")
	if orphan == nil || orphan.Outcome == nil || orphan.Outcome.Status != task.SubagentOutcomeUnknown {
		t.Fatalf("expected orphan cleanup to mark unknown outcome, got %+v", orphan)
	}
}

func TestSubagentRegistryRestoresFromStateSnapshot(t *testing.T) {
	baseDir := t.TempDir()
	statePath := filepath.Join(baseDir, "subagents", "state.json")

	registry := task.NewSubagentRegistry()
	registry.SetStatePath(statePath)
	registry.Register(task.SubagentRunRecord{
		RunID:               "run-restored",
		ChildSessionKey:     "agent:main:subagent:restored",
		RequesterSessionKey: "agent:main:main",
		Task:                "restore me",
		SpawnMode:           "session",
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
	})

	restored := task.NewSubagentRegistry()
	restored.SetStatePath(statePath)
	if err := restored.RestoreFromState(); err != nil {
		t.Fatalf("restore state: %v", err)
	}
	record := restored.Get("run-restored")
	if record == nil || record.ChildSessionKey != "agent:main:subagent:restored" || record.SpawnMode != "session" {
		t.Fatalf("expected restored run, got %+v", record)
	}
}

func TestRestoreSubagentAnnouncementRecoveryFlushesReadyAndSchedulesDeferred(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	registry := task.NewSubagentRegistry()
	registry.Register(task.SubagentRunRecord{
		RunID:                    "run-ready",
		ChildSessionKey:          "agent:worker:subagent:ready",
		RequesterSessionKey:      "agent:main:main",
		Task:                     "ready child",
		Label:                    "ready child",
		ExpectsCompletionMessage: true,
		SpawnMode:                "run",
		CreatedAt:                time.Now().UTC().Add(-2 * time.Minute),
		StartedAt:                time.Now().UTC().Add(-2 * time.Minute),
		EndedAt:                  time.Now().UTC().Add(-time.Minute),
		FrozenResultText:         "READY-CHILD-RESULT",
	})
	registry.Register(task.SubagentRunRecord{
		RunID:                    "run-deferred",
		ChildSessionKey:          "agent:worker:subagent:deferred",
		RequesterSessionKey:      "agent:main:main",
		Task:                     "deferred child",
		Label:                    "deferred child",
		ExpectsCompletionMessage: true,
		SpawnMode:                "run",
		CreatedAt:                time.Now().UTC().Add(-2 * time.Minute),
		StartedAt:                time.Now().UTC().Add(-2 * time.Minute),
		EndedAt:                  time.Now().UTC().Add(-time.Minute),
		FrozenResultText:         "DEFERRED-CHILD-RESULT",
		NextAnnounceAttemptAt:    time.Now().UTC().Add(80 * time.Millisecond),
		CompletionDeferredAt:     time.Time{},
		CompletionMessageSentAt:  time.Time{},
	})
	runtime := &Runtime{
		Sessions:  store,
		Subagents: registry,
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
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })
	parentSessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(parentSessionKey, core.SessionEntry{SessionID: "sess_parent"}); err != nil {
		t.Fatalf("upsert parent session: %v", err)
	}
	readyDone := make(chan struct{}, 1)
	runtime.Backend = fakeBackend{
		onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			if strings.Contains(runCtx.Request.Message, "READY-CHILD-RESULT") {
				select {
				case readyDone <- struct{}{}:
				default:
				}
			}
			return core.AgentRunResult{}, nil
		},
	}
	runtime.restoreSubagentAnnouncementRecovery()
	select {
	case <-readyDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready announcement flush after restore")
	}
}

func TestSpawnedSubagentAnnouncesCompletionBackToParentSession(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	parentSessionKey := session.BuildMainSessionKey("main")
	parentDone := make(chan string, 1)

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				PersonaPrompt:   "You are a worker.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if runCtx.Request.Lane == core.LaneSubagent {
					runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "worker finished task"})
					return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "worker finished task"}}}, nil
				}
				if runCtx.Session.SessionKey == parentSessionKey && strings.Contains(runCtx.Request.Message, "[Subagent completion]") {
					parentDone <- runCtx.Request.Message
				}
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "parent ack"})
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "parent ack"}}}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })

	result, err := runtime.SpawnSubagent(context.Background(), task.SubagentSpawnRequest{
		RequesterSessionKey: parentSessionKey,
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "summarize logs",
		WorkspaceDir:        filepath.Join(baseDir, "workspace"),
	})
	if err != nil {
		t.Fatalf("spawn subagent: %v", err)
	}
	if result.Status != "accepted" {
		t.Fatalf("unexpected spawn result: %+v", result)
	}

	select {
	case msg := <-parentDone:
		if !strings.Contains(msg, "[Subagent completion] summarize logs") {
			t.Fatalf("unexpected announce title: %q", msg)
		}
		if !strings.Contains(msg, "worker finished task") {
			t.Fatalf("expected frozen child result in announce: %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subagent completion announce")
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.ActiveRuns.Count(parentSessionKey) == 0 &&
			runtime.ActiveRuns.Count(result.ChildSessionKey) == 0 &&
			runtime.Queue.Depth(parentSessionKey) == 0 &&
			runtime.Queue.Depth(result.ChildSessionKey) == 0
	})
}

func TestSpawnedSubagentAnnouncementRetriesAfterTransientFailure(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	parentSessionKey := session.BuildMainSessionKey("main")
	parentDone := make(chan string, 1)
	attempts := 0

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				PersonaPrompt:   "You are a worker.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if runCtx.Request.Lane == core.LaneSubagent {
					runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "worker finished task"})
					return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "worker finished task"}}}, nil
				}
				if runCtx.Session.SessionKey == parentSessionKey && strings.Contains(runCtx.Request.Message, "[Subagent completion]") {
					attempts++
					if attempts == 1 {
						return core.AgentRunResult{}, errors.New("transient announce failure")
					}
					parentDone <- runCtx.Request.Message
				}
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "parent ack"})
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "parent ack"}}}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })

	_, err = runtime.SpawnSubagent(context.Background(), task.SubagentSpawnRequest{
		RequesterSessionKey: parentSessionKey,
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "summarize logs",
		WorkspaceDir:        filepath.Join(baseDir, "workspace"),
	})
	if err != nil {
		t.Fatalf("spawn subagent: %v", err)
	}

	select {
	case msg := <-parentDone:
		if !strings.Contains(msg, "worker finished task") {
			t.Fatalf("expected retried announce to include worker output, got %q", msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for retried subagent completion announce")
	}
	if attempts < 2 {
		t.Fatalf("expected at least 2 announce attempts, got %d", attempts)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.ActiveRuns.Count(parentSessionKey) == 0 && runtime.Queue.Depth(parentSessionKey) == 0
	})
	time.Sleep(50 * time.Millisecond)
}

func TestSpawnedSubagentAnnouncementRecoversAfterDirectDeliveryFailure(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	parentSessionKey := session.BuildMainSessionKey("main")
	parentDone := make(chan string, 1)
	attempts := 0

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				PersonaPrompt:   "You are a worker.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if runCtx.Request.Lane == core.LaneSubagent {
					runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "worker finished task"})
					return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "worker finished task"}}}, nil
				}
				if runCtx.Session.SessionKey == parentSessionKey && strings.Contains(runCtx.Request.Message, "[Subagent completion]") {
					attempts++
					if attempts == 1 && runCtx.Request.Deliver {
						return core.AgentRunResult{}, errors.New("direct delivery path failed")
					}
					parentDone <- runCtx.Request.Message
				}
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "parent ack"})
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "parent ack"}}}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })

	_, err = runtime.SpawnSubagent(context.Background(), task.SubagentSpawnRequest{
		RequesterSessionKey: parentSessionKey,
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "summarize logs",
		WorkspaceDir:        filepath.Join(baseDir, "workspace"),
		Cleanup:             "keep",
		RouteChannel:        "discord",
		RouteTo:             "chan-1",
		RouteAccountID:      "acct-1",
		RouteThreadID:       "thread-1",
	})
	if err != nil {
		t.Fatalf("spawn subagent: %v", err)
	}

	select {
	case <-parentDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for recovered completion announce")
	}
	if attempts < 2 {
		t.Fatalf("expected recovered completion delivery after direct failure, got %d attempts", attempts)
	}
	time.Sleep(50 * time.Millisecond)
}

func TestPermanentAnnouncementFailureDoesNotScheduleRetry(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	parentSessionKey := session.BuildMainSessionKey("main")
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", PersonaPrompt: "You are Kocort.", WorkspaceDir: filepath.Join(baseDir, "workspace"), DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if strings.Contains(runCtx.Request.Message, "[Subagent completion]") {
					return core.AgentRunResult{}, errors.New("runtime is not fully configured")
				}
				return core.AgentRunResult{}, nil
			},
		},
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })
	runtime.Subagents.Register(task.SubagentRunRecord{
		RunID:               "run-1",
		ChildSessionKey:     "agent:worker:subagent:one",
		RequesterSessionKey: parentSessionKey,
		Label:               "worker-1",
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
		EndedAt:             time.Now().UTC(),
		FrozenResultText:    "result",
	})
	if err := runtime.flushSubagentAnnouncements(context.Background(), parentSessionKey); err == nil {
		t.Fatal("expected flush to return announcement error")
	}
	record := runtime.Subagents.Get("run-1")
	if record == nil || record.CompletionMessageSentAt.IsZero() {
		t.Fatalf("expected permanent failure to mark completion handled, got %+v", record)
	}
	if !record.NextAnnounceAttemptAt.IsZero() {
		t.Fatalf("expected no retry scheduling for permanent failure, got %+v", record)
	}
}

func TestSessionsSpawnToolStartsSubagentAndReturnsAcceptedResult(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	parentSessionKey := session.BuildMainSessionKey("main")
	parentDone := make(chan string, 1)

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				PersonaPrompt:   "You are a worker.",
				WorkspaceDir:    filepath.Join(baseDir, "worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if runCtx.Request.Lane == core.LaneSubagent {
					runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "worker tool result"})
					return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "worker tool result"}}}, nil
				}
				if runCtx.Session.SessionKey == parentSessionKey && strings.Contains(runCtx.Request.Message, "[Subagent completion]") {
					parentDone <- runCtx.Request.Message
				}
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "parent"}}}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })

	session, err := runtime.Sessions.Resolve(context.Background(), "main", parentSessionKey, "", "", "")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	identity, err := runtime.Identities.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			AgentID:    "main",
			SessionKey: parentSessionKey,
			SpawnDepth: 0,
		},
		Session:      session,
		Identity:     identity,
		WorkspaceDir: identity.WorkspaceDir,
	}

	toolResult, err := runtime.ExecuteTool(context.Background(), runCtx, "sessions_spawn", map[string]any{
		"task":    "do the work",
		"agentId": "worker",
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if !strings.Contains(toolResult.Text, `"Status":"accepted"`) {
		t.Fatalf("unexpected tool result: %s", toolResult.Text)
	}

	select {
	case msg := <-parentDone:
		if !strings.Contains(msg, "worker tool result") {
			t.Fatalf("expected child output in parent announce, got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tool-spawned subagent announce")
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.ActiveRuns.TotalCount() == 0
	})
	time.Sleep(50 * time.Millisecond)
}

func TestSubagentsToolSteerRegistersReplacementRun(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
				ToolAllowlist:   []string{"subagents"},
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSubagentsTool()),
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "steered"})
				return core.AgentRunResult{RunID: runCtx.Request.RunID, Payloads: []core.ReplyPayload{{Text: "steered"}}}, nil
			},
		},
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })
	runtime.Subagents.Register(task.SubagentRunRecord{
		RunID:                    "run-old",
		ChildSessionKey:          "agent:worker:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		RequesterDisplayKey:      "main",
		Task:                     "worker task",
		Label:                    "worker-1",
		Model:                    "gpt-4.1-mini",
		SpawnMode:                "session",
		Cleanup:                  "keep",
		ExpectsCompletionMessage: true,
		CreatedAt:                time.Now().UTC().Add(-time.Minute),
		StartedAt:                time.Now().UTC().Add(-time.Minute),
	})

	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{AgentID: "main", SessionKey: "agent:main:main", MaxSpawnDepth: 5},
		Session: core.SessionResolution{SessionID: "sess-main", SessionKey: "agent:main:main"},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolAllowlist: []string{"subagents"},
		},
	}

	result, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{
		"action":  "steer",
		"target":  "worker-1",
		"message": "continue",
	})
	if err != nil {
		t.Fatalf("steer subagent: %v", err)
	}
	if !strings.Contains(result.Text, `"steered":true`) {
		t.Fatalf("unexpected tool result: %s", result.Text)
	}
	old := runtime.Subagents.Get("run-old")
	if old == nil || old.SuppressAnnounceReason != "steer-restart" {
		t.Fatalf("expected old run to be suppressed, got %+v", old)
	}
	replacements := runtime.Subagents.ListByRequester("agent:main:main")
	foundReplacement := false
	for _, candidate := range replacements {
		if candidate.RunID != "run-old" && candidate.ChildSessionKey == "agent:worker:subagent:one" {
			foundReplacement = true
			break
		}
	}
	if !foundReplacement {
		t.Fatalf("expected replacement run in registry, got %+v", replacements)
	}
}

func TestToolLoopBackendCanCallSessionsSpawnAndFinish(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	parentSessionKey := session.BuildMainSessionKey("main")
	parentCompletion := make(chan string, 1)

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				PersonaPrompt:   "You are a worker.",
				WorkspaceDir:    filepath.Join(baseDir, "worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })

	runtime.Backend = &backend.ToolLoopBackend{
		Runtime: runtime,
		Planner: fakeToolPlanner{
			next: func(ctx context.Context, runCtx rtypes.AgentRunContext, state core.ToolPlannerState) (core.ToolPlan, error) {
				if runCtx.Request.Lane == core.LaneSubagent {
					return core.ToolPlan{
						Final: []core.ReplyPayload{{Text: "worker loop result"}},
					}, nil
				}
				if runCtx.Session.SessionKey == parentSessionKey && strings.Contains(runCtx.Request.Message, "[Subagent completion]") {
					parentCompletion <- runCtx.Request.Message
					return core.ToolPlan{
						Final: []core.ReplyPayload{{Text: "parent handled completion"}},
					}, nil
				}
				if len(state.ToolCalls) == 0 {
					return core.ToolPlan{
						ToolCall: &core.ToolCall{
							Name: "sessions_spawn",
							Args: map[string]any{
								"task":    "delegate",
								"agentId": "worker",
							},
						},
					}, nil
				}
				return core.ToolPlan{
					Final: []core.ReplyPayload{{Text: "spawn requested"}},
				}, nil
			},
		},
	}

	result, err := runtime.Run(context.Background(), core.AgentRunRequest{
		Message:    "delegate this",
		SessionKey: parentSessionKey,
		AgentID:    "main",
	})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "spawn requested" {
		t.Fatalf("unexpected parent result: %+v", result)
	}

	select {
	case msg := <-parentCompletion:
		if !strings.Contains(msg, "worker loop result") {
			t.Fatalf("expected subagent completion payload, got %q", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for tool-loop subagent completion")
	}
	waitForCondition(t, 10*time.Second, func() bool {
		return runtime.ActiveRuns.TotalCount() == 0 &&
			runtime.Queue.Depth(parentSessionKey) == 0
	})
}

func TestSubagentToolPolicyDepthAwareBehavior(t *testing.T) {
	identity := core.AgentIdentity{
		ID: "worker",
	}

	orchestratorCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			Lane:          core.LaneSubagent,
			SpawnDepth:    1,
			MaxSpawnDepth: 2,
		},
		Session: core.SessionResolution{
			SessionKey: "agent:worker:subagent:child",
		},
		Identity: identity,
	}
	if !toolfn.IsToolAllowedByIdentity(identity, orchestratorCtx, core.ToolRegistrationMeta{}, "sessions_spawn") {
		t.Fatal("expected depth-1 orchestrator subagent to allow sessions_spawn")
	}
	if !toolfn.IsToolAllowedByIdentity(identity, orchestratorCtx, core.ToolRegistrationMeta{}, "subagents") {
		t.Fatal("expected depth-1 orchestrator subagent to allow subagents")
	}
	if toolfn.IsToolAllowedByIdentity(identity, orchestratorCtx, core.ToolRegistrationMeta{}, "gateway") {
		t.Fatal("expected subagent to deny gateway")
	}

	leafCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			Lane:          core.LaneSubagent,
			SpawnDepth:    2,
			MaxSpawnDepth: 2,
		},
		Session: core.SessionResolution{
			SessionKey: "agent:worker:subagent:leaf",
		},
		Identity: identity,
	}
	if toolfn.IsToolAllowedByIdentity(identity, leafCtx, core.ToolRegistrationMeta{}, "sessions_spawn") {
		t.Fatal("expected leaf subagent to deny sessions_spawn")
	}
	if toolfn.IsToolAllowedByIdentity(identity, leafCtx, core.ToolRegistrationMeta{}, "sessions_list") {
		t.Fatal("expected leaf subagent to deny sessions_list")
	}
	if toolfn.IsToolAllowedByIdentity(identity, leafCtx, core.ToolRegistrationMeta{}, "sessions_history") {
		t.Fatal("expected leaf subagent to deny sessions_history")
	}
	if !toolfn.IsToolAllowedByIdentity(identity, leafCtx, core.ToolRegistrationMeta{}, "subagents") {
		t.Fatal("expected leaf subagent to still allow subagents")
	}
}

func TestSubagentToolPolicyAllowlistCanReEnableDefaultDeniedTool(t *testing.T) {
	identity := core.AgentIdentity{
		ID:            "worker",
		ToolAllowlist: []string{"sessions_send"},
	}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			Lane:          core.LaneSubagent,
			SpawnDepth:    1,
			MaxSpawnDepth: 2,
		},
		Session: core.SessionResolution{
			SessionKey: "agent:worker:subagent:child",
		},
		Identity: identity,
	}
	if !toolfn.IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "sessions_send") {
		t.Fatal("expected explicit allowlist to re-enable default-denied sessions_send")
	}
}

func TestSubagentsToolListsAndKillsActiveRun(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	parentSessionKey := session.BuildMainSessionKey("main")
	started := make(chan string, 1)
	release := make(chan struct{})
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				ToolProfile:     "coding",
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSubagentsTool()),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			if runCtx.Request.Lane == core.LaneSubagent {
				started <- runCtx.Request.RunID
				<-ctx.Done()
				return core.AgentRunResult{}, ctx.Err()
			}
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "parent"}}}, nil
		}},
	}
	spawned, err := runtime.SpawnSubagent(context.Background(), task.SubagentSpawnRequest{
		RequesterSessionKey: parentSessionKey,
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "long task",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		record := runtime.Subagents.Get(spawned.RunID)
		t.Fatalf("subagent did not start; record=%+v", record)
	}
	session, _ := runtime.Sessions.Resolve(context.Background(), "main", parentSessionKey, "", "", "")
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{Runtime: runtime, Request: core.AgentRunRequest{AgentID: "main", SessionKey: parentSessionKey}, Session: session, Identity: identity}
	listResult, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("subagents list: %v", err)
	}
	if !strings.Contains(listResult.Text, spawned.RunID) {
		t.Fatalf("expected subagent run in list, got %s", listResult.Text)
	}
	killResult, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{"action": "kill", "target": spawned.RunID})
	if err != nil {
		t.Fatalf("subagents kill: %v", err)
	}
	if !strings.Contains(killResult.Text, `"status":"ok"`) {
		t.Fatalf("expected successful kill, got %s", killResult.Text)
	}
	close(release)
}

func TestSubagentsToolSendAndInfo(t *testing.T) {
	store := storeForTests(t)
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    t.TempDir(),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
				ToolAllowlist:   []string{"subagents"},
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSubagentsTool()),
		Deliverer:  &delivery.MemoryDeliverer{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "child:" + runCtx.Request.Message})
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "child:" + runCtx.Request.Message}}}, nil
			},
		},
	}
	runtime.Subagents.Register(task.SubagentRunRecord{
		RunID:               "run-sub-1",
		ChildSessionKey:     "agent:main:subagent:test-child",
		RequesterSessionKey: "agent:main:main",
		Task:                "initial task",
		Label:               "worker-1",
		SpawnDepth:          1,
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
	})
	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{AgentID: "main", SessionKey: "agent:main:main", MaxSpawnDepth: 5},
		Session: core.SessionResolution{SessionID: "sess-main", SessionKey: "agent:main:main"},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolAllowlist: []string{"subagents"},
		},
	}
	infoResult, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{
		"action": "info",
		"target": "worker-1",
	})
	if err != nil {
		t.Fatalf("subagents info: %v", err)
	}
	if !strings.Contains(infoResult.Text, "worker-1") {
		t.Fatalf("expected info result to include label, got %s", infoResult.Text)
	}
	sendResult, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{
		"action":  "send",
		"target":  "worker-1",
		"message": "continue",
	})
	if err != nil {
		t.Fatalf("subagents send: %v", err)
	}
	if !strings.Contains(sendResult.Text, "continue") {
		t.Fatalf("expected child reply in send result, got %s", sendResult.Text)
	}
}

func TestSpawnSubagentPersistsConfiguredChildModelOverride(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                  "main",
				DefaultProvider:     "openai",
				DefaultModel:        "gpt-4.1",
				SubagentAllowAgents: []string{"worker"},
			},
			"worker": {
				ID:                   "worker",
				DefaultProvider:      "openai",
				DefaultModel:         "gpt-4.1",
				SubagentModelPrimary: "openai/gpt-4.1-mini",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
	}
	result, err := rt.SpawnSubagent(context.Background(), task.SubagentSpawnRequest{
		RequesterSessionKey: session.BuildMainSessionKey("main"),
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "child",
		MaxSpawnDepth:       3,
		MaxChildren:         2,
	})
	if err != nil {
		t.Fatalf("spawn subagent: %v", err)
	}
	entry := rt.Sessions.Entry(result.ChildSessionKey)
	if entry == nil {
		t.Fatal("expected child session entry")
	}
	if entry.ProviderOverride != "openai" || entry.ModelOverride != "gpt-4.1-mini" {
		t.Fatalf("expected persisted child model override, got %+v", *entry)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !rt.ActiveRuns.IsActive(result.ChildSessionKey) {
			time.Sleep(100 * time.Millisecond)
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for spawned subagent run to finish")
}

func TestSessionsSpawnToolUsesConfiguredAgentSubagentDefaults(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID: "main",
				Subagents: config.AgentSubagentConfig{
					AllowAgents:         []string{"worker"},
					MaxSpawnDepth:       3,
					MaxChildrenPerAgent: 2,
					TimeoutSeconds:      77,
					Thinking:            "medium",
				},
				Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{StateDir: t.TempDir(), AgentID: "main"})
	if err != nil {
		t.Fatalf("new runtime from config: %v", err)
	}
	rt.Backends = nil
	rt.Backend = fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "child ok"}}}, nil
	}}
	identity, err := rt.Identities.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	session := core.SessionResolution{
		SessionKey: session.BuildMainSessionKey("main"),
		SessionID:  "sess-main",
	}
	runCtx := rtypes.AgentRunContext{
		Runtime:      rt,
		Request:      core.AgentRunRequest{SpawnDepth: 0},
		Session:      session,
		Identity:     identity,
		WorkspaceDir: t.TempDir(),
	}
	result, err := tool.NewSessionsSpawnTool().Execute(context.Background(), rtypes.ToolContext{Runtime: rt, Run: runCtx}, map[string]any{
		"task":    "do work",
		"agentId": "worker",
	})
	if err != nil {
		t.Fatalf("sessions_spawn execute: %v", err)
	}
	var payload task.SubagentSpawnResult
	if err := json.Unmarshal(result.JSON, &payload); err != nil {
		t.Fatalf("unmarshal spawn result: %v", err)
	}
	record := rt.Subagents.Get(payload.RunID)
	if record == nil {
		t.Fatalf("expected subagent record")
	}
	if record.RunTimeoutSeconds != 77 || record.SpawnDepth != 1 {
		t.Fatalf("expected configured subagent defaults in record, got %+v", record)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		next := rt.Subagents.Get(payload.RunID)
		if next != nil && !next.EndedAt.IsZero() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}
