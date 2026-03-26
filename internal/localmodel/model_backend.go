package localmodel

import (
	"context"

	"github.com/kocort/kocort/internal/localmodel/llamawrapper"
)

// ModelBackend abstracts the inference engine used by Manager.
//
// In production builds with the llamacpp tag, this is backed by
// llamawrapper.Engine which directly manages llama.cpp model/context.
// In builds without llamacpp, a stub backend is used.
// For testing, custom implementations can be provided via
// NewManagerWithBackend.
type ModelBackend interface {
	// Start loads the model and begins the inference loop.
	Start(modelPath string, threads, contextSize, gpuLayers int,
		sampling SamplingParams, enableThinking bool) error

	// Stop releases all model resources and stops the inference loop.
	Stop() error

	// IsStub returns true if this is a no-op backend (e.g. built without
	// llama.cpp support).
	IsStub() bool

	// ContextSize returns the effective context window size after loading.
	// Returns 0 if no model is loaded.
	ContextSize() int

	// SetSamplingParams updates sampling parameters for subsequent inference.
	SetSamplingParams(sp SamplingParams)

	// CreateChatCompletionStream creates a streaming chat completion.
	// The caller provides a llamawrapper.ChatCompletionRequest;
	// inference via the underlying engine, handles <think> block parsing,
	// and wraps the result as a ChatCompletionStream.
	CreateChatCompletionStream(ctx context.Context, req llamawrapper.ChatCompletionRequest,
		enableThinking bool) (<-chan llamawrapper.ChatCompletionChunk, error)
}
