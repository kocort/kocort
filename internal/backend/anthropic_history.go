// Canonical implementation of Anthropic history sanitization, context
// pruning, and response extraction functions.
//
// Pure functions operating on core types and anthropic SDK types.
// No runtime dependency.
package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"

	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// Message building
// ---------------------------------------------------------------------------

// BuildAnthropicMessages constructs the anthropic message list from a
// transcript, request, identity, and session resolution.  The caller
// decomposes its AgentRunContext into these individual core-type parameters
// so that this package does not depend on the runtime package.
func BuildAnthropicMessages(
	transcript []core.TranscriptMessage,
	req core.AgentRunRequest,
	identity core.AgentIdentity,
	session core.SessionResolution,
) []anthropic.MessageParam {
	history := PruneTranscriptForAnthropic(transcript, identity, session)
	messages := make([]anthropic.MessageParam, 0, len(history)+2)
	for _, message := range history {
		switch strings.TrimSpace(strings.ToLower(message.Type)) {
		case "assistant_partial":
			continue
		case "tool_call":
			callName := strings.TrimSpace(message.ToolName)
			callID := strings.TrimSpace(message.ToolCallID)
			if callName == "" || callID == "" {
				continue
			}
			input := any(map[string]any{})
			if len(message.Args) > 0 {
				input = utils.CloneAnyMap(message.Args)
			}
			blocks := []anthropic.ContentBlockParamUnion{}
			if text := strings.TrimSpace(message.Text); text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(text))
			}
			blocks = append(blocks, anthropic.NewToolUseBlock(callID, input, callName))
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
			continue
		case "tool_result":
			toolUseID := strings.TrimSpace(message.ToolCallID)
			if toolUseID == "" {
				continue
			}
			text := strings.TrimSpace(message.Text)
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewToolResultBlock(toolUseID, text, false)))
			continue
		case "system_event", "internal_event":
			continue
		}

		role := NormalizeTranscriptRole(message.Role, message.Type)
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		switch role {
		case "assistant":
			messages = append(messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(text)))
		case "user":
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(text)))
		}
	}
	if blocks := BuildAnthropicRequestBlocks(req); len(blocks) > 0 {
		// Deduplicate: if the last message is a text-only user message with the
		// same content as the request, replace it with the full multimodal
		// message (which may include image attachments). SanitizeAnthropicMessages
		// already merges consecutive user turns, but replacing avoids redundant
		// text duplication in the merged result.
		reqText := strings.TrimSpace(req.Message)
		replaced := false
		if reqText != "" && len(messages) > 0 {
			last := messages[len(messages)-1]
			if last.Role == anthropic.MessageParamRoleUser && len(last.Content) == 1 {
				if last.Content[0].OfText != nil && strings.TrimSpace(last.Content[0].OfText.Text) == reqText {
					messages[len(messages)-1] = anthropic.NewUserMessage(blocks...)
					replaced = true
				}
			}
		}
		if !replaced {
			messages = append(messages, anthropic.NewUserMessage(blocks...))
		}
	}
	return SanitizeAnthropicMessages(messages)
}

// BuildAnthropicRequestBlocks builds content blocks from an agent run
// request, including text and image attachments.
func BuildAnthropicRequestBlocks(req core.AgentRunRequest) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(req.Attachments)+1)
	if trimmed := strings.TrimSpace(req.Message); trimmed != "" {
		blocks = append(blocks, anthropic.NewTextBlock(trimmed))
	}
	for _, attachment := range req.Attachments {
		if !infra.AttachmentIsImage(attachment) || len(attachment.Content) == 0 {
			continue
		}
		mimeType := infra.NormalizeAttachmentMime(attachment)
		if mimeType == "" {
			continue
		}
		blocks = append(blocks, anthropic.NewImageBlockBase64(mimeType, base64.StdEncoding.EncodeToString(attachment.Content)))
	}
	return blocks
}

// ---------------------------------------------------------------------------
// Context pruning
// ---------------------------------------------------------------------------

// PruneTranscriptForAnthropic applies cache-TTL context pruning to the
// transcript when conditions are met.
func PruneTranscriptForAnthropic(
	transcript []core.TranscriptMessage,
	identity core.AgentIdentity,
	session core.SessionResolution,
) []core.TranscriptMessage {
	if !ShouldRunAnthropicContextPruning(identity, session, len(transcript)) {
		return append([]core.TranscriptMessage{}, transcript...)
	}
	return ApplyAnthropicContextPruning(transcript, identity)
}

// ShouldRunAnthropicContextPruning returns true when cache-TTL context
// pruning should be applied based on the identity configuration and
// session state.
func ShouldRunAnthropicContextPruning(
	identity core.AgentIdentity,
	session core.SessionResolution,
	transcriptLen int,
) bool {
	if strings.TrimSpace(strings.ToLower(identity.ContextPruningMode)) != "cache-ttl" {
		return false
	}
	if identity.ContextPruningTTL <= 0 {
		return false
	}
	if session.Entry == nil || session.Entry.LastModelCallAt.IsZero() {
		return false
	}
	if time.Since(session.Entry.LastModelCallAt) < identity.ContextPruningTTL {
		return false
	}
	if transcriptLen == 0 {
		return false
	}
	return true
}

// ApplyAnthropicContextPruning soft-trims and hard-clears old tool results
// in-place based on the identity's pruning configuration.
func ApplyAnthropicContextPruning(history []core.TranscriptMessage, identity core.AgentIdentity) []core.TranscriptMessage {
	if len(history) == 0 {
		return nil
	}
	cutoff := PruningAssistantCutoff(history, identity.ContextPruningKeepLastAssistants)
	if cutoff < 0 {
		return append([]core.TranscriptMessage{}, history...)
	}
	out := append([]core.TranscriptMessage{}, history...)
	candidates := CollectPrunableToolResults(out, cutoff, identity)
	if len(candidates) == 0 {
		return out
	}

	totalChars := 0
	for _, idx := range candidates {
		totalChars += len(strings.TrimSpace(out[idx].Text))
	}
	if totalChars == 0 {
		return out
	}

	softTrimTarget := int(float64(totalChars) * identity.ContextPruningSoftTrimRatio)
	hardClearTarget := int(float64(totalChars) * identity.ContextPruningHardClearRatio)
	softTrimmedChars := 0
	hardClearedChars := 0

	for _, idx := range candidates {
		text := strings.TrimSpace(out[idx].Text)
		if text == "" {
			continue
		}
		if softTrimTarget > 0 && softTrimmedChars < softTrimTarget {
			trimmed := SoftTrimToolResult(text, identity)
			if trimmed != text {
				softTrimmedChars += len(text)
				out[idx].Text = trimmed
				continue
			}
		}
		if identity.ContextPruningHardClearEnabled && hardClearTarget > 0 && hardClearedChars < hardClearTarget {
			hardClearedChars += len(text)
			out[idx].Text = HardClearToolResultPlaceholder(identity, len(text))
		}
	}
	return out
}

// PruningAssistantCutoff returns the transcript index of the Nth-from-last
// assistant entry, or -1 if there aren't enough.
func PruningAssistantCutoff(history []core.TranscriptMessage, keepLastAssistants int) int {
	if keepLastAssistants <= 0 {
		keepLastAssistants = 3
	}
	seen := 0
	for idx := len(history) - 1; idx >= 0; idx-- {
		role := NormalizeTranscriptRole(history[idx].Role, history[idx].Type)
		if role != "assistant" {
			continue
		}
		msgType := strings.TrimSpace(strings.ToLower(history[idx].Type))
		if msgType == "assistant_partial" {
			continue
		}
		seen++
		if seen >= keepLastAssistants {
			return idx
		}
	}
	return -1
}

// CollectPrunableToolResults returns indices of tool_result entries before
// the cutoff that are eligible for pruning.
func CollectPrunableToolResults(history []core.TranscriptMessage, cutoff int, identity core.AgentIdentity) []int {
	indices := make([]int, 0)
	minChars := identity.ContextPruningMinPrunableToolChars
	if minChars <= 0 {
		minChars = 50000
	}
	for idx := 0; idx < cutoff && idx < len(history); idx++ {
		msg := history[idx]
		if strings.TrimSpace(strings.ToLower(msg.Type)) != "tool_result" {
			continue
		}
		if !IsToolPrunableByPolicy(msg.ToolName, identity.ContextPruningAllowTools, identity.ContextPruningDenyTools) {
			continue
		}
		text := strings.TrimSpace(msg.Text)
		if len(text) < minChars {
			continue
		}
		indices = append(indices, idx)
	}
	return indices
}

// IsToolPrunableByPolicy returns whether a tool is eligible for pruning
// based on allow/deny lists.
func IsToolPrunableByPolicy(toolName string, allow []string, deny []string) bool {
	name := strings.TrimSpace(strings.ToLower(toolName))
	if name == "" {
		return false
	}
	if MatchesToolPatterns(name, deny) {
		return false
	}
	if len(allow) == 0 {
		return true
	}
	return MatchesToolPatterns(name, allow)
}

// MatchesToolPatterns returns whether toolName matches any of the given
// glob-like patterns (supports *, prefix*, *suffix, *contains*).
func MatchesToolPatterns(toolName string, patterns []string) bool {
	for _, pattern := range patterns {
		p := strings.TrimSpace(strings.ToLower(pattern))
		switch {
		case p == "":
			continue
		case p == "*":
			return true
		case strings.HasPrefix(p, "*") && strings.HasSuffix(p, "*") && len(p) > 2:
			if strings.Contains(toolName, strings.Trim(p, "*")) {
				return true
			}
		case strings.HasPrefix(p, "*"):
			if strings.HasSuffix(toolName, strings.TrimPrefix(p, "*")) {
				return true
			}
		case strings.HasSuffix(p, "*"):
			if strings.HasPrefix(toolName, strings.TrimSuffix(p, "*")) {
				return true
			}
		case toolName == p:
			return true
		}
	}
	return false
}

// SoftTrimToolResult trims a tool result to head+tail with an ellipsis
// placeholder in between.
func SoftTrimToolResult(text string, identity core.AgentIdentity) string {
	maxChars := identity.ContextPruningSoftTrimMaxChars
	headChars := identity.ContextPruningSoftTrimHeadChars
	tailChars := identity.ContextPruningSoftTrimTailChars
	if maxChars <= 0 {
		maxChars = 4000
	}
	if headChars <= 0 {
		headChars = 1500
	}
	if tailChars <= 0 {
		tailChars = 1500
	}
	if len(text) <= maxChars || headChars+tailChars >= len(text) {
		return text
	}
	head := text[:MinInt(headChars, len(text))]
	tail := text[MaxInt(len(text)-tailChars, 0):]
	return strings.TrimSpace(fmt.Sprintf("%s\n...\n%s\n[tool result trimmed from %d chars]", head, tail, len(text)))
}

// HardClearToolResultPlaceholder returns a short placeholder string that
// replaces a fully cleared tool result.
func HardClearToolResultPlaceholder(identity core.AgentIdentity, originalChars int) string {
	placeholder := strings.TrimSpace(identity.ContextPruningHardClearPlaceholder)
	if placeholder == "" {
		placeholder = "[Old tool result content cleared]"
	}
	return fmt.Sprintf("%s (%d chars)", placeholder, originalChars)
}

// MinInt returns the smaller of a and b.
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MaxInt returns the larger of a and b.
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Anthropic message sanitization
// ---------------------------------------------------------------------------

// SanitizeAnthropicMessages merges consecutive same-role messages and strips
// dangling tool uses.
func SanitizeAnthropicMessages(messages []anthropic.MessageParam) []anthropic.MessageParam {
	if len(messages) == 0 {
		return nil
	}
	stripped := StripDanglingAnthropicToolUses(messages)
	merged := make([]anthropic.MessageParam, 0, len(stripped))
	for _, message := range stripped {
		if len(message.Content) == 0 {
			continue
		}
		if len(merged) > 0 && merged[len(merged)-1].Role == anthropic.MessageParamRoleUser && message.Role == anthropic.MessageParamRoleUser {
			merged[len(merged)-1].Content = append(merged[len(merged)-1].Content, message.Content...)
			continue
		}
		merged = append(merged, message)
	}
	return merged
}

// StripDanglingAnthropicToolUses removes tool_use blocks from assistant
// messages that lack a corresponding tool_result in the following user
// message.
func StripDanglingAnthropicToolUses(messages []anthropic.MessageParam) []anthropic.MessageParam {
	if len(messages) == 0 {
		return nil
	}
	result := make([]anthropic.MessageParam, 0, len(messages))
	for index, message := range messages {
		if message.Role != anthropic.MessageParamRoleAssistant {
			if len(message.Content) > 0 {
				result = append(result, message)
			}
			continue
		}
		nextIndex := index + 1
		if nextIndex >= len(messages) || messages[nextIndex].Role != anthropic.MessageParamRoleUser {
			if len(message.Content) > 0 {
				result = append(result, message)
			}
			continue
		}
		validIDs := CollectAnthropicToolResultIDs(messages[nextIndex])
		original := message.Content
		filtered := make([]anthropic.ContentBlockParamUnion, 0, len(original))
		for _, block := range original {
			if block.OfToolUse != nil {
				if _, ok := validIDs[strings.TrimSpace(block.OfToolUse.ID)]; ok {
					filtered = append(filtered, block)
				}
				continue
			}
			filtered = append(filtered, block)
		}
		if len(original) > 0 && len(filtered) == 0 {
			filtered = []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("[tool calls omitted]")}
		}
		if len(filtered) == 0 {
			continue
		}
		result = append(result, anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleAssistant,
			Content: filtered,
		})
	}
	return result
}

// CollectAnthropicToolResultIDs returns the set of tool_use IDs referenced
// by tool_result blocks in a message.
func CollectAnthropicToolResultIDs(message anthropic.MessageParam) map[string]struct{} {
	ids := map[string]struct{}{}
	for _, block := range message.Content {
		if block.OfToolResult != nil {
			toolUseID := strings.TrimSpace(block.OfToolResult.ToolUseID)
			if toolUseID != "" {
				ids[toolUseID] = struct{}{}
			}
		}
	}
	return ids
}

// ---------------------------------------------------------------------------
// Anthropic response extraction
// ---------------------------------------------------------------------------

// ExtractAnthropicResponseText extracts the concatenated text from an
// Anthropic response message.
func ExtractAnthropicResponseText(message anthropic.Message) string {
	var parts []string
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			if text := strings.TrimSpace(variant.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// ExtractAnthropicToolUses extracts valid tool-use blocks from an
// Anthropic response message.
func ExtractAnthropicToolUses(message anthropic.Message) []anthropic.ToolUseBlock {
	toolUses := make([]anthropic.ToolUseBlock, 0)
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.ToolUseBlock:
			if strings.TrimSpace(variant.ID) == "" || strings.TrimSpace(variant.Name) == "" {
				continue
			}
			toolUses = append(toolUses, variant)
		}
	}
	return toolUses
}

// AnthropicUsageToMap converts Anthropic usage data to a generic map.
func AnthropicUsageToMap(usage anthropic.Usage) map[string]any {
	out := map[string]any{
		"prompt_tokens":     usage.InputTokens,
		"completion_tokens": usage.OutputTokens,
		"total_tokens":      usage.InputTokens + usage.OutputTokens,
	}
	if usage.CacheCreationInputTokens > 0 {
		out["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
	if usage.CacheReadInputTokens > 0 {
		out["cache_read_input_tokens"] = usage.CacheReadInputTokens
	}
	return out
}

// DecodeAnthropicToolUseArgs decodes a JSON raw message into a map for
// tool-use arguments.
func DecodeAnthropicToolUseArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// LimitAnthropicHistoryTurns truncates Anthropic message history to at most
// `limit` user turns, counting from the end. A user turn starts at each
// message with role "user". If limit <= 0, returns messages unchanged.
func LimitAnthropicHistoryTurns(messages []anthropic.MessageParam, limit int) []anthropic.MessageParam {
	if limit <= 0 || len(messages) == 0 {
		return messages
	}
	// Collect indices of user turn starts.
	var userTurnStarts []int
	for i, msg := range messages {
		if msg.Role == anthropic.MessageParamRoleUser {
			userTurnStarts = append(userTurnStarts, i)
		}
	}
	if len(userTurnStarts) <= limit {
		return messages
	}
	cutIndex := userTurnStarts[len(userTurnStarts)-limit]
	return messages[cutIndex:]
}
