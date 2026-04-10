package serve

import (
	"context"

	"github.com/kocort/kocort/internal/localmodel/engine"
)

// Inference abstracts the inference engine used by Server.
//
// The production implementation is *engine.Engine. Custom implementations
// can be provided for testing via NewServerWithInference.
//
// All type aliases (ChatCompletionRequest, etc.) resolve to the canonical
// definitions in the api/ package; using engine.X here is equivalent since
// engine re-exports them as transparent aliases.
type Inference interface {
	// ── Lifecycle ────────────────────────────────────────────────────────

	// Load initializes the model. Must be called before Run.
	Load() error

	// Run starts the batch decode loop. Blocks until ctx is cancelled.
	Run(ctx context.Context)

	// RequestStop signals the decode loop to exit at the next opportunity.
	RequestStop()

	// Close releases all model resources. Must be called after Run returns.
	Close()

	// ── Status ──────────────────────────────────────────────────────────

	// Status returns the engine's current lifecycle status.
	Status() engine.EngineStatus

	// ── Chat Completions (OpenAI-compatible) ────────────────────────────

	// ChatCompletion creates a streaming chat completion.
	ChatCompletion(ctx context.Context, req engine.ChatCompletionRequest) (<-chan engine.ChatCompletionChunk, error)

	// ── Text Completions (OpenAI-compatible) ────────────────────────────

	// TextCompletion creates a streaming text completion.
	TextCompletion(ctx context.Context, req engine.TextCompletionRequest) (<-chan engine.TextCompletionChunk, error)

	// ── Native Completions ──────────────────────────────────────────────

	// NativeCompletion runs a raw prompt through the engine.
	NativeCompletion(ctx context.Context, prompt string, images []engine.ImageData,
		numPredict int, stops []string, numKeep int, sampling *engine.SamplingConfig,
		shift, truncate, logprobs bool, topLogprobs int) (<-chan engine.CompletionChunk, error)

	// ── Embeddings ──────────────────────────────────────────────────────

	// Embedding computes embeddings for the given text.
	Embedding(ctx context.Context, text string) ([]float32, int, error)
}

// Verify that *engine.Engine satisfies Inference at compile time.
var _ Inference = (*engine.Engine)(nil)
