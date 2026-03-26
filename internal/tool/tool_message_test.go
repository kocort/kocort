package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

type messageRuntimeStub struct {
	webRuntimeStub
	lastKind    core.ReplyKind
	lastPayload core.ReplyPayload
	lastTarget  core.DeliveryTarget
	deliveries  []core.DeliveryRecord
	deliverFn   func(context.Context, core.ReplyKind, core.ReplyPayload, core.DeliveryTarget) error
}

func (s *messageRuntimeStub) DeliverMessage(ctx context.Context, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) error {
	s.lastKind = kind
	s.lastPayload = payload
	s.lastTarget = target
	s.deliveries = append(s.deliveries, core.DeliveryRecord{Kind: kind, Payload: payload, Target: target})
	if s.deliverFn != nil {
		return s.deliverFn(ctx, kind, payload, target)
	}
	return nil
}

func TestMessageTool_SameTargetSendsOnceAndKeepsTranscriptMirror(t *testing.T) {
	tool := NewMessageTool()
	rt := &messageRuntimeStub{}
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: rt,
		Run: AgentRunContext{
			Session: core.SessionResolution{SessionKey: "agent:main:main"},
			Request: core.AgentRunRequest{
				Channel:   "feishu",
				To:        "chat-1",
				AccountID: "bot-1",
				ThreadID:  "thread-9",
			},
		},
	}, map[string]any{
		"message":   "hello atlas",
		"replyToId": "msg-1",
		"channelData": map[string]any{
			"mode": "thread",
		},
	})
	if err != nil {
		t.Fatalf("message send: %v", err)
	}
	if rt.lastKind != core.ReplyKindFinal {
		t.Fatalf("unexpected reply kind: %s", rt.lastKind)
	}
	if rt.lastTarget.Channel != "feishu" || rt.lastTarget.To != "chat-1" || rt.lastTarget.AccountID != "bot-1" || rt.lastTarget.ThreadID != "thread-9" {
		t.Fatalf("unexpected target: %+v", rt.lastTarget)
	}
	if rt.lastPayload.Text != "hello atlas" || rt.lastPayload.ReplyToID != "msg-1" {
		t.Fatalf("unexpected payload: %+v", rt.lastPayload)
	}
	if len(rt.deliveries) != 1 || rt.deliveries[0].Target.SkipTranscriptMirror {
		t.Fatalf("unexpected deliveries: %+v", rt.deliveries)
	}
	if got := rt.lastPayload.ChannelData["mode"]; got != "thread" {
		t.Fatalf("unexpected channel data: %+v", rt.lastPayload.ChannelData)
	}
	if !strings.Contains(result.Text, `"status":"ok"`) {
		t.Fatalf("unexpected result: %s", result.Text)
	}
	if result.MediaURL != "" || len(result.MediaURLs) > 0 {
		t.Fatalf("tool result should not re-export media: %+v", result)
	}
}

func TestMessageTool_SendFile(t *testing.T) {
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(filePath, []byte("atlas"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	tool := NewMessageTool()
	rt := &messageRuntimeStub{}
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: rt,
		Run: AgentRunContext{
			Session:      core.SessionResolution{SessionKey: "agent:main:main"},
			Request:      core.AgentRunRequest{Channel: "webchat", To: "user-1"},
			WorkspaceDir: workspace,
		},
	}, map[string]any{
		"path": "report.txt",
	})
	if err != nil {
		t.Fatalf("message send file: %v", err)
	}
	if rt.lastPayload.MediaURL != fileURI(filePath) {
		t.Fatalf("unexpected media url: %+v", rt.lastPayload)
	}
	if len(rt.deliveries) != 1 {
		t.Fatalf("unexpected delivery count: %+v", rt.deliveries)
	}
	if rt.lastTarget.Channel != "webchat" || rt.lastTarget.To != "user-1" {
		t.Fatalf("unexpected target: %+v", rt.lastTarget)
	}
	if rt.lastTarget.SkipTranscriptMirror {
		t.Fatalf("webchat self-delivery should keep transcript mirror: %+v", rt.lastTarget)
	}
	if result.MediaURL != "" || len(result.MediaURLs) > 0 {
		t.Fatalf("tool result should not re-export media: %+v", result)
	}
}

func TestMessageTool_WebchatCrossTargetMirrorsBackToCurrentSession(t *testing.T) {
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(filePath, []byte("atlas"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	tool := NewMessageTool()
	rt := &messageRuntimeStub{}
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: rt,
		Run: AgentRunContext{
			Session:      core.SessionResolution{SessionKey: "agent:main:webchat:direct:user-1"},
			Request:      core.AgentRunRequest{Channel: "webchat", To: "user-1"},
			WorkspaceDir: workspace,
		},
	}, map[string]any{
		"path":    "report.txt",
		"channel": "feishu",
		"to":      "chat-9",
	})
	if err != nil {
		t.Fatalf("message send cross target: %v", err)
	}
	if len(rt.deliveries) != 2 {
		t.Fatalf("unexpected delivery count: %+v", rt.deliveries)
	}
	first := rt.deliveries[0]
	if first.Target.Channel != "feishu" || first.Target.To != "chat-9" || !first.Target.SkipTranscriptMirror {
		t.Fatalf("unexpected direct target: %+v", first.Target)
	}
	if first.Payload.MediaURL != fileURI(filePath) {
		t.Fatalf("unexpected direct payload: %+v", first.Payload)
	}
	second := rt.deliveries[1]
	if second.Target.Channel != "webchat" || second.Target.To != "user-1" || second.Target.SkipTranscriptMirror {
		t.Fatalf("unexpected webchat mirror target: %+v", second.Target)
	}
	if second.Payload.MediaURL != fileURI(filePath) {
		t.Fatalf("unexpected mirror payload: %+v", second.Payload)
	}
	if result.MediaURL != "" || len(result.MediaURLs) > 0 {
		t.Fatalf("tool result should not re-export media: %+v", result)
	}
}

func TestMessageTool_NonWebchatCrossTargetSendsDirectlyOnce(t *testing.T) {
	tool := NewMessageTool()
	rt := &messageRuntimeStub{}
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: rt,
		Run: AgentRunContext{
			Session: core.SessionResolution{SessionKey: "agent:main:feishu:direct:chat-1"},
			Request: core.AgentRunRequest{
				Channel:   "feishu",
				To:        "chat-1",
				AccountID: "bot-1",
				ThreadID:  "thread-1",
			},
		},
	}, map[string]any{
		"message":   "forward this",
		"channel":   "dingtalk",
		"to":        "room-9",
		"accountId": "bot-2",
		"threadId":  "thread-9",
	})
	if err != nil {
		t.Fatalf("message send cross target: %v", err)
	}
	if len(rt.deliveries) != 1 {
		t.Fatalf("unexpected delivery count: %+v", rt.deliveries)
	}
	delivery := rt.deliveries[0]
	if delivery.Target.Channel != "dingtalk" || delivery.Target.To != "room-9" || delivery.Target.AccountID != "bot-2" || delivery.Target.ThreadID != "thread-9" {
		t.Fatalf("unexpected target: %+v", delivery.Target)
	}
	if delivery.Target.SkipTranscriptMirror {
		t.Fatalf("non-webchat cross-target delivery should keep transcript mirror: %+v", delivery.Target)
	}
	if delivery.Payload.Text != "forward this" {
		t.Fatalf("unexpected payload: %+v", delivery.Payload)
	}
	if !strings.Contains(result.Text, `"status":"ok"`) {
		t.Fatalf("unexpected result: %s", result.Text)
	}
	if result.MediaURL != "" || len(result.MediaURLs) > 0 {
		t.Fatalf("tool result should not re-export media: %+v", result)
	}
}
