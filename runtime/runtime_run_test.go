package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

func TestResolveActiveRunQueueActionMatchesKocortRules(t *testing.T) {
	if got := task.ResolveActiveRunQueueAction(false, false, false, core.QueueModeFollowup); got != core.ActiveRunRunNow {
		t.Fatalf("expected run-now, got %s", got)
	}
	if got := task.ResolveActiveRunQueueAction(true, true, true, core.QueueModeFollowup); got != core.ActiveRunDrop {
		t.Fatalf("expected drop, got %s", got)
	}
	if got := task.ResolveActiveRunQueueAction(true, false, true, core.QueueModeFollowup); got != core.ActiveRunEnqueueFollowup {
		t.Fatalf("expected enqueue-followup, got %s", got)
	}
	if got := task.ResolveActiveRunQueueAction(true, false, false, core.QueueModeSteer); got != core.ActiveRunEnqueueFollowup {
		t.Fatalf("expected enqueue-followup for steer, got %s", got)
	}
}

func TestReplyDispatcherPreservesOrder(t *testing.T) {
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})

	if !dispatcher.SendBlockReply(core.ReplyPayload{Text: "first"}) {
		t.Fatal("expected first block to enqueue")
	}
	if !dispatcher.SendFinalReply(core.ReplyPayload{Text: "final"}) {
		t.Fatal("expected final reply to enqueue")
	}
	dispatcher.MarkComplete()
	if err := dispatcher.WaitForIdle(context.Background()); err != nil {
		t.Fatalf("wait for idle: %v", err)
	}
	if len(deliverer.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(deliverer.Records))
	}
	if deliverer.Records[0].Payload.Text != "first" || deliverer.Records[1].Payload.Text != "final" {
		t.Fatalf("unexpected delivery order: %+v", deliverer.Records)
	}
}

type failingDeliverer struct {
	err error
}

type staticBackendResolver struct {
	backend rtypes.Backend
	kind    string
}

func (s staticBackendResolver) Resolve(provider string) (rtypes.Backend, string, error) {
	return s.backend, s.kind, nil
}

func (d *failingDeliverer) Deliver(context.Context, core.ReplyKind, core.ReplyPayload, core.DeliveryTarget) error {
	return d.err
}

func TestReplyDispatcherWaitForIdleReturnsNilOnDeliveryError(t *testing.T) {
	dispatcher := delivery.NewReplyDispatcher(&failingDeliverer{err: errors.New("deliver failed")}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	if !dispatcher.SendFinalReply(core.ReplyPayload{Text: "hello"}) {
		t.Fatal("expected final reply to enqueue")
	}
	dispatcher.MarkComplete()
	err := dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("expected nil error (delivery errors are silently discarded), got %v", err)
	}
}

func TestRuntimeEnqueuesFollowupWhenSessionAlreadyActive(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	ranMessages := make(chan string, 2)

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
				ranMessages <- runCtx.Request.Message
				if strings.Contains(runCtx.Request.Message, "first") {
					close(started)
					<-release
				}
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "done:" + runCtx.Request.Message})
				return core.AgentRunResult{
					Payloads: []core.ReplyPayload{{Text: "done:" + runCtx.Request.Message}},
				}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}
	runtime.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })

	firstDone := make(chan core.AgentRunResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		result, err := runtime.Run(context.Background(), core.AgentRunRequest{
			Message: "first",
			To:      "user-1",
			Channel: "webchat",
		})
		firstDone <- result
		firstErr <- err
	}()

	<-started
	secondResult, err := runtime.Run(context.Background(), core.AgentRunRequest{
		Message:        "second",
		To:             "user-1",
		Channel:        "webchat",
		ShouldFollowup: true,
		QueueMode:      core.QueueModeFollowup,
	})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !secondResult.Queued {
		t.Fatalf("expected second run to be queued, got %+v", secondResult)
	}
	if secondResult.QueueDepth != 1 {
		t.Fatalf("expected queue depth 1, got %+v", secondResult)
	}

	close(release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first run error: %v", err)
	}
	<-firstDone

	deadline := time.After(2 * time.Second)
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case msg := <-ranMessages:
			if strings.Contains(msg, "first") {
				seen["first"] = true
			}
			if strings.Contains(msg, "second") {
				seen["second"] = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for queued followup run, saw=%v", seen)
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("expected both runs to execute, saw=%v", seen)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		sessionKey := session.BuildDirectSessionKey("main", "webchat", "user-1")
		return runtime.ActiveRuns.Count(sessionKey) == 0 && runtime.Queue.Depth(sessionKey) == 0
	})
}

func TestRunReturnsVisiblePayloadWhenBackendFailsAfterStreamingBlockReply(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		EventHub:   gateway.NewWebchatHub(),
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Backend: backendFunc(func(_ context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			runCtx.ReplyDispatcher.SendBlockReply(core.ReplyPayload{Text: "我会帮您设置一分钟后的提醒！"})
			return core.AgentRunResult{}, &backend.BackendError{
				Reason:  backend.BackendFailureTransientHTTP,
				Message: "provider request failed: context deadline exceeded",
			}
		}),
	}
	result, err := rt.Run(context.Background(), core.AgentRunRequest{
		AgentID:    "main",
		SessionKey: "agent:main:webchat:direct:webchat-user",
		Channel:    "webchat",
		To:         "webchat-user",
		Message:    "5分钟后提醒我拿衣服！",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Payloads) != 1 || strings.TrimSpace(result.Payloads[0].Text) != "我会帮您设置一分钟后的提醒！" {
		t.Fatalf("expected visible payload preserved from partial reply, got %+v", result.Payloads)
	}
	history, err := rt.Sessions.LoadTranscript("agent:main:webchat:direct:webchat-user")
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	foundFinal := false
	for _, msg := range history {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") &&
			strings.EqualFold(strings.TrimSpace(msg.Type), "assistant_final") &&
			strings.TrimSpace(msg.Text) == "我会帮您设置一分钟后的提醒！" {
			foundFinal = true
			break
		}
	}
	if !foundFinal {
		t.Fatalf("expected transcript to preserve assistant final, got %+v", history)
	}
}

func TestRuntimeRetriesTransientBackendErrorOnce(t *testing.T) {
	store := storeForTests(t)
	calls := 0
	runtime := &Runtime{
		Sessions:   store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"}}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			calls++
			if calls == 1 {
				return core.AgentRunResult{}, &backend.BackendError{Reason: backend.BackendFailureTransientHTTP, Message: "502 bad gateway"}
			}
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
		}},
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{Message: "hello", AgentID: "main"})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if calls != 2 || len(result.Payloads) != 1 || result.Payloads[0].Text != "ok" {
		t.Fatalf("expected retry success, calls=%d result=%+v", calls, result)
	}
}

func TestChatSendAppliesRequestTimeoutToBackend(t *testing.T) {
	store := storeForTests(t)
	runtime := &Runtime{
		Sessions:   store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"}}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			select {
			case <-ctx.Done():
				return core.AgentRunResult{}, ctx.Err()
			case <-time.After(5 * time.Second):
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "late"}}}, nil
			}
		}},
	}
	start := time.Now()
	_, err := runtime.ChatSend(context.Background(), core.ChatSendRequest{
		AgentID:    "main",
		SessionKey: session.BuildDirectSessionKey("main", "webchat", "webchat-user"),
		Message:    "hello",
		Channel:    "webchat",
		To:         "webchat-user",
		TimeoutMs:  50,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected timeout quickly, elapsed=%s", elapsed)
	}
}

func TestChatSendWithoutExplicitTimeoutUsesAgentDefaultTimeout(t *testing.T) {
	store := storeForTests(t)
	seenTimeout := time.Duration(-1)
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
				TimeoutSeconds:  600,
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			seenTimeout = runCtx.Request.Timeout
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
		}},
	}
	resp, err := runtime.ChatSend(context.Background(), core.ChatSendRequest{
		AgentID:    "main",
		SessionKey: session.BuildDirectSessionKey("main", "webchat", "webchat-user"),
		Message:    "hello",
		Channel:    "webchat",
		To:         "webchat-user",
	})
	if err != nil {
		t.Fatalf("chat send: %v", err)
	}
	if len(resp.Payloads) != 1 || resp.Payloads[0].Text != "ok" {
		t.Fatalf("unexpected payloads: %+v", resp.Payloads)
	}
	if seenTimeout != 600*time.Second {
		t.Fatalf("expected agent default timeout to apply, got %s", seenTimeout)
	}
}

func TestChatHistoryCollapsesIdenticalAssistantPartialAndFinal(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-chat"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().UTC()
	if err := store.AppendTranscript(sessionKey, "sess-chat",
		core.TranscriptMessage{Role: "user", Text: "456", Timestamp: now},
		core.TranscriptMessage{Type: "assistant_partial", Role: "assistant", Text: "456！", Timestamp: now, Partial: true},
		core.TranscriptMessage{Type: "assistant_final", Role: "assistant", Text: "456！", Timestamp: now, Final: true},
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	messages, _, _, _, err := session.LoadChatHistoryPage(store, sessionKey, 0, 0)
	if err != nil {
		t.Fatalf("chat history: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected collapsed history, got %+v", messages)
	}
	if messages[1].Type != "assistant_final" || strings.TrimSpace(messages[1].Text) != "456！" {
		t.Fatalf("expected final assistant message after collapse, got %+v", messages[1])
	}
}

func TestChatHistoryDropsAssistantPartialChainWhenFinalExists(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-chat-2"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().UTC()
	if err := store.AppendTranscript(sessionKey, "sess-chat-2",
		core.TranscriptMessage{Role: "user", Text: "666", Timestamp: now},
		core.TranscriptMessage{Type: "assistant_partial", Role: "assistant", Text: "666看起来很棒！😊", Timestamp: now, Partial: true},
		core.TranscriptMessage{Type: "assistant_partial", Role: "assistant", Text: "我立刻为您处理", Timestamp: now, Partial: true},
		core.TranscriptMessage{Type: "assistant_final", Role: "assistant", Text: "666 看起来很棒！😊\n\n如果您需要继续在当前部署会话中操作，或者希望检查部署输出或执行下一步任务，请直接告诉我，我立刻为您处理", Timestamp: now, Final: true},
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	messages, _, _, _, err := session.LoadChatHistoryPage(store, sessionKey, 0, 0)
	if err != nil {
		t.Fatalf("chat history: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected collapsed history with only user + final, got %+v", messages)
	}
	if messages[1].Type != "assistant_final" || !strings.Contains(messages[1].Text, "我立刻为您处") {
		t.Fatalf("expected final assistant message after collapse, got %+v", messages[1])
	}
}

func TestChatHistoryPreservesToolCallAndToolResultEntries(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-chat-tools"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().UTC()
	if err := store.AppendTranscript(sessionKey, "sess-chat-tools",
		core.TranscriptMessage{Role: "user", Text: "帮我查一", Timestamp: now},
		core.TranscriptMessage{Type: "tool_call", Role: "assistant", ToolCallID: "call_1", ToolName: "memory_search", Args: map[string]any{"query": "docs"}, Timestamp: now},
		core.TranscriptMessage{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "memory_search", Text: "found docs", Timestamp: now},
		core.TranscriptMessage{Type: "assistant_final", Role: "assistant", Text: "done", Timestamp: now, Final: true},
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	messages, _, _, _, err := session.LoadChatHistoryPage(store, sessionKey, 0, 0)
	if err != nil {
		t.Fatalf("chat history: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("expected user + tool_call + tool_result + final, got %+v", messages)
	}
	if messages[1].Type != "tool_call" || messages[1].ToolName != "memory_search" {
		t.Fatalf("expected preserved tool_call, got %+v", messages[1])
	}
	if messages[2].Type != "tool_result" || strings.TrimSpace(messages[2].Text) != "found docs" {
		t.Fatalf("expected preserved tool_result, got %+v", messages[2])
	}
}

func TestChatHistoryUsesRecentWindowLimit(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-chat-limit"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().UTC()
	var messages []core.TranscriptMessage
	for i := 0; i < 205; i++ {
		messages = append(messages, core.TranscriptMessage{
			Role:      "user",
			Text:      fmt.Sprintf("msg-%03d", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
	}
	if err := store.AppendTranscript(sessionKey, "sess-chat-limit", messages...); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	loaded, _, _, _, err := session.LoadChatHistoryPage(store, sessionKey, 0, 0)
	if err != nil {
		t.Fatalf("chat history: %v", err)
	}
	if len(loaded) != 200 {
		t.Fatalf("expected default recent window 200, got %d", len(loaded))
	}
	if strings.TrimSpace(loaded[0].Text) != "msg-005" {
		t.Fatalf("expected oldest visible message to be msg-005, got %+v", loaded[0])
	}
}

func TestRuntimeAutoCompactsAndRetriesAfterContextOverflow(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_reset"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i := 0; i < 12; i++ {
		role := "user"
		text := fmt.Sprintf("history %d", i)
		if i%2 == 1 {
			role = "assistant"
			text = fmt.Sprintf("reply %d", i)
		}
		if err := store.AppendTranscript(sessionKey, "sess_reset", core.TranscriptMessage{
			Role:      role,
			Text:      text,
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append transcript: %v", err)
		}
	}
	var calls int
	runtime := &Runtime{
		Sessions:   store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"}}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			calls++
			switch calls {
			case 1:
				return core.AgentRunResult{}, &backend.BackendError{Reason: backend.BackendFailureContextOverflow, Message: "context limit exceeded"}
			case 2:
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "Compacted summary"}}}, nil
			default:
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "Recovered after compaction"}}}, nil
			}
		}},
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{Message: "hello", AgentID: "main", SessionKey: sessionKey})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected overflow + compact + retry, got %d calls", calls)
	}
	if len(result.Payloads) != 1 || strings.TrimSpace(result.Payloads[0].Text) != "Recovered after compaction" {
		t.Fatalf("expected recovered final payload, got %+v", result)
	}
	if entry := runtime.Sessions.Entry(sessionKey); entry == nil {
		t.Fatal("expected session entry")
	} else {
		if entry.SessionID != "sess_reset" {
			t.Fatalf("expected same session id after compaction, got %+v", entry)
		}
		if entry.CompactionCount != 1 {
			t.Fatalf("expected compaction count 1, got %+v", entry)
		}
	}
	history, err := store.LoadTranscript(sessionKey)
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	if len(history) == 0 || history[0].Type != "compaction" {
		t.Fatalf("expected compaction entry after overflow, got %+v", history)
	}
}

func TestRuntimeRunsPreCompactionMemoryFlushOnceBeforeAutoCompaction(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_flush"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i := 0; i < 12; i++ {
		role := "user"
		text := fmt.Sprintf("history %d", i)
		if i%2 == 1 {
			role = "assistant"
			text = fmt.Sprintf("reply %d", i)
		}
		if err := store.AppendTranscript(sessionKey, "sess_flush", core.TranscriptMessage{
			Role:      role,
			Text:      text,
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append transcript: %v", err)
		}
	}
	workspaceDir := t.TempDir()
	var (
		calls            int
		maintenanceCalls int
	)
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                             "main",
				DefaultProvider:                "openai",
				DefaultModel:                   "gpt-4.1",
				WorkspaceDir:                   workspaceDir,
				MemoryEnabled:                  true,
				MemoryFlushEnabled:             true,
				MemoryFlushSoftThresholdTokens: 4000,
				CompactionReserveTokensFloor:   20000,
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			calls++
			if runCtx.Request.IsMaintenance {
				maintenanceCalls++
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "NO_REPLY"}}}, nil
			}
			switch calls {
			case 1:
				return core.AgentRunResult{}, &backend.BackendError{Reason: backend.BackendFailureContextOverflow, Message: "context limit exceeded"}
			case 3:
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "Compacted summary"}}}, nil
			default:
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "Recovered after compaction"}}}, nil
			}
		}},
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{Message: "hello", AgentID: "main", SessionKey: sessionKey})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if calls != 4 {
		t.Fatalf("expected overflow + maintenance flush + compact + retry, got %d calls", calls)
	}
	if maintenanceCalls != 1 {
		t.Fatalf("expected one maintenance flush call, got %d", maintenanceCalls)
	}
	if len(result.Payloads) != 1 || strings.TrimSpace(result.Payloads[0].Text) != "Recovered after compaction" {
		t.Fatalf("expected recovered final payload, got %+v", result)
	}
	entry := runtime.Sessions.Entry(sessionKey)
	if entry == nil {
		t.Fatal("expected session entry")
	}
	if entry.MemoryFlushAt.IsZero() {
		t.Fatalf("expected memory flush metadata, got %+v", entry)
	}
	if entry.MemoryFlushCompactionCount != 0 {
		t.Fatalf("expected memory flush to record pre-compaction cycle 0, got %+v", entry)
	}
}

func TestRuntimeRunsPreemptiveMemoryFlushBeforeThresholdOverflow(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{
		SessionID:     "sess_preemptive",
		ContextTokens: 760,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	workspaceDir := t.TempDir()
	var (
		calls            int
		maintenanceCalls int
	)
	runtime := &Runtime{
		Config: config.AppConfig{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderConfig{
					"openai": {Models: []config.ProviderModelConfig{{ID: "gpt-4.1", ContextWindow: 1000}}},
				},
			},
		},
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                             "main",
				DefaultProvider:                "openai",
				DefaultModel:                   "gpt-4.1",
				WorkspaceDir:                   workspaceDir,
				MemoryEnabled:                  true,
				MemoryFlushEnabled:             true,
				MemoryFlushSoftThresholdTokens: 100,
				CompactionReserveTokensFloor:   200,
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			calls++
			if runCtx.Request.IsMaintenance {
				maintenanceCalls++
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "NO_REPLY"}}}, nil
			}
			return core.AgentRunResult{
				Payloads: []core.ReplyPayload{{Text: "ok"}},
				Usage:    map[string]any{"prompt_tokens": 780, "completion_tokens": 20, "total_tokens": 800},
			}, nil
		}},
	}

	result, err := runtime.Run(context.Background(), core.AgentRunRequest{Message: "hello", AgentID: "main", SessionKey: sessionKey})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected maintenance flush + normal turn, got %d calls", calls)
	}
	if maintenanceCalls != 1 {
		t.Fatalf("expected one maintenance flush call, got %d", maintenanceCalls)
	}
	if len(result.Payloads) != 1 || strings.TrimSpace(result.Payloads[0].Text) != "ok" {
		t.Fatalf("unexpected payloads: %+v", result.Payloads)
	}
	entry := runtime.Sessions.Entry(sessionKey)
	if entry == nil {
		t.Fatal("expected session entry")
	}
	if entry.MemoryFlushAt.IsZero() {
		t.Fatalf("expected preemptive memory flush metadata, got %+v", entry)
	}
	if entry.ContextTokens != 800 {
		t.Fatalf("expected context tokens saved from usage, got %+v", entry)
	}
}

func TestRuntimeSkipsPreemptiveMemoryFlushBelowThreshold(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{
		SessionID:     "sess_preemptive",
		ContextTokens: 200,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	workspaceDir := t.TempDir()
	var (
		calls            int
		maintenanceCalls int
	)
	runtime := &Runtime{
		Config: config.AppConfig{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderConfig{
					"openai": {Models: []config.ProviderModelConfig{{ID: "gpt-4.1", ContextWindow: 1000}}},
				},
			},
		},
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                             "main",
				DefaultProvider:                "openai",
				DefaultModel:                   "gpt-4.1",
				WorkspaceDir:                   workspaceDir,
				MemoryEnabled:                  true,
				MemoryFlushEnabled:             true,
				MemoryFlushSoftThresholdTokens: 100,
				CompactionReserveTokensFloor:   200,
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			calls++
			if runCtx.Request.IsMaintenance {
				maintenanceCalls++
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "NO_REPLY"}}}, nil
			}
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
		}},
	}

	result, err := runtime.Run(context.Background(), core.AgentRunRequest{Message: "hello", AgentID: "main", SessionKey: sessionKey})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected only normal turn, got %d calls", calls)
	}
	if maintenanceCalls != 0 {
		t.Fatalf("expected no maintenance flush call, got %d", maintenanceCalls)
	}
	if len(result.Payloads) != 1 || strings.TrimSpace(result.Payloads[0].Text) != "ok" {
		t.Fatalf("unexpected payloads: %+v", result.Payloads)
	}
}

func TestRuntimeSkipsPreCompactionMemoryFlushWhenAlreadyFlushedThisCycle(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{
		SessionID:                  "sess_flush",
		MemoryFlushAt:              time.Now().UTC(),
		MemoryFlushCompactionCount: 0,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i := 0; i < 12; i++ {
		if err := store.AppendTranscript(sessionKey, "sess_flush", core.TranscriptMessage{
			Role:      "user",
			Text:      fmt.Sprintf("msg %d", i),
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append transcript: %v", err)
		}
	}
	workspaceDir := t.TempDir()
	var (
		calls            int
		maintenanceCalls int
	)
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                             "main",
				DefaultProvider:                "openai",
				DefaultModel:                   "gpt-4.1",
				WorkspaceDir:                   workspaceDir,
				MemoryEnabled:                  true,
				MemoryFlushEnabled:             true,
				MemoryFlushSoftThresholdTokens: 4000,
				CompactionReserveTokensFloor:   20000,
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			calls++
			if runCtx.Request.IsMaintenance {
				maintenanceCalls++
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "NO_REPLY"}}}, nil
			}
			switch calls {
			case 1:
				return core.AgentRunResult{}, &backend.BackendError{Reason: backend.BackendFailureContextOverflow, Message: "context limit exceeded"}
			case 2:
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "Compacted summary"}}}, nil
			default:
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "Recovered after compaction"}}}, nil
			}
		}},
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{Message: "hello", AgentID: "main", SessionKey: sessionKey})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if maintenanceCalls != 0 {
		t.Fatalf("expected no maintenance flush call, got %d", maintenanceCalls)
	}
	if calls < 2 {
		t.Fatalf("expected at least overflow + continued handling, got %d calls", calls)
	}
	if len(result.Payloads) != 1 || strings.TrimSpace(result.Payloads[0].Text) == "" {
		t.Fatalf("expected recovered final payload, got %+v", result)
	}
	entry := runtime.Sessions.Entry(sessionKey)
	if entry == nil {
		t.Fatal("expected session entry")
	}
	if !entry.MemoryFlushAt.IsZero() && entry.MemoryFlushCompactionCount != 0 {
		t.Fatalf("expected existing memory flush metadata to remain on cycle 0, got %+v", entry)
	}
}

func TestRuntimeFallsBackToResetWhenOverflowCompactionFails(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_reset"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i := 0; i < 12; i++ {
		if err := store.AppendTranscript(sessionKey, "sess_reset", core.TranscriptMessage{
			Role:      "user",
			Text:      fmt.Sprintf("msg %d", i),
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append transcript: %v", err)
		}
	}
	runtime := &Runtime{
		Sessions:   store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"}}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, &backend.BackendError{Reason: backend.BackendFailureContextOverflow, Message: "context limit exceeded"}
		}},
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{Message: "hello", AgentID: "main", SessionKey: sessionKey})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if !strings.Contains(result.Payloads[0].Text, "Context limit exceeded") {
		t.Fatalf("expected reset warning, got %+v", result)
	}
	if entry := runtime.Sessions.Entry(sessionKey); entry != nil {
		if entry.SessionID == "sess_reset" || entry.ResetReason != "overflow" {
			t.Fatalf("expected rolled session after overflow reset fallback, got %+v", entry)
		}
	}
}

func TestRuntimeHandlesNewAndResetCommands(t *testing.T) {
	store := storeForTests(t)
	var calls int
	runtime := &Runtime{
		Config:   config.AppConfig{Session: config.SessionConfig{ResetTriggers: []string{"/new", "/reset"}}},
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			calls++
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
		}},
	}
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_before"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Message: "/reset"})
	if err != nil {
		t.Fatalf("run reset: %v", err)
	}
	if calls != 0 || !strings.Contains(result.Payloads[0].Text, "Session reset") {
		t.Fatalf("expected command-only reset, calls=%d result=%+v", calls, result)
	}
	entry := store.Entry(sessionKey)
	if entry == nil || entry.SessionID == "sess_before" {
		t.Fatalf("expected reset session entry, got %+v", entry)
	}
	oldSessionID := entry.SessionID
	result, err = runtime.Run(context.Background(), core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Message: "/new say hi"})
	if err != nil {
		t.Fatalf("run new with remainder: %v", err)
	}
	if calls != 1 || len(result.Payloads) != 1 || result.Payloads[0].Text != "ok" {
		t.Fatalf("expected backend run after /new remainder, calls=%d result=%+v", calls, result)
	}
	entry = store.Entry(sessionKey)
	if entry == nil || entry.SessionID == oldSessionID || entry.ResetReason != "new" {
		t.Fatalf("expected new session rollover, got %+v", entry)
	}
}

func TestRuntimeArchivesSessionMemoryOnReset(t *testing.T) {
	store := storeForTests(t)
	workspaceDir := t.TempDir()
	runtime := &Runtime{
		Config:   config.AppConfig{Session: config.SessionConfig{ResetTriggers: []string{"/reset"}}},
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
				WorkspaceDir:    workspaceDir,
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
		}},
	}
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_before"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().UTC()
	if err := store.AppendTranscript(sessionKey, "sess_before",
		core.TranscriptMessage{Role: "user", Text: "remember atlas status", Timestamp: now},
		core.TranscriptMessage{Role: "assistant", Text: "atlas is in rollout", Timestamp: now.Add(time.Second)},
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	result, err := runtime.Run(context.Background(), core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Message: "/reset"})
	if err != nil {
		t.Fatalf("run reset: %v", err)
	}
	if len(result.Payloads) != 1 || !strings.Contains(result.Payloads[0].Text, "Session reset") {
		t.Fatalf("unexpected reset result: %+v", result)
	}

	matches, err := filepath.Glob(filepath.Join(workspaceDir, "memory", "*-reset-*.md"))
	if err != nil {
		t.Fatalf("glob memory archive: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one reset archive, got %v", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "remember atlas status") || !strings.Contains(text, "atlas is in rollout") {
		t.Fatalf("expected archived conversation in memory file, got:\n%s", text)
	}
}

func TestRuntimeResetUsesACPResetPathWhenSessionIsACPBound(t *testing.T) {
	store := storeForTests(t)
	runtime := &Runtime{
		Config:   config.AppConfig{Session: config.SessionConfig{ResetTriggers: []string{"/reset"}}},
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backends:   staticBackendResolver{backend: &backend.ACPBackend{}, kind: "acp"},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
		}},
	}
	sessionKey := "agent:main:acp:acp-live:test"
	if err := store.Upsert(sessionKey, core.SessionEntry{
		SessionID: "sess-before",
		ACP:       &core.AcpSessionMeta{Backend: "acp-live", Mode: core.AcpSessionModePersistent},
	}); err != nil {
		t.Fatalf("upsert ACP session: %v", err)
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey, Message: "/reset"})
	if err != nil {
		t.Fatalf("run ACP reset: %v", err)
	}
	if len(result.Payloads) != 1 || !strings.Contains(result.Payloads[0].Text, "Session reset") {
		t.Fatalf("unexpected ACP reset result: %+v", result)
	}
	entry := store.Entry(sessionKey)
	if entry == nil || entry.SessionID == "sess-before" {
		t.Fatalf("expected ACP reset rollover on same key, got %+v", entry)
	}
}

func TestRuntimeCompactKeepsSessionAndWritesCompactionEntry(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_compact"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 12; i++ {
		role := "user"
		text := fmt.Sprintf("user message %d", i)
		if i%2 == 1 {
			role = "assistant"
			text = fmt.Sprintf("assistant reply %d", i)
		}
		if err := store.AppendTranscript(sessionKey, "sess_compact", core.TranscriptMessage{
			Role:      role,
			Text:      text,
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append transcript %d: %v", i, err)
		}
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "Compacted summary"}}}, nil
		}},
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{
		AgentID:    "main",
		SessionKey: sessionKey,
		Message:    "/compact keep key commitments",
	})
	if err != nil {
		t.Fatalf("compact run: %v", err)
	}
	if len(result.Payloads) != 1 || !strings.Contains(result.Payloads[0].Text, "Session compacted") {
		t.Fatalf("unexpected compact result: %+v", result)
	}
	entry := store.Entry(sessionKey)
	if entry == nil {
		t.Fatal("expected session entry")
	}
	if entry.SessionID != "sess_compact" {
		t.Fatalf("expected same session id, got %q", entry.SessionID)
	}
	if entry.CompactionCount != 1 {
		t.Fatalf("expected compaction count 1, got %+v", entry)
	}
	history, err := store.LoadTranscript(sessionKey)
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	if len(history) == 0 || history[0].Type != "compaction" {
		t.Fatalf("expected leading compaction entry, got %+v", history)
	}
	if strings.TrimSpace(history[0].Text) != "Compacted summary" {
		t.Fatalf("unexpected compaction summary: %+v", history[0])
	}
	if strings.TrimSpace(history[0].Summary) != "Compacted summary" {
		t.Fatalf("expected persisted compaction summary field, got %+v", history[0])
	}
	if strings.TrimSpace(history[0].Instructions) != "keep key commitments" {
		t.Fatalf("expected persisted compact instructions, got %+v", history[0])
	}
	if len(history) != 9 {
		t.Fatalf("expected compaction entry + 8 kept messages, got %d entries", len(history))
	}
	if strings.TrimSpace(history[0].FirstKeptEntryID) == "" {
		t.Fatalf("expected firstKeptEntryId, got %+v", history[0])
	}
	if history[1].ID != history[0].FirstKeptEntryID {
		t.Fatalf("expected firstKeptEntryId %q to match first kept entry %q", history[0].FirstKeptEntryID, history[1].ID)
	}
}

func TestRuntimeCompactNoOpsWhenHistoryIsTooShort(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_short"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.AppendTranscript(sessionKey, "sess_short",
		core.TranscriptMessage{Role: "user", Text: "hello", Timestamp: time.Now().UTC()},
		core.TranscriptMessage{Role: "assistant", Text: "world", Timestamp: time.Now().UTC()},
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	var backendCalls int
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			backendCalls++
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "unused"}}}, nil
		}},
	}
	result, err := runtime.Run(context.Background(), core.AgentRunRequest{
		AgentID:    "main",
		SessionKey: sessionKey,
		Message:    "/compact",
	})
	if err != nil {
		t.Fatalf("compact run: %v", err)
	}
	if backendCalls != 0 {
		t.Fatalf("expected no backend call for short history, got %d", backendCalls)
	}
	if len(result.Payloads) != 1 || !strings.Contains(result.Payloads[0].Text, "Session compacted") {
		t.Fatalf("unexpected compact result: %+v", result)
	}
	history, err := store.LoadTranscript(sessionKey)
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected untouched history, got %d entries", len(history))
	}
}

func TestRuntimeRunPersistsUserBeforeToolTranscriptEntries(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "user")
	deliverer := &delivery.MemoryDeliverer{}
	registry := tool.NewToolRegistry(&stubTool{
		name: "echo_tool",
		execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
			return core.ToolResult{Text: "tool-ok"}, nil
		},
	})
	runtime := &Runtime{
		Sessions:   store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{"main": {ID: "main", DefaultProvider: "test", DefaultModel: "model"}}),
		Memory: memoryProviderFunc(func(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, message string) ([]core.MemoryHit, error) {
			return nil, nil
		}),
		Deliverer:  deliverer,
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      registry,
	}
	backend := &backend.ToolLoopBackend{
		Runtime: runtime,
		Planner: fakeToolPlanner{
			next: func(ctx context.Context, runCtx rtypes.AgentRunContext, state core.ToolPlannerState) (core.ToolPlan, error) {
				if len(state.ToolCalls) == 0 {
					return core.ToolPlan{ToolCall: &core.ToolCall{Name: "echo_tool", Args: map[string]any{"message": "hi"}}}, nil
				}
				return core.ToolPlan{Final: []core.ReplyPayload{{Text: "done"}}}, nil
			},
		},
	}
	runtime.Backend = backend

	if _, err := runtime.Run(context.Background(), core.AgentRunRequest{
		AgentID: "main",
		Message: "please use the tool",
		Channel: "webchat",
		To:      "user",
	}); err != nil {
		t.Fatalf("run: %v", err)
	}

	history, err := store.LoadTranscript(sessionKey)
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	if len(history) < 4 {
		t.Fatalf("expected user, tool call, tool result, assistant final; got %+v", history)
	}
	if history[0].Role != "user" || !strings.Contains(history[0].Text, "please use the tool") {
		t.Fatalf("expected first transcript entry to be the user message, got %+v", history[0])
	}
	if history[1].Type != "tool_call" || history[1].ToolName != "echo_tool" {
		t.Fatalf("expected tool call after user message, got %+v", history[1])
	}
	if history[2].Type != "tool_result" || history[2].Text != "tool-ok" {
		t.Fatalf("expected tool result after tool call, got %+v", history[2])
	}
	if history[len(history)-1].Type != "assistant_final" || history[len(history)-1].Text != "done" {
		t.Fatalf("expected final assistant reply at end, got %+v", history[len(history)-1])
	}
}

func TestRouterDelivererMirrorsWebchatFinalReplyToTranscript(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "mirror-user")
	session, err := store.Resolve(context.Background(), "main", sessionKey, "webchat", "mirror-user", "")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if err := store.AppendTranscript(sessionKey, session.SessionID, core.TranscriptMessage{
		Role:      "user",
		Text:      "hello",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append user transcript: %v", err)
	}

	webchat := gateway.NewWebchatHub()
	deliverer := &delivery.RouterDeliverer{
		Events:   webchat,
		Sessions: store,
	}
	target := core.DeliveryTarget{
		SessionKey: sessionKey,
		Channel:    "webchat",
		To:         "mirror-user",
	}
	if err := deliverer.Deliver(context.Background(), core.ReplyKindFinal, core.ReplyPayload{Text: "webchat reminder"}, target); err != nil {
		t.Fatalf("deliver webchat final: %v", err)
	}

	history, err := store.LoadTranscript(sessionKey)
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	if !containsTranscriptFinal(history, "webchat reminder") {
		t.Fatalf("expected mirrored assistant final in transcript, got %+v", history)
	}
	records := webchat.History(sessionKey)
	if len(records) != 1 || records[0].Payload.Text != "webchat reminder" {
		t.Fatalf("expected webchat hub record, got %+v", records)
	}
}

func TestFollowupQueueCollectsSameRouteItems(t *testing.T) {
	queue := task.NewFollowupQueue()
	queue.SetSleep(func(context.Context, time.Duration) error { return nil })

	settings := task.QueueSettings{Mode: core.QueueModeCollect, Debounce: time.Millisecond, Cap: 20, DropPolicy: core.QueueDropSummarize}
	if !queue.Enqueue(task.FollowupRun{
		QueueKey:           "agent:main:main",
		Prompt:             "first",
		Request:            core.AgentRunRequest{Message: "first"},
		OriginatingChannel: "webchat",
		OriginatingTo:      "u1",
	}, settings, core.QueueDedupeMessageID) {
		t.Fatal("expected first enqueue")
	}
	if !queue.Enqueue(task.FollowupRun{
		QueueKey:           "agent:main:main",
		Prompt:             "second",
		Request:            core.AgentRunRequest{Message: "second"},
		OriginatingChannel: "webchat",
		OriginatingTo:      "u1",
	}, settings, core.QueueDedupeMessageID) {
		t.Fatal("expected second enqueue")
	}

	var (
		mu   sync.Mutex
		runs []task.FollowupRun
		wg   sync.WaitGroup
	)
	wg.Add(1)
	queue.ScheduleDrain(context.Background(), "agent:main:main", func(run task.FollowupRun) error {
		mu.Lock()
		runs = append(runs, run)
		mu.Unlock()
		wg.Done()
		return nil
	})
	wg.Wait()

	if len(runs) != 1 {
		t.Fatalf("expected one collected run, got %d", len(runs))
	}
	if got := runs[0].Request.Message; got == "first" || got == "second" {
		t.Fatalf("expected collected prompt, got %q", got)
	}
	if want := "[Queued messages while agent was busy]"; runs[0].Prompt[:len(want)] != want {
		t.Fatalf("unexpected collect prompt: %q", runs[0].Prompt)
	}
}

func TestFollowupQueueSummarizesDroppedItems(t *testing.T) {
	queue := task.NewFollowupQueue()
	queue.SetSleep(func(context.Context, time.Duration) error { return nil })
	settings := task.QueueSettings{Mode: core.QueueModeFollowup, Debounce: time.Millisecond, Cap: 1, DropPolicy: core.QueueDropSummarize}

	queue.Enqueue(task.FollowupRun{QueueKey: "k", Prompt: "first dropped", SummaryLine: "first dropped", Request: core.AgentRunRequest{Message: "first dropped"}}, settings, core.QueueDedupeMessageID)
	queue.Enqueue(task.FollowupRun{QueueKey: "k", Prompt: "second kept", SummaryLine: "second kept", Request: core.AgentRunRequest{Message: "second kept"}}, settings, core.QueueDedupeMessageID)

	var got []task.FollowupRun
	var wg sync.WaitGroup
	wg.Add(1)
	queue.ScheduleDrain(context.Background(), "k", func(run task.FollowupRun) error {
		got = append(got, run)
		wg.Done()
		return nil
	})
	wg.Wait()

	if len(got) != 1 {
		t.Fatalf("expected one summary callback, got %d", len(got))
	}
	if got[0].Prompt == "second kept" || got[0].Prompt == "" {
		t.Fatalf("expected summary prompt first, got %q", got[0].Prompt)
	}
	if got[0].Request.Message != got[0].Prompt {
		t.Fatalf("expected request message to be rewritten to summary prompt, got %+v", got[0])
	}
}

func TestBlockReplyPipelineCoalescesAndFlushesMedia(t *testing.T) {
	var (
		mu       sync.Mutex
		payloads []core.ReplyPayload
	)
	pipeline := delivery.NewBlockReplyPipeline(func(_ context.Context, payload core.ReplyPayload) error {
		mu.Lock()
		defer mu.Unlock()
		payloads = append(payloads, payload)
		return nil
	}, time.Second, &delivery.BlockStreamingCoalescing{
		MinChars: 2,
		MaxChars: 20,
		Idle:     time.Hour,
		Joiner:   " ",
	}, nil)

	pipeline.Enqueue(core.ReplyPayload{Text: "hello"})
	pipeline.Enqueue(core.ReplyPayload{Text: "world"})
	if err := pipeline.Flush(true); err != nil {
		t.Fatalf("flush: %v", err)
	}

	pipeline.Enqueue(core.ReplyPayload{Text: "photo soon"})
	pipeline.Enqueue(core.ReplyPayload{MediaURL: "https://example.com/a.png"})
	if err := pipeline.Flush(true); err != nil {
		t.Fatalf("flush after media: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 3 {
		t.Fatalf("expected coalesced text, buffered text, then media payload, got %d", len(payloads))
	}
	if payloads[0].Text != "hello world" {
		t.Fatalf("unexpected coalesced payload: %+v", payloads[0])
	}
	if payloads[1].Text != "photo soon" {
		t.Fatalf("expected pending text flush before media, got %+v", payloads[1])
	}
	if payloads[2].MediaURL == "" {
		t.Fatalf("expected media payload last, got %+v", payloads[2])
	}
}
