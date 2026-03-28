package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

type MessageTool struct{}

func NewMessageTool() *MessageTool { return &MessageTool{} }

func (t *MessageTool) Name() string { return "message" }

func (t *MessageTool) Description() string {
	return "Send a proactive message, file, or image to the current channel or another delivery target."
}

func (t *MessageTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Channel action to perform. Currently only 'send' is supported.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Visible text to send.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Optional file path to send. Can be absolute or relative to the working directory.",
				},
				"paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional file paths to send. Use either 'path' or 'paths', not both.",
				},
				"channel": map[string]any{
					"type":        "string",
					"description": "Optional target channel. Defaults to the current request channel.",
				},
				"to": map[string]any{
					"type":        "string",
					"description": "Optional recipient/chat identifier. Defaults to the current request target.",
				},
				"accountId": map[string]any{
					"type":        "string",
					"description": "Optional outbound account id override.",
				},
				"threadId": map[string]any{
					"type":        "string",
					"description": "Optional thread id override.",
				},
				"replyToId": map[string]any{
					"type":        "string",
					"description": "Optional channel-native reply target id.",
				},
				"channelData": map[string]any{
					"type":        "object",
					"description": "Optional string key/value channel metadata.",
				},
			},
			"additionalProperties": false,
		},
	}
}

type messageRuntime interface {
	DeliverMessage(context.Context, core.ReplyKind, core.ReplyPayload, core.DeliveryTarget) error
}

func (t *MessageTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	runtime, ok := toolCtx.Runtime.(messageRuntime)
	if !ok {
		return core.ToolResult{}, fmt.Errorf("message delivery is not available in this runtime")
	}
	action, _ := ReadStringParam(args, "action", false)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "send"
	}
	if action != "send" {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  fmt.Sprintf("unsupported action %q", action),
		})
	}
	messageText, _ := ReadStringParam(args, "message", false)
	singlePath, _ := ReadStringParam(args, "path", false)
	multiplePaths, err := ReadOptionalStringSliceParam(args, "paths")
	if err != nil {
		return core.ToolResult{}, err
	}
	var mediaURLs []string
	var pathErrors []string
	if strings.TrimSpace(singlePath) != "" || len(multiplePaths) > 0 {
		var invalidInputResult *core.ToolResult
		mediaURLs, pathErrors, invalidInputResult, err = buildSendFileMediaResult(toolCtx, singlePath, multiplePaths)
		if err != nil {
			return core.ToolResult{}, err
		}
		if invalidInputResult != nil {
			return *invalidInputResult, nil
		}
	}
	channel, _ := ReadStringParam(args, "channel", false)
	to, _ := ReadStringParam(args, "to", false)
	accountID, _ := ReadStringParam(args, "accountId", false)
	threadID, _ := ReadStringParam(args, "threadId", false)
	replyToID, _ := ReadStringParam(args, "replyToId", false)
	channelData, err := ReadOptionalStringMapParam(args, "channelData")
	if err != nil {
		return core.ToolResult{}, err
	}
	payload := core.ReplyPayload{
		Text:      strings.TrimSpace(messageText),
		ReplyToID: strings.TrimSpace(replyToID),
	}
	if len(mediaURLs) == 1 {
		payload.MediaURL = mediaURLs[0]
	} else if len(mediaURLs) > 1 {
		payload.MediaURLs = append([]string{}, mediaURLs...)
	}
	if len(channelData) > 0 {
		payload.ChannelData = make(map[string]any, len(channelData))
		for key, value := range channelData {
			payload.ChannelData[key] = value
		}
	}
	if strings.TrimSpace(payload.Text) == "" && payload.MediaURL == "" && len(payload.MediaURLs) == 0 {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "message requires at least one of: message, path, paths",
		})
	}
	target := core.DeliveryTarget{
		SessionKey: strings.TrimSpace(toolCtx.Run.Session.SessionKey),
		Channel:    firstNonEmpty(channel, toolCtx.Run.Request.Channel),
		To:         firstNonEmpty(to, toolCtx.Run.Request.To),
		AccountID:  firstNonEmpty(accountID, toolCtx.Run.Request.AccountID),
		ThreadID:   firstNonEmpty(threadID, toolCtx.Run.Request.ThreadID),
		RunID:      strings.TrimSpace(toolCtx.Run.Request.RunID),
	}
	currentTarget := core.DeliveryTarget{
		SessionKey: strings.TrimSpace(toolCtx.Run.Session.SessionKey),
		Channel:    strings.TrimSpace(toolCtx.Run.Request.Channel),
		To:         strings.TrimSpace(toolCtx.Run.Request.To),
		AccountID:  strings.TrimSpace(toolCtx.Run.Request.AccountID),
		ThreadID:   strings.TrimSpace(toolCtx.Run.Request.ThreadID),
		RunID:      strings.TrimSpace(toolCtx.Run.Request.RunID),
	}
	for _, nextTarget := range buildMessageDeliveryTargets(currentTarget, target) {
		if err := runtime.DeliverMessage(ctx, core.ReplyKindFinal, payload, nextTarget); err != nil {
			return core.ToolResult{}, err
		}
	}
	return JSONResult(map[string]any{
		"status":      "ok",
		"action":      action,
		"channel":     target.Channel,
		"to":          target.To,
		"accountId":   target.AccountID,
		"threadId":    target.ThreadID,
		"textChars":   len([]rune(payload.Text)),
		"mediaCount":  countPayloadMedia(payload),
		"pathErrors":  pathErrors,
		"usedReplyTo": payload.ReplyToID != "",
		"channelData": payload.ChannelData,
		"sessionKey":  target.SessionKey,
		"description": buildMessageDeliveryDescription(payload, pathErrors),
	})
}

func buildMessageDeliveryTargets(currentTarget core.DeliveryTarget, target core.DeliveryTarget) []core.DeliveryTarget {
	if sameDeliveryTarget(currentTarget, target) {
		target.SkipTranscriptMirror = false
		return []core.DeliveryTarget{target}
	}
	target.SkipTranscriptMirror = true
	if !isWebchatChannel(currentTarget.Channel) {
		target.SkipTranscriptMirror = false
		return []core.DeliveryTarget{target}
	}
	if strings.TrimSpace(currentTarget.SessionKey) == "" {
		return []core.DeliveryTarget{target}
	}
	currentTarget.SkipTranscriptMirror = false
	return []core.DeliveryTarget{target, currentTarget}
}

func sameDeliveryTarget(a core.DeliveryTarget, b core.DeliveryTarget) bool {
	return strings.EqualFold(strings.TrimSpace(a.Channel), strings.TrimSpace(b.Channel)) &&
		strings.TrimSpace(a.To) == strings.TrimSpace(b.To) &&
		strings.TrimSpace(a.AccountID) == strings.TrimSpace(b.AccountID) &&
		strings.TrimSpace(a.ThreadID) == strings.TrimSpace(b.ThreadID)
}

func isWebchatChannel(channel string) bool {
	return strings.EqualFold(strings.TrimSpace(channel), "webchat")
}

func buildMessageDeliveryDescription(payload core.ReplyPayload, pathErrors []string) string {
	parts := []string{}
	if strings.TrimSpace(payload.Text) != "" {
		parts = append(parts, fmt.Sprintf("text:%d", len([]rune(payload.Text))))
	}
	if count := countPayloadMedia(payload); count > 0 {
		parts = append(parts, fmt.Sprintf("media:%d", count))
	}
	if len(pathErrors) > 0 {
		parts = append(parts, fmt.Sprintf("pathErrors:%d", len(pathErrors)))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

func countPayloadMedia(payload core.ReplyPayload) int {
	if payload.MediaURL != "" {
		return 1
	}
	return len(payload.MediaURLs)
}

func buildSendFileMediaResult(toolCtx ToolContext, singlePath string, multiplePaths []string) ([]string, []string, *core.ToolResult, error) {
	if strings.TrimSpace(singlePath) != "" && len(multiplePaths) > 0 {
		result, err := JSONResult(map[string]any{
			"status": "error",
			"error":  "Provide either 'path' or 'paths', not both.",
		})
		if err != nil {
			return nil, nil, nil, err
		}
		return nil, nil, &result, nil
	}
	if strings.TrimSpace(singlePath) != "" {
		multiplePaths = []string{strings.TrimSpace(singlePath)}
	}
	if len(multiplePaths) == 0 {
		result, err := JSONResult(map[string]any{
			"status": "error",
			"error":  "Either 'path' or 'paths' is required.",
		})
		if err != nil {
			return nil, nil, nil, err
		}
		return nil, nil, &result, nil
	}
	validPaths := make([]string, 0, len(multiplePaths))
	errors := make([]string, 0)
	for _, rawPath := range multiplePaths {
		resolvedPath, err := resolveSendablePath(toolCtx, rawPath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", strings.TrimSpace(rawPath), err))
			continue
		}
		validPaths = append(validPaths, fileURI(resolvedPath))
	}
	if len(validPaths) == 0 {
		errorMsg := "no valid files to send"
		if len(errors) > 0 {
			errorMsg = strings.Join(errors, "; ")
		}
		result, err := JSONResult(map[string]any{
			"status": "error",
			"error":  errorMsg,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		return nil, errors, &result, nil
	}
	return validPaths, errors, nil, nil
}

func resolveSendablePath(toolCtx ToolContext, rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", ToolInputError{Message: "path is required"}
	}
	workspaceDir := strings.TrimSpace(toolCtx.Run.WorkspaceDir)
	if workspaceDir == "" {
		return "", fmt.Errorf("working directory is not configured")
	}
	resolvedPath := rawPath
	if !filepath.IsAbs(rawPath) && workspaceDir != "" {
		resolvedPath = filepath.Join(workspaceDir, rawPath)
	}
	resolvedPath, err := filepath.Abs(resolvedPath)
	if err != nil {
		return "", err
	}
	if err := ensurePathWithinToolSandbox(toolCtx, resolvedPath); err != nil {
		return "", err
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("is a directory, not a file")
	}
	return resolvedPath, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
func ReadOptionalStringSliceParam(params map[string]any, key string) ([]string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, ToolInputError{Message: fmt.Sprintf("parameter %q must be an array", key)}
	}
	out := make([]string, 0, len(arr))
	for i, item := range arr {
		text, ok := item.(string)
		if !ok {
			return nil, ToolInputError{Message: fmt.Sprintf("parameter %q[%d] must be a string", key, i)}
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out, nil
}
