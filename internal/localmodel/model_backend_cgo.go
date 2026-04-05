//go:build llamacpp

package localmodel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/localmodel/llamawrapper"
)

// stopTimeout is how long Stop waits for the decode goroutine to exit
// before force-closing the engine.
const stopTimeout = 10 * time.Second

// engineBackend is the real ModelBackend backed by llamawrapper.Engine.
// It directly manages llama.cpp model loading and batch inference via
// the Engine — no Inferencer layer.
type engineBackend struct {
	mu       sync.Mutex
	engine   *llamawrapper.Engine
	cancel   context.CancelFunc
	runDone  chan struct{}
	sampling SamplingParams
	ready    bool
}

// newDefaultBackend creates an Engine-based ModelBackend.
func newDefaultBackend() ModelBackend {
	return &engineBackend{
		sampling: DefaultSamplingParams(),
	}
}

// Start loads the model via llamawrapper.Engine and starts the batch
// inference loop in the background.
func (eb *engineBackend) Start(modelPath string, threads, contextSize, gpuLayers int,
	sampling SamplingParams, enableThinking bool) error {

	eb.mu.Lock()
	defer eb.mu.Unlock()

	if eb.ready {
		return fmt.Errorf("backend already started; call Stop first")
	}

	eb.sampling = sampling

	engine, err := llamawrapper.NewEngine(llamawrapper.EngineConfig{
		ModelPath:      modelPath,
		ContextSize:    contextSize,
		BatchSize:      512,
		Parallel:       1,
		Threads:        threads,
		GPULayers:      gpuLayers,
		UseMmap:        true,
		EnableThinking: enableThinking,
	})
	if err != nil {
		return fmt.Errorf("load model: %w", err)
	}

	// Start the batch inference loop.
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	eb.engine = engine
	eb.cancel = cancel
	eb.runDone = runDone
	eb.ready = true

	go func() {
		defer close(runDone)
		engine.Run(ctx)
	}()

	slog.Info("[model-backend] engine started",
		"modelPath", modelPath,
		"threads", threads,
		"contextSize", contextSize,
		"gpuLayers", gpuLayers)

	return nil
}

// Stop halts the inference loop and releases all resources.
// It waits up to stopTimeout for the decode goroutine to exit, then
// force-closes the engine to avoid blocking the caller forever.
func (eb *engineBackend) Stop() error {
	eb.mu.Lock()
	if !eb.ready {
		eb.mu.Unlock()
		return nil
	}

	cancel := eb.cancel
	engine := eb.engine
	runDone := eb.runDone
	eb.cancel = nil
	eb.engine = nil
	eb.runDone = nil
	eb.ready = false
	eb.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if engine != nil {
		engine.RequestStop()

		// Wait with a timeout so a stuck decode loop can't block us forever.
		exited := true
		if runDone != nil {
			select {
			case <-runDone:
				// Clean exit.
			case <-time.After(stopTimeout):
				slog.Warn("[model-backend] decode loop did not exit within timeout, leaking engine to avoid crash",
					"timeout", stopTimeout)
				exited = false
			}
		}
		if exited {
			engine.Close()
		} else {
			// The decode goroutine is still inside C code (llama_decode).
			// Calling llama_free while C code is executing causes SIGSEGV.
			// We intentionally leak the engine resources here; a memory leak
			// is far preferable to crashing the entire process.
			// Wait asynchronously so resources are freed once the goroutine finally exits.
			go func() {
				if runDone != nil {
					<-runDone
				}
				engine.Close()
				slog.Info("[model-backend] leaked engine finally closed after decode loop exit")
			}()
		}
	}

	slog.Info("[model-backend] engine stopped")
	return nil
}

// IsStub returns false — this is a real backend.
func (eb *engineBackend) IsStub() bool { return false }

// ContextSize returns the effective context window size.
func (eb *engineBackend) ContextSize() int {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if eb.engine == nil {
		return 0
	}
	return eb.engine.ContextSize()
}

// SetSamplingParams updates the sampling parameters.
func (eb *engineBackend) SetSamplingParams(sp SamplingParams) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.sampling = sp
}

// buildSamplingConfig converts localmodel.SamplingParams to llamawrapper.SamplingConfig.
func (eb *engineBackend) buildSamplingConfig() *llamawrapper.SamplingConfig {
	sp := eb.sampling
	return &llamawrapper.SamplingConfig{
		Temperature:   sp.Temp,
		TopP:          sp.TopP,
		TopK:          sp.TopK,
		MinP:          sp.MinP,
		TypicalP:      sp.TypicalP,
		RepeatLastN:   sp.RepeatLastN,
		RepeatPenalty: sp.PenaltyRepeat,
		FreqPenalty:   sp.PenaltyFreq,
		PresPenalty:   sp.PenaltyPresent,
	}
}

// CreateChatCompletionStream runs inference through the Engine and
// returns a channel of ChatCompletionChunk.
//
// Flow:
//  1. Set sampling override and call Engine.ChatCompletion() → chunk stream
//  2. thinkBlockParser routes <think> tokens to ReasoningContent
//  3. Final chunk carries parsed ToolCalls and FinishReason
func (eb *engineBackend) CreateChatCompletionStream(ctx context.Context,
	req llamawrapper.ChatCompletionRequest, enableThinking bool) (<-chan llamawrapper.ChatCompletionChunk, error) {

	eb.mu.Lock()
	if !eb.ready || eb.engine == nil {
		eb.mu.Unlock()
		return nil, fmt.Errorf("backend not started")
	}
	engine := eb.engine
	samplingConfig := eb.buildSamplingConfig()
	eb.mu.Unlock()

	slog.Debug("[model-backend] ChatCompletion", "enableThinking", enableThinking, "samplingConfig", samplingConfig)

	// Set sampling override from backend config.
	req.SamplingOverride = samplingConfig
	req.Stream = true
	req.EnableThinking = llamawrapper.BoolPtr(enableThinking)

	return engine.ChatCompletion(ctx, req)
}
