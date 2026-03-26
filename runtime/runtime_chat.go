package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/gateway"
	sessionpkg "github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

func (r *Runtime) ChatSend(ctx context.Context, req core.ChatSendRequest) (core.ChatSendResponse, error) {
	deliver := true
	if req.Deliver != nil {
		deliver = *req.Deliver
	}
	timeout := time.Duration(0)
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}
	runReq := core.AgentRunRequest{
		AgentID:     req.AgentID,
		SessionKey:  req.SessionKey,
		Message:     req.Message,
		Channel:     utils.NonEmpty(req.Channel, "webchat"),
		To:          req.To,
		AccountID:   req.AccountID,
		ThreadID:    req.ThreadID,
		ChatType:    req.ChatType,
		Attachments: req.Attachments,
		Deliver:     deliver,
		Timeout:     timeout,
	}
	if runReq.AgentID == "" {
		runReq.AgentID = sessionpkg.ResolveAgentIDFromSessionKey(runReq.SessionKey)
	}
	if runReq.AgentID == "" {
		runReq.AgentID = sessionpkg.DefaultAgentID
	}
	if strings.TrimSpace(runReq.SessionKey) == "" {
		bindingSvc := sessionpkg.NewThreadBindingService(r.Sessions)
		if boundKey, ok := bindingSvc.ResolveThreadSession(sessionpkg.BoundSessionLookupOptions{
			Channel:   runReq.Channel,
			To:        runReq.To,
			AccountID: runReq.AccountID,
			ThreadID:  runReq.ThreadID,
		}); ok {
			runReq.SessionKey = boundKey
			runReq.AgentID = sessionpkg.ResolveAgentIDFromSessionKey(boundKey)
			bindingSvc.TouchThreadBinding(sessionpkg.BoundSessionLookupOptions{
				Channel:   runReq.Channel,
				To:        runReq.To,
				AccountID: runReq.AccountID,
				ThreadID:  runReq.ThreadID,
			})
		}
	}
	session, err := r.Sessions.ResolveForRequest(ctx, sessionpkg.SessionResolveOptions{
		AgentID:             runReq.AgentID,
		SessionKey:          runReq.SessionKey,
		SessionID:           runReq.SessionID,
		To:                  runReq.To,
		Channel:             runReq.Channel,
		ThreadID:            runReq.ThreadID,
		ChatType:            runReq.ChatType,
		MainKey:             config.ResolveSessionMainKey(r.Config),
		DMScope:             config.ResolveSessionDMScope(r.Config),
		ParentForkMaxTokens: config.ResolveSessionParentForkMaxTokens(r.Config),
		Now:                 time.Now().UTC(),
		ResetPolicy:         sessionpkg.ResolveFreshnessPolicyForSession(r.Config, runReq.SessionKey, runReq.ChatType, runReq.Channel, runReq.ThreadID),
	})
	if err != nil {
		return core.ChatSendResponse{}, err
	}
	runReq.SessionKey = session.SessionKey
	if req.Stop || gateway.IsChatStopCommandText(req.Message) {
		return r.handleChatStop(ctx, req, session.SessionKey)
	}
	result, err := r.Run(ctx, runReq)
	if err != nil {
		return core.ChatSendResponse{}, err
	}
	sessionKey := runReq.SessionKey
	sessionID := ""
	if entry := r.Sessions.Entry(sessionKey); entry != nil {
		sessionID = entry.SessionID
	}
	history, _, _, _, _ := sessionpkg.LoadChatHistoryPage(r.Sessions, sessionKey, 0, 0) // best-effort; only text matters here
	return core.ChatSendResponse{
		RunID:          result.RunID,
		SessionKey:     sessionKey,
		SessionID:      sessionID,
		SkillsSnapshot: chatSkillSnapshotSummary(r.Sessions.Entry(sessionKey)),
		Payloads:       append([]core.ReplyPayload{}, result.Payloads...),
		Queued:         result.Queued,
		QueueDepth:     result.QueueDepth,
		Messages:       history,
		Aborted:        false,
		AbortedRunIDs:  nil,
	}, nil
}

func (r *Runtime) ChatCancel(ctx context.Context, req core.ChatCancelRequest) (core.ChatCancelResponse, error) {
	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		sessionKey = session.BuildMainSessionKeyWithMain(config.ResolveDefaultConfiguredAgentID(r.Config), config.ResolveSessionMainKey(r.Config))
	}
	runIDs, queueCleared := r.cancelChatExecution(sessionKey, strings.TrimSpace(req.RunID))
	resp := core.ChatCancelResponse{
		SessionKey:     sessionKey,
		SkillsSnapshot: chatSkillSnapshotSummary(r.Sessions.Entry(sessionKey)),
		Aborted:        len(runIDs) > 0,
		RunIDs:         append([]string{}, runIDs...),
		ClearedQueued:  queueCleared,
	}
	if len(runIDs) > 0 || queueCleared > 0 {
		if entry := r.Sessions.Entry(sessionKey); entry != nil && entry.SessionID != "" {
			_ = r.Sessions.AppendTranscript(sessionKey, entry.SessionID, core.TranscriptMessage{ // best-effort; failure is non-critical
				Type:      "assistant_final",
				Role:      "assistant",
				Text:      gateway.FormatChatAbortReplyText(),
				Timestamp: time.Now().UTC(),
				Final:     true,
			})
		}
		history, _, _, _, _ := session.LoadChatHistoryPage(r.Sessions, sessionKey, 0, 0) // best-effort; only text matters here
		resp.Messages = history
		resp.Payloads = []core.ReplyPayload{{Text: gateway.FormatChatAbortReplyText()}}
	}
	return resp, nil
}

func (r *Runtime) cancelChatExecution(sessionKey string, runID string) ([]string, int) {
	if r == nil || r.ActiveRuns == nil {
		return nil, 0
	}
	var runIDs []string
	if runID != "" {
		if r.ActiveRuns.CancelRun(sessionKey, runID) {
			runIDs = append(runIDs, runID)
		} else {
			// Fallback: the caller may have sent a stale or client-generated
			// runID (e.g. a frontend pending key). Cancel every active run
			// on the session so the request still takes effect.
			runIDs = r.ActiveRuns.CancelSession(sessionKey)
		}
	} else {
		runIDs = r.ActiveRuns.CancelSession(sessionKey)
	}
	queueCleared := 0
	if r.Queue != nil {
		queueCleared = r.Queue.Clear(sessionKey)
	}
	return runIDs, queueCleared
}

func (r *Runtime) handleChatStop(ctx context.Context, req core.ChatSendRequest, sessionKey string) (core.ChatSendResponse, error) {
	resp, err := r.ChatCancel(ctx, core.ChatCancelRequest{
		SessionKey: sessionKey,
		RunID:      strings.TrimSpace(req.RunID),
	})
	if err != nil {
		return core.ChatSendResponse{}, err
	}
	sessionID := ""
	if entry := r.Sessions.Entry(sessionKey); entry != nil {
		sessionID = entry.SessionID
	}
	return core.ChatSendResponse{
		SessionKey:     sessionKey,
		SessionID:      sessionID,
		SkillsSnapshot: resp.SkillsSnapshot,
		Payloads:       append([]core.ReplyPayload{}, resp.Payloads...),
		Messages:       append([]core.TranscriptMessage{}, resp.Messages...),
		Aborted:        resp.Aborted,
		AbortedRunIDs:  append([]string{}, resp.RunIDs...),
		ClearedQueue:   resp.ClearedQueued,
	}, nil
}

func chatSkillSnapshotSummary(entry *core.SessionEntry) *core.SkillSnapshotSummary {
	if entry == nil || entry.SkillsSnapshot == nil {
		return nil
	}
	snapshot := entry.SkillsSnapshot
	skillNames := append([]string{}, snapshot.ResolvedName...)
	if len(skillNames) == 0 {
		skillNames = make([]string, 0, len(snapshot.Skills))
		for _, skillEntry := range snapshot.Skills {
			if name := strings.TrimSpace(skillEntry.Name); name != "" {
				skillNames = append(skillNames, name)
			}
		}
	}
	commandNames := make([]string, 0, len(snapshot.Commands))
	for _, command := range snapshot.Commands {
		if name := strings.TrimSpace(command.Name); name != "" {
			commandNames = append(commandNames, name)
		}
	}
	return &core.SkillSnapshotSummary{
		Version:      snapshot.Version,
		SkillNames:   skillNames,
		CommandNames: commandNames,
	}
}

// PushInbound routes an inbound channel message through the runtime.
func (r *Runtime) PushInbound(ctx context.Context, msg core.ChannelInboundMessage) (core.ChatSendResponse, error) {
	event.SyncDelivererHooks(r.Deliverer, r.Hooks, r.Audit)
	if r == nil {
		return core.ChatSendResponse{}, core.ErrRuntimeNotReady
	}
	if r.Channels == nil {
		return core.ChatSendResponse{}, core.ErrChannelRegistryNotConfigured
	}
	normalized, err := r.Channels.NormalizeInbound(msg.Channel, &msg)
	if err != nil {
		return core.ChatSendResponse{}, err
	}
	agentID := sessionpkg.NormalizeAgentID(utils.NonEmpty(normalized.AgentID, utils.NonEmpty(r.Channels.ResolveAgentID(normalized.Channel), sessionpkg.DefaultAgentID)))
	replyTarget := utils.NonEmpty(normalized.From, normalized.To)
	switch normalized.ChatType {
	case core.ChatTypeGroup, core.ChatTypeTopic:
		replyTarget = utils.NonEmpty(normalized.To, normalized.From)
	}
	sessionKey := strings.TrimSpace(replyTarget)
	if sessionKey != "" {
		dmScope := config.ResolveSessionDMScope(r.Config)
		mainKey := config.ResolveSessionMainKey(r.Config)
		switch normalized.ChatType {
		case core.ChatTypeGroup, core.ChatTypeTopic:
			sessionKey = sessionpkg.BuildGroupSessionKey(agentID, normalized.Channel, normalized.ChatType, replyTarget)
			if strings.TrimSpace(normalized.ThreadID) != "" {
				sessionKey = sessionpkg.BuildThreadSessionKey(sessionKey, normalized.ThreadID)
			}
		case core.ChatTypeThread:
			base := sessionpkg.BuildMainSessionKeyWithMain(agentID, mainKey)
			if dmScope != "main" {
				base = sessionpkg.BuildDirectSessionKey(agentID, normalized.Channel, utils.NonEmpty(normalized.To, normalized.From))
			}
			sessionKey = sessionpkg.BuildThreadSessionKey(base, normalized.ThreadID)
		default:
			if dmScope == "main" {
				sessionKey = sessionpkg.BuildMainSessionKeyWithMain(agentID, mainKey)
			} else {
				sessionKey = sessionpkg.BuildDirectSessionKey(agentID, normalized.Channel, replyTarget)
			}
		}
	}
	event.RecordChannelEvent(ctx, r.Audit, r.Logger, *normalized, "inbound_received", "channel inbound message received", map[string]any{
		"sessionKey":      sessionKey,
		"accountId":       normalized.AccountID,
		"from":            normalized.From,
		"to":              normalized.To,
		"threadId":        normalized.ThreadID,
		"chatType":        normalized.ChatType,
		"messageId":       normalized.MessageID,
		"text":            normalized.Text,
		"attachmentCount": len(normalized.Attachments),
	})
	event.EmitDebugEvent(r.EventHub, sessionKey, "", "inbound", map[string]any{
		"type":            "channel_inbound",
		"channel":         normalized.Channel,
		"accountId":       normalized.AccountID,
		"from":            normalized.From,
		"to":              normalized.To,
		"threadId":        normalized.ThreadID,
		"chatType":        normalized.ChatType,
		"messageId":       normalized.MessageID,
		"text":            normalized.Text,
		"attachmentCount": len(normalized.Attachments),
	})
	return r.ChatSend(ctx, core.ChatSendRequest{
		AgentID:     agentID,
		Message:     normalized.Text,
		Channel:     normalized.Channel,
		To:          replyTarget,
		AccountID:   normalized.AccountID,
		ThreadID:    normalized.ThreadID,
		ChatType:    normalized.ChatType,
		Deliver:     utils.BoolPtr(true),
		Attachments: append([]core.Attachment{}, normalized.Attachments...),
	})
}
