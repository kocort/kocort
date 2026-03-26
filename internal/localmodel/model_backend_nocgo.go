//go:build !llamacpp

package localmodel

import (
	"context"

	"github.com/kocort/kocort/internal/localmodel/llamawrapper"
)

// stubBackend is a no-op ModelBackend used when the llamacpp build tag
// is not set.
type stubBackend struct {
	started bool
}

// newDefaultBackend returns a stub backend when llamacpp is not available.
func newDefaultBackend() ModelBackend {
	return &stubBackend{}
}

func (sb *stubBackend) Start(modelPath string, threads, contextSize, gpuLayers int,
	sampling SamplingParams, enableThinking bool) error {
	sb.started = true
	return nil
}

func (sb *stubBackend) Stop() error {
	sb.started = false
	return nil
}

func (sb *stubBackend) IsStub() bool { return true }

func (sb *stubBackend) ContextSize() int { return 0 }

func (sb *stubBackend) SetSamplingParams(_ SamplingParams) {}

func (sb *stubBackend) CreateChatCompletionStream(_ context.Context,
	_ llamawrapper.ChatCompletionRequest, _ bool) (<-chan llamawrapper.ChatCompletionChunk, error) {
	ch := make(chan llamawrapper.ChatCompletionChunk, 1)
	close(ch)
	return ch, nil
}
