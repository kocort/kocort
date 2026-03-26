package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

type ParentStreamRelay struct {
	deliverer core.Deliverer
	target    core.DeliveryTarget
	logPath   string
	agentID   string
	childKey  string
}

func StartParentStreamRelay(deliverer core.Deliverer, req SessionSpawnRequest, result SessionSpawnResult) *ParentStreamRelay {
	if deliverer == nil || strings.TrimSpace(req.StreamTo) != "parent" {
		return nil
	}
	target := core.DeliveryTarget{
		SessionKey: req.RequesterSessionKey,
		Channel:    req.RouteChannel,
		To:         req.RouteTo,
		AccountID:  req.RouteAccountID,
		ThreadID:   req.RouteThreadID,
	}
	if strings.TrimSpace(target.Channel) == "" || strings.TrimSpace(target.To) == "" {
		return nil
	}
	logPath := ResolveParentStreamLogPath(result.ChildSessionKey)
	relay := &ParentStreamRelay{
		deliverer: deliverer,
		target:    target,
		logPath:   logPath,
		agentID:   strings.TrimSpace(result.AgentID),
		childKey:  strings.TrimSpace(result.ChildSessionKey),
	}
	relay.appendLog("started", fmt.Sprintf("started child=%s agent=%s", relay.childKey, relay.agentID))
	_ = relay.deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
		Text: fmt.Sprintf("[ACP stream started] %s -> %s", relay.agentID, relay.childKey),
	}, relay.target)
	return relay
}

func (r *ParentStreamRelay) NotifyCompleted(result core.AgentRunResult, runErr error) {
	if r == nil {
		return
	}
	if runErr != nil {
		r.appendLog("error", runErr.Error())
		_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
			Text:    fmt.Sprintf("[ACP stream error] %s", runErr.Error()),
			IsError: true,
		}, r.target)
		return
	}
	text := summarizeRelayPayloads(result.Payloads)
	if text == "" {
		text = "completed"
	}
	for _, payload := range result.Payloads {
		if strings.TrimSpace(payload.Text) == "" && strings.TrimSpace(payload.MediaURL) == "" && len(payload.MediaURLs) == 0 {
			continue
		}
		_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, payload, r.target)
	}
	r.appendLog("completed", text)
}

func ResolveParentStreamLogPath(childSessionKey string) string {
	base := filepath.Join(".kocort", "acp-streams")
	token := strings.NewReplacer(":", "_", "/", "_").Replace(strings.TrimSpace(childSessionKey))
	if token == "" {
		token = fmt.Sprintf("stream_%d", time.Now().UTC().UnixNano())
	}
	return filepath.Join(base, token+".log")
}

func (r *ParentStreamRelay) appendLog(kind string, text string) {
	if r == nil || strings.TrimSpace(r.logPath) == "" {
		return
	}
	dir := filepath.Dir(r.logPath)
	_ = os.MkdirAll(dir, 0o755)
	line := fmt.Sprintf("%s\t%s\t%s\n", time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(kind), strings.TrimSpace(text))
	f, err := os.OpenFile(r.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func summarizeRelayPayloads(payloads []core.ReplyPayload) string {
	parts := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		text := strings.TrimSpace(payload.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}
