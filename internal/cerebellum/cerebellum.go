// Package cerebellum implements the safety review layer (小脑).
//
// The cerebellum acts as a security watchdog and local configuration translator,
// running a small quantized model (0.8B–1.5B) entirely offline. It is
// integrated directly into the agent architecture:
//
//   - Safety Review: In normal (usage) mode, the cloud LLM's tool_call
//     instructions pass through the cerebellum for semantic safety review
//     before actual tool execution.
//   - Help / Configuration Mode: When the cloud LLM is offline or not
//     configured, the cerebellum drives the conversation to help the user
//     set up their LLM provider.
//
// The cerebellum delegates model lifecycle to localmodel.Manager and
// focuses on safety review logic and prompt construction.
package cerebellum

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/localmodel"
)

// Status constants — re-exported from localmodel for convenience.
const (
	StatusRunning  = localmodel.StatusRunning
	StatusStopped  = localmodel.StatusStopped
	StatusStarting = localmodel.StatusStarting
	StatusStopping = localmodel.StatusStopping
	StatusError    = localmodel.StatusError
)

// ErrNotConfigured is returned when the cerebellum manager is not available.
var ErrNotConfigured = errors.New("cerebellum is not configured")

// Re-export types from localmodel for backward compatibility.
type ModelInfo = localmodel.ModelInfo
type ModelPreset = localmodel.ModelPreset
type DownloadProgress = localmodel.DownloadProgress
type ModelBackend = localmodel.ModelBackend

// BuiltinModelCatalog is the default catalog for cerebellum models.
var BuiltinModelCatalog = localmodel.BuiltinCerebellumCatalog

// sensitiveKeywords — re-exported from localmodel.
var sensitiveKeywords = localmodel.SensitiveKeywords

// chatMLArtifactRe matches ChatML special tokens that may leak through from
// the model: <|im_start|>role, <|im_end|>, <|endoftext|>, and orphaned
// </think> close tags that aren't part of a <think>...</think> pair.
var chatMLArtifactRe = regexp.MustCompile(`<\|im_start\|>[a-z]*|<\|im_end\|>|<\|endoftext\|>|</think>`)

// cleanModelOutput strips ChatML special-token artifacts and orphaned
// </think> close tags from model output. This is needed because:
//   - The CGO Infer() stop strings only catch <|im_end|> as a suffix;
//     <|im_start|>user etc. can leak through.
//   - Models sometimes emit </think> without a matching <think>.
func cleanModelOutput(s string) string {
	s = chatMLArtifactRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// ---------------------------------------------------------------------------
// Safety Review types (integrated into agent tool-execution pipeline)
// ---------------------------------------------------------------------------

// ToolCallReviewRequest describes a tool call to be safety-reviewed by the
// cerebellum before execution. This is called from the agent loop, NOT via
// an HTTP endpoint.
type ToolCallReviewRequest struct {
	UserMessage string         // the original user message that triggered this tool call
	ToolName    string         // the tool being invoked
	ToolParams  map[string]any // tool call parameters
	SessionKey  string         // optional session context
	AgentID     string         // the originating agent
}

// ToolCallReviewResult contains the outcome of a safety review.
// Verdict is one of: "approve", "flag", "reject".
type ToolCallReviewResult struct {
	Verdict string `json:"verdict"` // "approve" | "flag" | "reject"
	Reason  string `json:"reason"`  // human-readable explanation
	Risk    string `json:"risk"`    // "none" | "low" | "medium" | "high"
}

// ReviewRequest describes an instruction to be safety-reviewed by the
// cerebellum before execution.
// Deprecated: Use ToolCallReviewRequest for agent-integrated review.
type ReviewRequest struct {
	Instruction string // the instruction text to review
	SessionKey  string // optional session context
	AgentID     string // the originating agent
}

// ReviewResult contains the outcome of a safety review.
// Deprecated: Use ToolCallReviewResult for agent-integrated review.
type ReviewResult struct {
	Safe   bool   // true if the instruction is considered safe
	Reason string // human-readable explanation
	Risk   string // risk level: "none", "low", "medium", "high"
}

// ---------------------------------------------------------------------------
// Help / Configuration Mode types
// ---------------------------------------------------------------------------

// HelpRequest describes a natural-language configuration query to be
// translated by the cerebellum into actionable local configuration.
type HelpRequest struct {
	Query      string // the user's natural-language query
	Context    string // optional surrounding context (current config, etc.)
	SessionKey string // optional session context
}

// HelpResult contains the cerebellum's configuration suggestion.
type HelpResult struct {
	Answer     string         // natural-language explanation
	Suggestion map[string]any // optional structured config suggestion
}

// ---------------------------------------------------------------------------
// Model Catalog — built-in model presets with download URLs
// ---------------------------------------------------------------------------

// Manager manages the cerebellum safety review layer.
// It delegates model lifecycle to a localmodel.Manager instance.
type Manager struct {
	local *localmodel.Manager
}

// BuiltinModelCatalogPresets returns the built-in cerebellum catalog.
// Alias kept for backward compatibility in tests.
func BuiltinModelCatalogPresets() []localmodel.ModelPreset {
	return localmodel.BuiltinCerebellumCatalog
}

// NewManager creates a new cerebellum Manager.
func NewManager(cfg config.CerebellumConfig) *Manager {
	lm := localmodel.NewManager(localmodel.Config{
		ModelID:        cfg.ModelID,
		ModelsDir:      cfg.ModelsDir,
		Threads:        cfg.Threads,
		ContextSize:    cfg.ContextSize,
		GpuLayers:      cfg.GpuLayers,
		Sampling:       configSamplingToLocal(cfg.Sampling),
		EnableThinking: false, // Cerebellum review never uses thinking mode.
	}, BuiltinModelCatalog)
	return &Manager{local: lm}
}

// configSamplingToLocal converts config.SamplingConfig to localmodel.SamplingParams.
func configSamplingToLocal(sc *config.SamplingConfig) *localmodel.SamplingParams {
	if sc == nil {
		return nil
	}
	return &localmodel.SamplingParams{
		Temp:           sc.Temp,
		TopP:           sc.TopP,
		TopK:           sc.TopK,
		MinP:           sc.MinP,
		TypicalP:       sc.TypicalP,
		RepeatLastN:    sc.RepeatLastN,
		PenaltyRepeat:  sc.PenaltyRepeat,
		PenaltyFreq:    sc.PenaltyFreq,
		PenaltyPresent: sc.PenaltyPresent,
	}
}

// Local returns the underlying localmodel.Manager.
func (m *Manager) Local() *localmodel.Manager {
	return m.local
}

// SetDynamicHTTPClient sets the dynamic HTTP client for model downloads.
func (m *Manager) SetDynamicHTTPClient(dc *infra.DynamicHTTPClient) {
	m.local.SetDynamicHTTPClient(dc)
}

// Status returns the current lifecycle status.
func (m *Manager) Status() string { return m.local.Status() }

// ModelID returns the currently selected model ID.
func (m *Manager) ModelID() string { return m.local.ModelID() }

// Models returns the list of discovered models.
func (m *Manager) Models() []localmodel.ModelInfo { return m.local.Models() }

// LastError returns the last error message.
func (m *Manager) LastError() string { return m.local.LastError() }

// Start begins running the cerebellum model.
func (m *Manager) Start() error { return m.local.Start() }

// Stop shuts down the running cerebellum model.
func (m *Manager) Stop() error { return m.local.Stop() }

// Restart stops and then starts the cerebellum model.
func (m *Manager) Restart() error { return m.local.Restart() }

// WaitReady blocks until any pending lifecycle operation finishes.
func (m *Manager) WaitReady() string { return m.local.WaitReady() }

// SelectModel sets the active model ID.
func (m *Manager) SelectModel(modelID string) error { return m.local.SelectModel(modelID) }

// ClearSelectedModel clears the active default model.
func (m *Manager) ClearSelectedModel() error { return m.local.ClearSelectedModel() }

// DeleteModel removes a downloaded model file.
func (m *Manager) DeleteModel(modelID string) error { return m.local.DeleteModel(modelID) }

// Catalog returns the built-in model catalog.
func (m *Manager) Catalog() []localmodel.ModelPreset { return m.local.Catalog() }

// DownloadModel downloads a model from the catalog.
func (m *Manager) DownloadModel(presetID string, httpClient *http.Client) error {
	return m.local.DownloadModel(presetID, httpClient)
}

// CancelDownload cancels the active model download.
func (m *Manager) CancelDownload() error { return m.local.CancelDownload() }

// GetSamplingParams returns the current sampling parameters.
func (m *Manager) GetSamplingParams() localmodel.SamplingParams {
	return m.local.GetSamplingParams()
}

// SetSamplingParams updates the sampling parameters.
func (m *Manager) SetSamplingParams(sp localmodel.SamplingParams) error {
	return m.local.SetSamplingParams(sp)
}

// Threads returns the current inference thread count.
func (m *Manager) Threads() int { return m.local.Threads() }

// ContextSize returns the current context window size.
func (m *Manager) ContextSize() int { return m.local.ContextSize() }

// GpuLayers returns the current GPU layers setting.
func (m *Manager) GpuLayers() int { return m.local.GpuLayers() }

// UpdateRuntimeParams updates threads, contextSize, and gpuLayers.
func (m *Manager) UpdateRuntimeParams(threads, contextSize, gpuLayers int) error {
	return m.local.UpdateRuntimeParams(threads, contextSize, gpuLayers)
}

// UpdateAllParams updates sampling, threads, contextSize, and gpuLayers
// atomically, triggering at most one restart.
func (m *Manager) UpdateAllParams(sp *localmodel.SamplingParams, threads, contextSize, gpuLayers int) error {
	return m.local.UpdateAllParams(sp, threads, contextSize, gpuLayers)
}

// Snapshot returns the current state for API responses.
func (m *Manager) Snapshot(enabled bool) Snapshot {
	snap := m.local.Snapshot()
	return Snapshot{
		Enabled:          enabled,
		Status:           snap.Status,
		ModelID:          snap.ModelID,
		Models:           snap.Models,
		LastError:        snap.LastError,
		Catalog:          snap.Catalog,
		DownloadProgress: snap.DownloadProgress,
		Sampling:         snap.Sampling,
		Threads:          snap.Threads,
		ContextSize:      snap.ContextSize,
		GpuLayers:        snap.GpuLayers,
	}
}

// Snapshot is a point-in-time copy of cerebellum state.
type Snapshot struct {
	Enabled          bool
	Status           string
	ModelID          string
	Models           []localmodel.ModelInfo
	LastError        string
	Catalog          []localmodel.ModelPreset
	DownloadProgress *localmodel.DownloadProgress
	Sampling         localmodel.SamplingParams
	Threads          int
	ContextSize      int
	GpuLayers        int
}

// Help uses the local model to translate a natural-language query into
// actionable configuration suggestions. When the stub inferencer is in
// use, a generic pass-through answer is returned.
func (m *Manager) Help(req HelpRequest) (HelpResult, error) {
	status := m.local.Status()

	if status != StatusRunning {
		return HelpResult{}, fmt.Errorf("cerebellum is not running (status: %s)", status)
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		return HelpResult{Answer: "no query provided"}, nil
	}

	prompt := buildHelpPrompt(query, strings.TrimSpace(req.Context))
	slog.Debug("[cerebellum] Help: prompt built",
		"query", query,
		"prompt_len", len(prompt))

	output, err := m.inferSync(prompt, 512)
	if err != nil {
		slog.Warn("[cerebellum] Help: inference failed",
			"query", query,
			"error", err)
		return HelpResult{}, fmt.Errorf("inference failed: %w", err)
	}

	slog.Debug("[cerebellum] Help: raw output",
		"query", query,
		"raw_output", output)

	result := parseHelpOutput(output, query)

	slog.Info("[cerebellum] Help: result",
		"query", query,
		"answer_len", len(result.Answer))

	return result, nil
}

// ---------------------------------------------------------------------------
// Agent-integrated safety review (called from tool execution pipeline)
// ---------------------------------------------------------------------------

// ReviewToolCall performs a semantic safety review of a tool call from the
// cloud LLM. This is the primary review entry point called from the agent
// tool execution pipeline (not via HTTP). When the cerebellum is not running,
// an automatic approve with degradation notice is returned.
func (m *Manager) ReviewToolCall(req ToolCallReviewRequest) (ToolCallReviewResult, error) {
	status := m.local.Status()

	if status != StatusRunning {
		// Graceful degradation: approve when cerebellum not available.
		return ToolCallReviewResult{
			Verdict: "approve",
			Reason:  "cerebellum not running; degraded to rule-only check",
			Risk:    "none",
		}, nil
	}

	toolName := strings.TrimSpace(req.ToolName)
	if toolName == "" {
		return ToolCallReviewResult{Verdict: "approve", Reason: "no tool name", Risk: "none"}, nil
	}

	prompt := buildToolCallReviewPrompt(req.UserMessage, toolName, req.ToolParams)
	slog.Debug("[cerebellum] ReviewToolCall: prompt built",
		"tool", toolName,
		"params", req.ToolParams,
		"user_message", req.UserMessage,
		"prompt", prompt)

	output, err := m.inferSync(prompt, 4096)
	if err != nil {
		slog.Warn("[cerebellum] ReviewToolCall: inference failed, degrading to approve",
			"tool", toolName,
			"error", err)
		// Graceful degradation on inference failure.
		return ToolCallReviewResult{
			Verdict: "approve",
			Reason:  "inference failed; degraded to rule-only check",
			Risk:    "none",
		}, nil
	}

	slog.Debug("[cerebellum] ReviewToolCall: raw output",
		"tool", toolName,
		"raw_output", output,
		"raw_output_len", len(output))

	result := parseToolCallReviewOutput(output)

	slog.Info("[cerebellum] ReviewToolCall: result",
		"tool", toolName,
		"verdict", result.Verdict,
		"reason", result.Reason,
		"risk", result.Risk)

	return result, nil
}

// ShouldReviewToolCall determines whether a tool call needs cerebellum review.
// Returns false for configuration mode, low-risk read-only tools, and when
// the cerebellum is not enabled.
func ShouldReviewToolCall(toolName string, toolParams map[string]any, isElevated bool, isConfigMode bool) bool {
	if isConfigMode {
		return false
	}
	if isLowRiskReadOnly(toolName) {
		if localmodel.ContainsSensitiveKeywords(toolParams) {
			return true
		}
		return false
	}
	if isElevated {
		return true
	}
	if localmodel.ContainsSensitiveKeywords(toolParams) {
		return true
	}
	return true
}

// isLowRiskReadOnly returns true for tools that are considered read-only
// and low risk (e.g., memory search, session listing).
func isLowRiskReadOnly(toolName string) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	lowRiskTools := map[string]bool{
		"memory_search":    true,
		"memory_get":       true,
		"sessions_list":    true,
		"sessions_history": true,
		"session_status":   true,
	}
	return lowRiskTools[lower]
}

// buildToolCallReviewPrompt constructs the detailed prompt for tool call
// safety review per the design spec.
func buildToolCallReviewPrompt(userMessage, toolName string, toolParams map[string]any) string {
	paramsStr := "{}"
	if len(toolParams) > 0 {
		if b, err := json.MarshalIndent(toolParams, "  ", "  "); err == nil {
			paramsStr = string(b)
		}
	}
	// 必须关闭思考模式
	systemMsg := `/no_think
你是一个安全审查助手。你的任务是审查 AI Agent 工具调用指令是否安全。
## 审查要点
1. 指令是否与用户原始请求的意图一致？
2. 是否存在数据外泄风险 (如 curl 上传敏感文件)？
3. 是否存在超出用户预期的破坏性操作 (如删除非目标文件)？
4. 参数中是否包含注入攻击 (如命令注入、路径遍历)？
5. 操作范围是否限制在沙盒授权目录内？

## 输出格式 (严格 JSON)
{"verdict": "approve|flag|reject", "reason": "简短原因", "risk": "none|low|medium|high"}`

	userMsg := fmt.Sprintf("## 用户原始请求\n%s\n\n## Agent 计划执行的工具调用\n- 工具名: %s\n- 参数:\n  %s",
		userMessage, toolName, paramsStr)

	return fmt.Sprintf("<|im_start|>system\n%s<|im_end|>\n<|im_start|>user\n%s<|im_end|>\n<|im_start|>assistant\n",
		systemMsg, userMsg)
}

// parseToolCallReviewOutput interprets the model output into a ToolCallReviewResult.
// When output is empty (stub mode), the tool call is approved (pass-through).
// Any <think>...</think> blocks in the output are stripped — only the final
// content is used as the review result.
func parseToolCallReviewOutput(output string) ToolCallReviewResult {
	output = strings.TrimSpace(output)
	if output == "" {
		return ToolCallReviewResult{
			Verdict: "approve",
			Reason:  "no local model loaded; tool call passed through",
			Risk:    "none",
		}
	}

	// Strip thinking blocks — cerebellum review uses only the final content.
	output, _ = stripThinkBlocks(output)
	// Clean ChatML artifacts from model output.
	output = cleanModelOutput(output)
	if output == "" {
		// Model output was only thinking/artifacts with no final verdict — approve by default.
		return ToolCallReviewResult{
			Verdict: "approve",
			Reason:  "model produced only reasoning with no verdict",
			Risk:    "none",
		}
	}

	// Try to parse as JSON first.
	var result ToolCallReviewResult
	// Try to find JSON in the output.
	jsonStart := strings.Index(output, "{")
	jsonEnd := strings.LastIndex(output, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		jsonStr := output[jsonStart : jsonEnd+1]
		if err := json.Unmarshal([]byte(jsonStr), &result); err == nil {
			// Normalize verdict.
			result.Verdict = strings.ToLower(strings.TrimSpace(result.Verdict))
			if result.Verdict != "approve" && result.Verdict != "flag" && result.Verdict != "reject" {
				result.Verdict = "flag"
			}
			result.Risk = strings.ToLower(strings.TrimSpace(result.Risk))
			if result.Risk == "" {
				result.Risk = "medium"
			}
			return result
		}
	}

	// Fallback: heuristic parsing for non-JSON output.
	lower := strings.ToLower(output)
	verdict := "approve"
	if strings.Contains(lower, "reject") {
		verdict = "reject"
	} else if strings.Contains(lower, "flag") || strings.Contains(lower, "unsafe") {
		verdict = "flag"
	}

	risk := "none"
	for _, level := range []string{"high", "medium", "low"} {
		if strings.Contains(lower, level) {
			risk = level
			break
		}
	}

	reason := output
	if idx := strings.IndexByte(output, '\n'); idx >= 0 {
		reason = strings.TrimSpace(output[:idx])
	}

	return ToolCallReviewResult{
		Verdict: verdict,
		Reason:  reason,
		Risk:    risk,
	}
}

// buildReviewPrompt constructs the prompt sent to the model for instruction review.
func buildReviewPrompt(instruction string) string {
	systemMsg := "You are a safety review assistant. Review instructions for safety. " +
		"Respond with SAFE or UNSAFE followed by a brief reason and risk level (none/low/medium/high)."
	userMsg := fmt.Sprintf("Review the following instruction:\n\n%s", instruction)
	return fmt.Sprintf("<|im_start|>system\n%s<|im_end|>\n<|im_start|>user\n%s<|im_end|>\n<|im_start|>assistant\n",
		systemMsg, userMsg)
}

// parseReviewOutput interprets the model output into a ReviewResult.
// Any <think>...</think> blocks in the output are stripped — only the final
// content is used as the review result.
func parseReviewOutput(output, instruction string) ReviewResult {
	output = strings.TrimSpace(output)
	if output == "" {
		return ReviewResult{
			Safe:   true,
			Reason: "no local model loaded; instruction passed through",
			Risk:   "none",
		}
	}
	// Strip thinking blocks — cerebellum review uses only the final content.
	output, _ = stripThinkBlocks(output)
	// Clean ChatML artifacts from model output.
	output = cleanModelOutput(output)
	if output == "" {
		// Model output was only thinking/artifacts with no conclusion — treat as safe.
		return ReviewResult{
			Safe:   true,
			Reason: "model produced only reasoning with no conclusion",
			Risk:   "none",
		}
	}
	lower := strings.ToLower(output)
	safe := !strings.Contains(lower, "unsafe")
	risk := "none"
	for _, level := range []string{"high", "medium", "low"} {
		if strings.Contains(lower, level) {
			risk = level
			break
		}
	}
	reason := output
	if idx := strings.IndexByte(output, '\n'); idx >= 0 {
		reason = strings.TrimSpace(output[:idx])
	}
	return ReviewResult{
		Safe:   safe,
		Reason: reason,
		Risk:   risk,
	}
}

// buildHelpPrompt constructs the prompt for help/translation.
func buildHelpPrompt(query, context string) string {
	systemMsg := "You are a helpful configuration assistant. Translate user requests into clear, actionable configuration suggestions. Be concise."
	var userMsg string
	if context != "" {
		userMsg = fmt.Sprintf("Current context:\n%s\n\nUser request: %s", context, query)
	} else {
		userMsg = query
	}
	return fmt.Sprintf("<|im_start|>system\n%s<|im_end|>\n<|im_start|>user\n%s<|im_end|>\n<|im_start|>assistant\n",
		systemMsg, userMsg)
}

// parseHelpOutput interprets the model output into a HelpResult.
// Any <think>...</think> blocks in the output are stripped — only the final
// content is used.
func parseHelpOutput(output, query string) HelpResult {
	output = strings.TrimSpace(output)
	if output == "" {
		return HelpResult{
			Answer: "no local model loaded; unable to provide configuration suggestion for: " + query,
		}
	}
	// Strip thinking blocks — use only the final content.
	output, _ = stripThinkBlocks(output)
	// Clean ChatML artifacts from model output.
	output = cleanModelOutput(output)
	if output == "" {
		return HelpResult{
			Answer: "model produced empty response for: " + query,
		}
	}
	return HelpResult{
		Answer: output,
	}
}

// ---------------------------------------------------------------------------
// inferSync — synchronous inference via CreateChatCompletionStream
// ---------------------------------------------------------------------------

// inferSync runs a synchronous, single-shot inference through the
// localmodel.Manager.CreateChatCompletionStream channel API. It collects
// all chunks until the channel is closed and returns the concatenated text.
// maxTokens sets the generation token budget.
func (m *Manager) inferSync(prompt string, maxTokens int) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	numPredict := maxTokens
	req := localmodel.ChatCompletionRequest{
		Model:     "cerebellum",
		Stream:    true,
		MaxTokens: &numPredict,
		RawPrompt: prompt,
	}

	ch, err := m.local.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for chunk := range ch {
		for _, choice := range chunk.Choices {
			if text := choice.Delta.Content; text != "" {
				sb.WriteString(text)
			}
		}
	}

	return strings.TrimSpace(sb.String()), nil
}

// ---------------------------------------------------------------------------
// stripThinkBlocks — remove <think>...</think> blocks from completed text
// ---------------------------------------------------------------------------

// stripThinkBlocks removes all <think>...</think> blocks from text and returns
// the cleaned content along with the concatenated thinking text. Multiple
// think blocks are supported. An unclosed <think> tag causes everything from
// <think> to the end to be treated as thinking.
func stripThinkBlocks(text string) (content, thinking string) {
	var contentBuf, thinkBuf strings.Builder
	remaining := text

	for {
		startIdx := strings.Index(remaining, "<think>")
		if startIdx < 0 {
			contentBuf.WriteString(remaining)
			break
		}

		contentBuf.WriteString(remaining[:startIdx])

		afterStart := remaining[startIdx+len("<think>"):]
		endIdx := strings.Index(afterStart, "</think>")
		if endIdx < 0 {
			if thinkBuf.Len() > 0 {
				thinkBuf.WriteString("\n")
			}
			thinkBuf.WriteString(strings.TrimSpace(afterStart))
			break
		}

		if thinkBuf.Len() > 0 {
			thinkBuf.WriteString("\n")
		}
		thinkBuf.WriteString(strings.TrimSpace(afterStart[:endIdx]))
		remaining = afterStart[endIdx+len("</think>"):]
	}

	content = strings.TrimSpace(contentBuf.String())
	thinking = strings.TrimSpace(thinkBuf.String())
	return
}
