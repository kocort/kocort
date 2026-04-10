package engine

// ── Engine Configuration ─────────────────────────────────────────────────────

// EngineConfig holds all parameters needed to initialize the inference engine.
type EngineConfig struct {
	// ModelPath is the path to the GGUF model file.
	ModelPath string

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
