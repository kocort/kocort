package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	openai "github.com/sashabaranov/go-openai"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/tool"
)

func TestSanitizeTranscriptForOpenAIKeepsCompactionSchemaFields(t *testing.T) {
	sanitized := SanitizeTranscriptForOpenAI([]core.TranscriptMessage{{
		ID:               "entry_compact",
		Type:             "compaction",
		Role:             "system",
		Summary:          "compressed history",
		FirstKeptEntryID: "entry_keep_1",
		TokensBefore:     1234,
		Instructions:     "preserve tasks",
		Timestamp:        time.Now().UTC(),
	}})
	if len(sanitized) != 1 {
		t.Fatalf("expected one compaction entry, got %+v", sanitized)
	}
	if sanitized[0].ID != "entry_compact" || sanitized[0].Summary != "compressed history" {
		t.Fatalf("expected id/summary to survive sanitize, got %+v", sanitized[0])
	}
	if sanitized[0].FirstKeptEntryID != "entry_keep_1" || sanitized[0].TokensBefore != 1234 {
		t.Fatalf("expected firstKeptEntryId/tokensBefore to survive sanitize, got %+v", sanitized[0])
	}
}

func TestSanitizeOpenAICompatMessagesDropsOrphanToolMessages(t *testing.T) {
	sanitized := SanitizeOpenAICompatMessages([]openai.ChatCompletionMessage{
		{Role: "user", Content: "hello"},
		{Role: "tool", ToolCallID: "missing", Name: "exec", Content: "orphan"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{
					Name:      "exec",
					Arguments: `{"command":"echo ok"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "exec", Content: "ok"},
	})
	if len(sanitized) != 3 {
		t.Fatalf("expected user + assistant + tool, got %+v", sanitized)
	}
	if sanitized[1].Role != "assistant" || len(sanitized[1].ToolCalls) != 1 {
		t.Fatalf("unexpected assistant message: %+v", sanitized[1])
	}
	if sanitized[2].Role != "tool" || sanitized[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool message: %+v", sanitized[2])
	}
}

func TestSanitizeTranscriptForOpenAIMergesAdjacentSameRoleMessages(t *testing.T) {
	sanitized := SanitizeTranscriptForOpenAI([]core.TranscriptMessage{
		{Role: "user", Text: "one"},
		{Role: "user", Text: "two"},
		{Role: "assistant", Text: "three"},
		{Role: "assistant", Text: "four"},
	})
	if len(sanitized) != 2 {
		t.Fatalf("expected merged transcript, got %+v", sanitized)
	}
	if sanitized[0].Text != "one\n\ntwo" || sanitized[1].Text != "three\n\nfour" {
		t.Fatalf("unexpected merged transcript: %+v", sanitized)
	}
}

func TestSanitizeTranscriptForOpenAIPreservesToolChain(t *testing.T) {
	sanitized := SanitizeTranscriptForOpenAI([]core.TranscriptMessage{
		{Role: "user", Text: "search docs"},
		{Type: "assistant_partial", Role: "assistant", Text: "thinking"},
		{Type: "tool_call", Role: "assistant", ToolCallID: "call_1", ToolName: "memory_search", Args: map[string]any{"query": "docs"}},
		{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "memory_search", Text: "found docs"},
		{Type: "assistant_final", Role: "assistant", Text: "done", Final: true},
	})
	if len(sanitized) != 4 {
		t.Fatalf("expected user + tool call + tool result + final, got %+v", sanitized)
	}
	if sanitized[1].Type != "tool_call" || sanitized[1].ToolName != "memory_search" {
		t.Fatalf("unexpected tool call transcript: %+v", sanitized[1])
	}
	if sanitized[2].Type != "tool_result" || sanitized[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool result transcript: %+v", sanitized[2])
	}
	if sanitized[3].Type != "assistant_final" || sanitized[3].Text != "done" {
		t.Fatalf("unexpected final transcript: %+v", sanitized[3])
	}
}

func TestBuildOpenAICompatMessagesIncludesTranscriptToolChain(t *testing.T) {
	messages := BuildOpenAICompatMessages(rtypes.AgentRunContext{
		SystemPrompt: "system",
		Request:      core.AgentRunRequest{Message: "continue"},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "search docs"},
			{Type: "tool_call", Role: "assistant", ToolCallID: "call_1", ToolName: "memory_search", Args: map[string]any{"query": "docs"}},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "memory_search", Text: "found docs"},
			{Type: "assistant_final", Role: "assistant", Text: "done", Final: true},
		},
	})
	if len(messages) != 6 {
		t.Fatalf("unexpected message count: %+v", messages)
	}
	if messages[2].Role != "assistant" || len(messages[2].ToolCalls) != 1 || messages[2].ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected transcript tool call in assistant message, got %+v", messages[2])
	}
	if messages[3].Role != "tool" || messages[3].ToolCallID != "call_1" {
		t.Fatalf("expected transcript tool result, got %+v", messages[3])
	}
}

func TestBuildOpenAICompatMessagesPreservesEmptyToolResult(t *testing.T) {
	messages := BuildOpenAICompatMessages(rtypes.AgentRunContext{
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "do it"},
			{Type: "tool_call", Role: "assistant", ToolCallID: "call_1", ToolName: "exec", Args: map[string]any{"command": "true"}},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "exec", Text: ""},
		},
	})
	if len(messages) != 3 {
		t.Fatalf("unexpected message count: %+v", messages)
	}
	if messages[2].Role != "tool" || messages[2].ToolCallID != "call_1" {
		t.Fatalf("expected tool result message to be preserved, got %+v", messages[2])
	}
}

func TestBuildOpenAICompatMessagesIncludesImageAttachments(t *testing.T) {
	messages := BuildOpenAICompatMessages(rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			Message: "describe this image",
			Attachments: []core.Attachment{{
				Type:     "image",
				Name:     "pixel.png",
				MIMEType: "image/png",
				Content:  []byte("PNGDATA"),
			}},
		},
	})
	if len(messages) != 1 {
		t.Fatalf("unexpected message count: %+v", messages)
	}
	if messages[0].Role != "user" || len(messages[0].MultiContent) != 2 {
		t.Fatalf("expected multimodal user message, got %+v", messages[0])
	}
	if messages[0].MultiContent[0].Type != openai.ChatMessagePartTypeText || messages[0].MultiContent[0].Text != "describe this image" {
		t.Fatalf("unexpected text part: %+v", messages[0].MultiContent[0])
	}
	wantURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
	if messages[0].MultiContent[1].Type != openai.ChatMessagePartTypeImageURL || messages[0].MultiContent[1].ImageURL == nil || messages[0].MultiContent[1].ImageURL.URL != wantURL {
		t.Fatalf("unexpected image part: %+v", messages[0].MultiContent[1])
	}
}

func TestImageAttachmentSurvivesSanitizeHistoryPipeline(t *testing.T) {
	messages := BuildOpenAICompatMessages(rtypes.AgentRunContext{
		SystemPrompt: "You are a helpful assistant.",
		Request: core.AgentRunRequest{
			Message: "describe this image",
			Attachments: []core.Attachment{{
				Type:     "image",
				Name:     "photo.jpg",
				MIMEType: "image/jpeg",
				Content:  []byte("JPEGDATA"),
			}},
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "Hi! How can I help?", Type: "assistant_final"},
		},
	})
	policy := ResolveTranscriptPolicy("nvidia", "openai-completions", "qwen3.5-plus")
	allowedNames := map[string]bool{"exec": true}
	sanitized := SanitizeHistoryPipeline(messages, policy, allowedNames)
	var found *openai.ChatCompletionMessage
	for i := range sanitized {
		if sanitized[i].Role == "user" && len(sanitized[i].MultiContent) > 0 {
			found = &sanitized[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no multimodal user message found after SanitizeHistoryPipeline; messages: %+v", sanitized)
	}
	if len(found.MultiContent) != 2 {
		t.Fatalf("expected 2 MultiContent parts (text + image), got %d: %+v", len(found.MultiContent), found.MultiContent)
	}
	if found.MultiContent[0].Type != openai.ChatMessagePartTypeText || found.MultiContent[0].Text != "describe this image" {
		t.Fatalf("unexpected text part: %+v", found.MultiContent[0])
	}
	wantURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("JPEGDATA"))
	if found.MultiContent[1].Type != openai.ChatMessagePartTypeImageURL || found.MultiContent[1].ImageURL == nil || found.MultiContent[1].ImageURL.URL != wantURL {
		t.Fatalf("unexpected image part: %+v", found.MultiContent[1])
	}
	for _, msg := range sanitized {
		if _, err := json.Marshal(msg); err != nil {
			t.Fatalf("message failed JSON marshal: %v (message: %+v)", err, msg)
		}
	}
}

func TestBuildOpenAICompatMessagesDedupesUserWithImage(t *testing.T) {
	messages := BuildOpenAICompatMessages(rtypes.AgentRunContext{
		SystemPrompt: "You are helpful.",
		Request: core.AgentRunRequest{
			Message: "describe this image",
			Attachments: []core.Attachment{{
				Type: "image", Name: "photo.png", MIMEType: "image/png", Content: []byte("PNG"),
			}},
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "Hi!", Type: "assistant_final"},
			{Role: "user", Text: "describe this image"},
		},
	})
	if len(messages) != 4 {
		for i, msg := range messages {
			t.Logf("  messages[%d]: role=%s multiContent=%d content=%q", i, msg.Role, len(msg.MultiContent), fmt.Sprintf("%v", msg.Content))
		}
		t.Fatalf("expected 4 messages after dedup, got %d", len(messages))
	}
	last := messages[len(messages)-1]
	if last.Role != "user" || len(last.MultiContent) != 2 {
		t.Fatalf("last message should be multimodal user; got role=%s multiContent=%d", last.Role, len(last.MultiContent))
	}
	if last.MultiContent[0].Type != openai.ChatMessagePartTypeText || last.MultiContent[0].Text != "describe this image" {
		t.Fatalf("unexpected text part: %+v", last.MultiContent[0])
	}
	if last.MultiContent[1].Type != openai.ChatMessagePartTypeImageURL {
		t.Fatalf("unexpected image part type: %v", last.MultiContent[1].Type)
	}
	if messages[len(messages)-2].Role != "assistant" {
		t.Fatalf("expected assistant before last user message, got %s", messages[len(messages)-2].Role)
	}
}

func TestSanitizeOpenAICompatMessagesPreservesEmptyToolResultForPendingCall(t *testing.T) {
	sanitized := SanitizeOpenAICompatMessages([]openai.ChatCompletionMessage{
		{Role: "user", Content: "do it"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_1",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "exec",
					Arguments: "{}",
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "exec", Content: ""},
	})
	if len(sanitized) != 3 {
		t.Fatalf("expected tool result to survive sanitize, got %+v", sanitized)
	}
	if sanitized[2].Role != "tool" || sanitized[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected sanitized tool message: %+v", sanitized[2])
	}
}

func TestSanitizeOpenAICompatMessagesStripsDanglingToolCallButKeepsAssistantText(t *testing.T) {
	sanitized := SanitizeOpenAICompatMessages([]openai.ChatCompletionMessage{
		{Role: "user", Content: "take a screenshot"},
		{
			Role:    "assistant",
			Content: "我来打开浏览器并截图",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_browser_1",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "agent-browser",
					Arguments: `{"action":"screenshot"}`,
				},
			}},
		},
		{Role: "user", Content: "next turn"},
	})
	if len(sanitized) != 3 {
		t.Fatalf("expected dangling tool call to be stripped, got %+v", sanitized)
	}
	if sanitized[1].Role != "assistant" || len(sanitized[1].ToolCalls) != 0 || strings.TrimSpace(fmt.Sprint(sanitized[1].Content)) != "我来打开浏览器并截图" {
		t.Fatalf("expected assistant text without tool calls, got %+v", sanitized[1])
	}
}

func TestBuildOpenAICompatMessagesStripsDanglingTranscriptToolCall(t *testing.T) {
	messages := BuildOpenAICompatMessages(rtypes.AgentRunContext{
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "take a screenshot"},
			{Type: "tool_call", Role: "assistant", ToolCallID: "call_browser_1", ToolName: "agent-browser", Text: "我来打开浏览器并截图", Args: map[string]any{"action": "screenshot"}},
		},
		Request: core.AgentRunRequest{Message: "next turn"},
	})
	if len(messages) != 3 {
		t.Fatalf("expected dangling transcript tool call to become plain assistant text, got %+v", messages)
	}
	if messages[1].Role != "assistant" || len(messages[1].ToolCalls) != 0 || strings.TrimSpace(fmt.Sprint(messages[1].Content)) != "我来打开浏览器并截图" {
		t.Fatalf("expected assistant text without tool calls, got %+v", messages[1])
	}
	if messages[2].Role != "user" || strings.TrimSpace(fmt.Sprint(messages[2].Content)) != "next turn" {
		t.Fatalf("unexpected final message sequence: %+v", messages)
	}
}

func TestUsageToMapHandlesMissingCompletionDetails(t *testing.T) {
	usage := UsageToMap(openai.Usage{
		PromptTokens:     1,
		CompletionTokens: 2,
		TotalTokens:      3,
	})
	if usage["prompt_tokens"] != 1 || usage["completion_tokens"] != 2 || usage["total_tokens"] != 3 {
		t.Fatalf("unexpected usage map: %+v", usage)
	}
	if _, ok := usage["reasoning_tokens"]; ok {
		t.Fatalf("did not expect reasoning_tokens when completion details are absent: %+v", usage)
	}
}

func TestBuildOpenAICompatToolDefinitionsIncludesCoreSessionAndMemoryTools(t *testing.T) {
	definitions := BuildOpenAICompatToolDefinitions([]tool.Tool{
		tool.NewExecTool(),
		tool.NewMemorySearchTool(),
		tool.NewMemoryGetTool(),
		tool.NewSessionsListTool(),
		tool.NewSessionsHistoryTool(),
		tool.NewSessionsSendTool(),
		tool.NewSessionsSpawnTool(),
		tool.NewSubagentsTool(),
		tool.NewSessionStatusTool(),
	})
	if len(definitions) != 9 {
		t.Fatalf("expected 9 tool definitions, got %d", len(definitions))
	}
	names := map[string]struct{}{}
	for _, definition := range definitions {
		names[definition.Function.Name] = struct{}{}
	}
	for _, expected := range []string{
		"exec",
		"memory_search",
		"memory_get",
		"sessions_list",
		"sessions_history",
		"sessions_send",
		"sessions_spawn",
		"subagents",
		"session_status",
	} {
		if _, ok := names[expected]; !ok {
			t.Fatalf("missing tool schema for %s", expected)
		}
	}
}

func TestSanitizeAnthropicMessagesStripsDanglingToolUseAndMergesUsers(t *testing.T) {
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock("one")),
		anthropic.NewUserMessage(anthropic.NewTextBlock("two")),
		anthropic.NewAssistantMessage(
			anthropic.NewTextBlock("working"),
			anthropic.NewToolUseBlock("toolu_1", map[string]any{"x": 1}, "demo"),
		),
		anthropic.NewUserMessage(anthropic.NewTextBlock("no tool result here")),
	}
	sanitized := SanitizeAnthropicMessages(messages)
	if len(sanitized) != 3 {
		t.Fatalf("expected merged user + repaired assistant + trailing user, got %#v", sanitized)
	}
	if len(sanitized[0].Content) != 2 {
		t.Fatalf("expected merged user content, got %#v", sanitized[0].Content)
	}
	if len(sanitized[1].Content) != 1 || sanitized[1].Content[0].OfText == nil || strings.TrimSpace(sanitized[1].Content[0].OfText.Text) != "working" {
		t.Fatalf("expected dangling tool_use to be stripped, got %#v", sanitized[1].Content)
	}
}

func TestBuildAnthropicMessagesPrunesOldToolResultsAfterTTL(t *testing.T) {
	runCtx := rtypes.AgentRunContext{
		Session: core.SessionResolution{
			SessionID:  "sess-prune",
			SessionKey: "agent:main:main",
			Entry: &core.SessionEntry{
				SessionID:       "sess-prune",
				LastModelCallAt: time.Now().Add(-10 * time.Minute),
			},
		},
		Identity: core.AgentIdentity{
			ContextPruningMode:                 "cache-ttl",
			ContextPruningTTL:                  5 * time.Minute,
			ContextPruningKeepLastAssistants:   1,
			ContextPruningSoftTrimRatio:        1,
			ContextPruningMinPrunableToolChars: 20,
			ContextPruningSoftTrimMaxChars:     20,
			ContextPruningSoftTrimHeadChars:    5,
			ContextPruningSoftTrimTailChars:    5,
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "u1"},
			{Type: "assistant_final", Role: "assistant", Text: "a1", Final: true},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "exec", Text: strings.Repeat("A", 64)},
			{Type: "assistant_final", Role: "assistant", Text: "latest", Final: true},
		},
		Request: core.AgentRunRequest{Message: "continue"},
	}
	messages := BuildAnthropicMessages(runCtx.Transcript, runCtx.Request, runCtx.Identity, runCtx.Session)
	if len(messages) != 5 {
		t.Fatalf("unexpected anthropic message count: %#v", messages)
	}
	toolResultBlock := messages[2].Content[0].OfToolResult
	if toolResultBlock == nil {
		t.Fatalf("expected tool result block, got %#v", messages[2].Content)
	}
	got := extractAnthropicToolResultTextHelper(toolResultBlock)
	if !strings.Contains(got, "[tool result trimmed from 64 chars]") {
		t.Fatalf("expected pruned tool result content, got %q", got)
	}
}

func TestBuildAnthropicMessagesSkipsPruningWithoutEnoughAssistantMessages(t *testing.T) {
	longText := strings.Repeat("B", 64)
	runCtx := rtypes.AgentRunContext{
		Session: core.SessionResolution{
			SessionID:  "sess-prune-skip",
			SessionKey: "agent:main:main",
			Entry: &core.SessionEntry{
				SessionID:       "sess-prune-skip",
				LastModelCallAt: time.Now().Add(-10 * time.Minute),
			},
		},
		Identity: core.AgentIdentity{
			ContextPruningMode:                 "cache-ttl",
			ContextPruningTTL:                  5 * time.Minute,
			ContextPruningKeepLastAssistants:   3,
			ContextPruningSoftTrimRatio:        1,
			ContextPruningMinPrunableToolChars: 20,
			ContextPruningSoftTrimMaxChars:     20,
			ContextPruningSoftTrimHeadChars:    5,
			ContextPruningSoftTrimTailChars:    5,
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "u1"},
			{Type: "assistant_final", Role: "assistant", Text: "a1", Final: true},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "exec", Text: longText},
		},
		Request: core.AgentRunRequest{Message: "continue"},
	}
	messages := BuildAnthropicMessages(runCtx.Transcript, runCtx.Request, runCtx.Identity, runCtx.Session)
	toolResultBlock := messages[2].Content[0].OfToolResult
	if toolResultBlock == nil || extractAnthropicToolResultTextHelper(toolResultBlock) != longText {
		t.Fatalf("expected unpruned tool result, got %#v", messages[2].Content)
	}
}

func TestBuildAnthropicMessagesHardClearsOlderToolResults(t *testing.T) {
	runCtx := rtypes.AgentRunContext{
		Session: core.SessionResolution{
			SessionID:  "sess-prune-clear",
			SessionKey: "agent:main:main",
			Entry: &core.SessionEntry{
				SessionID:       "sess-prune-clear",
				LastModelCallAt: time.Now().Add(-2 * time.Hour),
			},
		},
		Identity: core.AgentIdentity{
			ContextPruningMode:                 "cache-ttl",
			ContextPruningTTL:                  5 * time.Minute,
			ContextPruningKeepLastAssistants:   1,
			ContextPruningSoftTrimRatio:        0,
			ContextPruningHardClearRatio:       1,
			ContextPruningMinPrunableToolChars: 20,
			ContextPruningHardClearEnabled:     true,
			ContextPruningHardClearPlaceholder: "[Old tool result content cleared]",
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "u1"},
			{Type: "assistant_final", Role: "assistant", Text: "a1", Final: true},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "exec", Text: strings.Repeat("C", 64)},
			{Type: "assistant_final", Role: "assistant", Text: "latest", Final: true},
		},
		Request: core.AgentRunRequest{Message: "continue"},
	}
	messages := BuildAnthropicMessages(runCtx.Transcript, runCtx.Request, runCtx.Identity, runCtx.Session)
	toolResultBlock := messages[2].Content[0].OfToolResult
	if toolResultBlock == nil {
		t.Fatalf("expected tool result block, got %#v", messages[2].Content)
	}
	got := extractAnthropicToolResultTextHelper(toolResultBlock)
	if !strings.Contains(got, "[Old tool result content cleared]") {
		t.Fatalf("expected hard-clear placeholder, got %q", got)
	}
}

func TestBuildAnthropicMessagesIncludesImageAttachments(t *testing.T) {
	messages := BuildAnthropicMessages(nil, core.AgentRunRequest{
		Message: "describe this image",
		Attachments: []core.Attachment{{
			Type:     "image",
			Name:     "pixel.png",
			MIMEType: "image/png",
			Content:  []byte("PNGDATA"),
		}},
	}, core.AgentIdentity{}, core.SessionResolution{})
	if len(messages) != 1 {
		t.Fatalf("unexpected anthropic message count: %#v", messages)
	}
	if len(messages[0].Content) != 2 {
		t.Fatalf("expected text + image blocks, got %#v", messages[0].Content)
	}
	if messages[0].Content[0].OfText == nil || strings.TrimSpace(messages[0].Content[0].OfText.Text) != "describe this image" {
		t.Fatalf("unexpected first block: %#v", messages[0].Content[0])
	}
	imageBlock := messages[0].Content[1].OfImage
	if imageBlock == nil || imageBlock.Source.OfBase64 == nil {
		t.Fatalf("expected image block, got %#v", messages[0].Content[1])
	}
	if string(imageBlock.Source.OfBase64.MediaType) != "image/png" || imageBlock.Source.OfBase64.Data != base64.StdEncoding.EncodeToString([]byte("PNGDATA")) {
		t.Fatalf("unexpected image block payload: %#v", imageBlock.Source.OfBase64)
	}
}

func extractAnthropicToolResultTextHelper(block *anthropic.ToolResultBlockParam) string {
	if block == nil {
		return ""
	}
	parts := make([]string, 0, len(block.Content))
	for _, item := range block.Content {
		if item.OfText != nil {
			parts = append(parts, strings.TrimSpace(item.OfText.Text))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
