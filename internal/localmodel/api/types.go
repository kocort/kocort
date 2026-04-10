// Package api provides shared OpenAI-compatible API types used across the
// localmodel subsystem. These types are the canonical definitions for the
// wire format (HTTP request/response structs) and are imported by engine/,
// serve/, and the localmodel root package.
//
// This package has no dependency on engine or serve — it is a pure types
// package at the bottom of the dependency graph (Layer 0).
package api

import (
	"encoding/json"

	"github.com/kocort/kocort/internal/localmodel/chatfmt"
)

// ── Tool type aliases ────────────────────────────────────────────────────────
// Canonical definitions live in chatfmt; we re-export them here so that
// API-layer consumers import only this package.

type ToolCall = chatfmt.ToolCall
type ToolFunction = chatfmt.ToolFunction
type Tool = chatfmt.Tool
type ToolDefFunc = chatfmt.ToolDefFunc

// ── OpenAI-Compatible Chat Types ─────────────────────────────────────────────

// ChatMessage is an OpenAI-compatible conversation message.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"` // string or []ContentPart
	Reasoning  string     `json:"reasoning,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ContentPart is a typed content part (text or image_url).
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL holds a reference to an image.
type ImageURL struct {
	URL string `json:"url"`
}

// ChatCompletionRequest is the OpenAI /v1/chat/completions request.
type ChatCompletionRequest struct {
	Model            string          `json:"model"`
	Messages         []ChatMessage   `json:"messages"`
	Stream           bool            `json:"stream"`
	StreamOptions    *StreamOptions  `json:"stream_options,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	Stop             any             `json:"stop,omitempty"`
	ResponseFormat   *ResponseFormat `json:"response_format,omitempty"`
	Logprobs         *bool           `json:"logprobs,omitempty"`
	TopLogprobs      int             `json:"top_logprobs,omitempty"`
	Tools            []Tool          `json:"tools,omitempty"`
	ToolChoice       any             `json:"tool_choice,omitempty"`
	Reasoning        *ReasoningParam `json:"reasoning,omitempty"`
	ReasoningEffort  *string         `json:"reasoning_effort,omitempty"`

	// EnableThinking overrides the engine-level thinking default:
	//   *true  → enable thinking (inject <think> prefix)
	//   *false → disable thinking (inject empty <think></think>)
	//   nil    → use engine default
	EnableThinking *bool `json:"-"`

	// RawPrompt, when non-empty, bypasses message-based prompt building and
	// uses this string directly as the prompt.
	RawPrompt string `json:"-"`

	// SamplingOverride, when non-nil, replaces the derived sampling params.
	SamplingOverride *SamplingConfig `json:"-"`
}

// StreamOptions controls SSE stream behavior.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ReasoningParam controls reasoning behavior (OpenAI-compat).
type ReasoningParam struct {
	Effort string `json:"effort,omitempty"`
}

// ResponseFormat specifies output format constraints.
type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

// ── Chat Completion Response ─────────────────────────────────────────────────

// ChatChoice is a choice in a non-streaming chat completion response.
type ChatChoice struct {
	Index        int           `json:"index"`
	Message      ChatMessage   `json:"message"`
	FinishReason *string       `json:"finish_reason"`
	Logprobs     *ChatLogprobs `json:"logprobs,omitempty"`
}

// ChatLogprobs wraps OpenAI logprobs.
type ChatLogprobs struct {
	Content []OpenAILogprob `json:"content"`
}

// OpenAILogprob is a single logprob in OpenAI format.
type OpenAILogprob struct {
	Token       string               `json:"token"`
	Logprob     float64              `json:"logprob"`
	TopLogprobs []OpenAITokenLogprob `json:"top_logprobs,omitempty"`
}

// OpenAITokenLogprob is a top-K alternative token logprob.
type OpenAITokenLogprob struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

// ChatCompletionUsage holds token usage statistics.
type ChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionResponse is the non-streaming OpenAI chat response.
type ChatCompletionResponse struct {
	ID                string              `json:"id"`
	Object            string              `json:"object"`
	Created           int64               `json:"created"`
	Model             string              `json:"model"`
	SystemFingerprint string              `json:"system_fingerprint"`
	Choices           []ChatChoice        `json:"choices"`
	Usage             ChatCompletionUsage `json:"usage,omitempty"`
}

// ── Streaming Chunks ─────────────────────────────────────────────────────────

// ChunkDelta is the delta content in a streaming chunk.
type ChunkDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ChunkChoice is a choice in a streaming chunk.
type ChunkChoice struct {
	Index        int           `json:"index"`
	Delta        ChunkDelta    `json:"delta"`
	FinishReason *string       `json:"finish_reason"`
	Logprobs     *ChatLogprobs `json:"logprobs,omitempty"`
}

// ChatCompletionChunk is a single SSE chunk.
type ChatCompletionChunk struct {
	ID                string               `json:"id"`
	Object            string               `json:"object"`
	Created           int64                `json:"created"`
	Model             string               `json:"model"`
	SystemFingerprint string               `json:"system_fingerprint"`
	Choices           []ChunkChoice        `json:"choices"`
	Usage             *ChatCompletionUsage `json:"usage,omitempty"`
}

// ── Sampling Configuration ───────────────────────────────────────────────────

// SamplingConfig controls token sampling behavior.
type SamplingConfig struct {
	TopK          int     `json:"top_k"`
	TopP          float32 `json:"top_p"`
	MinP          float32 `json:"min_p"`
	TypicalP      float32 `json:"typical_p"`
	Temperature   float32 `json:"temperature"`
	RepeatLastN   int     `json:"repeat_last_n"`
	RepeatPenalty float32 `json:"repeat_penalty"`
	FreqPenalty   float32 `json:"frequency_penalty"`
	PresPenalty   float32 `json:"presence_penalty"`
	Seed          uint32  `json:"seed"`
	Grammar       string  `json:"grammar,omitempty"`
}

// DefaultSamplingConfig returns sensible defaults.
func DefaultSamplingConfig() SamplingConfig {
	return SamplingConfig{
		TopK:          40,
		TopP:          0.9,
		MinP:          0.01,
		TypicalP:      1.0,
		Temperature:   0.8,
		RepeatLastN:   64,
		RepeatPenalty: 1.3,
	}
}

// ── Logprob Types ────────────────────────────────────────────────────────────

// TokenLogprob holds the log probability of a single token.
type TokenLogprob struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

// LogprobEntry holds a generated token's logprob plus top-K alternatives.
type LogprobEntry struct {
	TokenLogprob
	TopLogprobs []TokenLogprob `json:"top_logprobs,omitempty"`
}

// ── Image Data ───────────────────────────────────────────────────────────────

// ImageData holds image binary data for multimodal inference.
type ImageData struct {
	ID   int    `json:"id"`
	Data []byte `json:"data"`
}

// ── OpenAI Models ────────────────────────────────────────────────────────────

// ModelEntry is a single model in the /v1/models list.
type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelList is the response for GET /v1/models.
type ModelList struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

// ── Load/Health Types ────────────────────────────────────────────────────────

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status   string  `json:"status"`
	Progress float32 `json:"progress,omitempty"`
}
