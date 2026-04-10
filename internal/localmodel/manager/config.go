package manager

import "github.com/kocort/kocort/internal/localmodel/catalog"

// SamplingParams configures the sampling parameters for inference.
// This is a type alias for catalog.SamplingParams so that both the
// catalog preset definitions and the runtime configuration share
// the same underlying type.
type SamplingParams = catalog.SamplingParams

// Config holds the configuration for a local model manager instance.
type Config struct {
	ModelID        string
	ModelsDir      string
	Threads        int
	ContextSize    int
	GpuLayers      int
	Sampling       *SamplingParams
	EnableThinking bool // When true, prompt the model to use <think> blocks for reasoning.
}

// DefaultSamplingParams returns the built-in default sampling parameters.
// Values based on Qwen3.5 HuggingFace recommended settings for thinking mode.
func DefaultSamplingParams() SamplingParams {
	return SamplingParams{
		Temp:          0.6,
		TopP:          0.95,
		TopK:          20,
		MinP:          0.0,
		RepeatLastN:   256,
		PenaltyRepeat: 1.15,
	}
}
