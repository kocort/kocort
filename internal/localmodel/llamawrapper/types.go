package llamawrapper

import (
	"encoding/json"
	"time"
)

// ── Engine Configuration ─────────────────────────────────────────────────────

// EngineConfig holds all parameters needed to initialize the inference engine.
type EngineConfig struct {
	// ModelPath is the path to the GGUF model file.
	ModelPath string

	// MmprojPath is the optional path to a vision projector (mmproj) GGUF file.
	// When set, the engine loads a multimodal context enabling image input.
	MmprojPath string

	// ContextSize is the total KV cache context window (default: 2048).
	ContextSize int

	// BatchSize is the max tokens per decode batch per sequence (default: 512).
	BatchSize int

	// Parallel is the number of simultaneous inference sequences (default: 1).
	Parallel int

	// Threads is the number of CPU threads for inference (default: runtime.NumCPU()).
	Threads int

	// GPULayers is how many layers to offload to GPU (-1 = all, 0 = none).
	GPULayers int

	// MainGPU selects which GPU to use when multiple are available.
	MainGPU int

	// UseMmap enables memory-mapped model loading (default: true).
	UseMmap bool

	// FlashAttention controls flash attention: -1=auto, 0=disabled, 1=enabled.
	FlashAttention int

	// KVCacheType sets the quantization for the KV cache ("f16", "q8_0", "q4_0").
	KVCacheType string

	// EnableThinking enables <think> block reasoning by default for all requests.
	EnableThinking bool
}

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

// ── Server Configuration ─────────────────────────────────────────────────────

// ServerConfig holds the HTTP server configuration.
type ServerConfig struct {
	// Addr is the listen address (e.g. "127.0.0.1:8080" or ":0" for ephemeral).
	Addr string

	// EngineConfig configures the underlying inference engine.
	EngineConfig EngineConfig
}

// ── Engine Status ────────────────────────────────────────────────────────────

// EngineStatus represents the lifecycle state of the engine.
type EngineStatus int

const (
	StatusCreated EngineStatus = iota // Engine created, no model loaded
	StatusLoading                     // Model is being loaded
	StatusReady                       // Model loaded, ready for inference
	StatusClosed                      // Engine has been shut down
)

func (s EngineStatus) String() string {
	switch s {
	case StatusCreated:
		return "created"
	case StatusLoading:
		return "loading"
	case StatusReady:
		return "ready"
	case StatusClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// ── Completion Done Reason ───────────────────────────────────────────────────

// DoneReason indicates why a completion sequence finished.
type DoneReason int

const (
	DoneStop       DoneReason = iota // Natural EOS or stop sequence matched
	DoneLength                       // Hit max_tokens / num_predict limit
	DoneDisconnect                   // Client disconnected or context cancelled
)

func (d DoneReason) String() string {
	switch d {
	case DoneStop:
		return "stop"
	case DoneLength:
		return "length"
	default:
		return ""
	}
}

// ── Internal Types ───────────────────────────────────────────────────────────

// input represents a single slot in the prompt: either a token id or an embedding vector.
type input struct {
	token int
	embed []float32
}

// fragment is what flows through the sequence response channel.
type fragment struct {
	text     string
	logprobs []LogprobEntry
}

// ImageData holds image binary data for multimodal inference.
type ImageData struct {
	ID   int    `json:"id"`
	Data []byte `json:"data"`
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

// ── Load/Health Types ────────────────────────────────────────────────────────

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status   string  `json:"status"`
	Progress float32 `json:"progress,omitempty"`
}

// EmbeddingRequest is the request body for POST /embedding.
type EmbeddingRequest struct {
	Content string `json:"content"`
}

// EmbeddingResponse is returned by POST /embedding.
type EmbeddingResponse struct {
	Embedding       []float32 `json:"embedding"`
	PromptEvalCount int       `json:"prompt_eval_count"`
}

// ── OpenAI-Compatible Types ──────────────────────────────────────────────────

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

// ToolCall is an OpenAI tool call.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Index    int          `json:"index"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function portion of a tool call.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is an OpenAI tool definition.
type Tool struct {
	Type     string      `json:"type"`
	Function ToolDefFunc `json:"function"`
}

// ToolDefFunc is the function definition within a tool spec.
type ToolDefFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
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

// ── OpenAI Error ─────────────────────────────────────────────────────────────

// APIError wraps an error in OpenAI format.
type APIError struct {
	Error APIErrorDetail `json:"error"`
}

// APIErrorDetail is the inner error detail.
type APIErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    *string `json:"code"`
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(v bool) *bool { return &v }

// IntPtr returns a pointer to the given int value.
func IntPtr(v int) *int { return &v }

// Float64Ptr returns a pointer to the given float64 value.
func Float64Ptr(v float64) *float64 { return &v }
