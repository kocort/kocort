package tool

import (
	"context"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"

	"strings"

	"github.com/kocort/kocort/utils"
)

type SessionsSendTool struct{}

func NewSessionsSendTool() *SessionsSendTool {
	return &SessionsSendTool{}
}

func (t *SessionsSendTool) Name() string {
	return "sessions_send"
}

func (t *SessionsSendTool) Description() string {
	return "Send a message to another session/sub-agent."
}

func (t *SessionsSendTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sessionKey": map[string]any{"type": "string"},
				"label":      map[string]any{"type": "string"},
				"agentId":    map[string]any{"type": "string"},
				"message":    map[string]any{"type": "string"},
			},
			"required":             []string{"message"},
			"additionalProperties": false,
		},
	}
}

func (t *SessionsSendTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	message, err := ReadStringParam(args, "message", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	sessionKey, _ := ReadStringParam(args, "sessionKey", false) // zero value fallback is intentional
	label, _ := ReadStringParam(args, "label", false)           // zero value fallback is intentional
	labelAgentID, _ := ReadStringParam(args, "agentId", false)  // zero value fallback is intentional
	if strings.TrimSpace(sessionKey) != "" && strings.TrimSpace(label) != "" {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "Provide either sessionKey or label (not both).",
		})
	}
	if strings.TrimSpace(sessionKey) == "" && strings.TrimSpace(label) != "" {
		sessionKey, _ = session.ResolveSessionReference(toolCtx.Runtime.GetSessions(), session.ResolveReferenceOptions{
			Reference:        label,
			RequesterAgentID: utils.NonEmpty(labelAgentID, toolCtx.Run.Request.AgentID),
		})
	}
	if strings.TrimSpace(sessionKey) == "" {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "Either sessionKey or label is required",
		})
	}
	target, denied, err := resolveAccessibleSessionTarget(toolCtx, session.SessionAccessSend, sessionKey, toolCtx.Run.Request.AgentID)
	if err != nil {
		return core.ToolResult{}, err
	}
	if denied != nil {
		return *denied, nil
	}
	sessionKey = target.Key

	// Evaluate send policy if configured.
	if policy := toolCtx.Runtime.GetSendPolicy(); policy != nil {
		result := session.EvaluateSendPolicy(*policy, session.SendPolicyInput{
			Channel:    toolCtx.Run.Request.Channel,
			SessionKey: sessionKey,
		})
		if !result.Allowed {
			reason := result.Reason
			if reason == "" {
				reason = "denied by send policy"
			}
			return JSONResult(map[string]any{
				"status": "error",
				"error":  reason,
			})
		}
	}

	extraSystemPrompt := BuildAgentToAgentMessageContext(
		toolCtx.Run.Session.SessionKey,
		toolCtx.Run.Request.Channel,
		sessionKey,
	)
	result, err := toolCtx.Runtime.Run(ctx, core.AgentRunRequest{
		Message:           message,
		SessionKey:        sessionKey,
		AgentID:           session.ResolveAgentIDFromSessionKey(sessionKey),
		Lane:              core.LaneNested,
		Deliver:           false,
		ExtraSystemPrompt: extraSystemPrompt,
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	replyText := ""
	for i := len(result.Payloads) - 1; i >= 0; i-- {
		if strings.TrimSpace(result.Payloads[i].Text) != "" {
			replyText = result.Payloads[i].Text
			break
		}
	}
	return JSONResult(map[string]any{
		"runId":      result.RunID,
		"status":     "ok",
		"sessionKey": sessionKey,
		"reply":      replyText,
	})
}

func BuildAgentToAgentMessageContext(requesterSessionKey string, requesterChannel string, targetSessionKey string) string {
	lines := []string{"Agent-to-agent message context:"}
	if strings.TrimSpace(requesterSessionKey) != "" {
		lines = append(lines, "Agent 1 (requester) session: "+strings.TrimSpace(requesterSessionKey)+".")
	}
	if strings.TrimSpace(requesterChannel) != "" {
		lines = append(lines, "Agent 1 (requester) channel: "+strings.TrimSpace(requesterChannel)+".")
	}
	lines = append(lines, "Agent 2 (target) session: "+strings.TrimSpace(targetSessionKey)+".")
	return strings.Join(lines, "\n")
}
