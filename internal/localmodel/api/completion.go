package api

import "time"

// ── Native Completion Types ──────────────────────────────────────────────────

// CompletionRequest is the request body for the native /completion endpoint.
type CompletionRequest struct {
	Prompt      string          `json:"prompt"`
	Images      []ImageData     `json:"images,omitempty"`
	Grammar     string          `json:"grammar,omitempty"`
	Options     *CompletionOpts `json:"options,omitempty"`
	Shift       bool            `json:"shift,omitempty"`
	Truncate    bool            `json:"truncate,omitempty"`
	Logprobs    bool            `json:"logprobs,omitempty"`
	TopLogprobs int             `json:"top_logprobs,omitempty"`
}

// CompletionOpts controls inference behavior for native completions.
type CompletionOpts struct {
	NumPredict       int      `json:"num_predict,omitempty"`
	Temperature      float32  `json:"temperature,omitempty"`
	TopK             int      `json:"top_k,omitempty"`
	TopP             float32  `json:"top_p,omitempty"`
	MinP             float32  `json:"min_p,omitempty"`
	TypicalP         float32  `json:"typical_p,omitempty"`
	RepeatLastN      int      `json:"repeat_last_n,omitempty"`
	RepeatPenalty    float32  `json:"repeat_penalty,omitempty"`
	FrequencyPenalty float32  `json:"frequency_penalty,omitempty"`
	PresencePenalty  float32  `json:"presence_penalty,omitempty"`
	Seed             int      `json:"seed,omitempty"`
	Stop             []string `json:"stop,omitempty"`
	NumKeep          int      `json:"num_keep,omitempty"`
}

// CompletionChunk is a single NDJSON chunk in the native completion response.
type CompletionChunk struct {
	Content            string         `json:"content"`
	Done               bool           `json:"done"`
	DoneReason         string         `json:"done_reason,omitempty"`
	PromptEvalCount    int            `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration time.Duration  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int            `json:"eval_count,omitempty"`
	EvalDuration       time.Duration  `json:"eval_duration,omitempty"`
	Logprobs           []LogprobEntry `json:"logprobs,omitempty"`
}

// ── OpenAI Text Completions ──────────────────────────────────────────────────

// TextCompletionRequest is the OpenAI /v1/completions request.
type TextCompletionRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        float64  `json:"top_p,omitempty"`
	Stream      bool     `json:"stream"`
	Stop        any      `json:"stop,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
	Suffix      string   `json:"suffix,omitempty"`
}

// TextCompletionChoice is a choice in the text completion response.
type TextCompletionChoice struct {
	Text         string  `json:"text"`
	Index        int     `json:"index"`
	FinishReason *string `json:"finish_reason"`
}

// TextCompletionResponse is the non-streaming text completion response.
type TextCompletionResponse struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"`
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	SystemFingerprint string                 `json:"system_fingerprint"`
	Choices           []TextCompletionChoice `json:"choices"`
	Usage             ChatCompletionUsage    `json:"usage,omitempty"`
}

// TextCompletionChunk is a single SSE chunk for text completions.
type TextCompletionChunk struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"`
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	SystemFingerprint string                 `json:"system_fingerprint"`
	Choices           []TextCompletionChoice `json:"choices"`
	Usage             *ChatCompletionUsage   `json:"usage,omitempty"`
}
