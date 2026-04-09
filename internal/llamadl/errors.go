package llamadl

import "errors"

var (
	// ErrKvCacheFull is returned when llama_decode cannot find a KV cache slot.
	ErrKvCacheFull = errors.New("could not find a kv cache slot")

	// ErrDecodeAborted is returned when llama_decode is cancelled via the abort callback.
	ErrDecodeAborted = errors.New("llama decode aborted")

	// ErrLibNotLoaded is returned when attempting to use the library before it has been loaded.
	ErrLibNotLoaded = errors.New("llama.cpp library not loaded")

	// ErrModelLoad is returned when llama_model_load_from_file fails.
	ErrModelLoad = errors.New("unable to load model")

	// ErrContextCreate is returned when llama_init_from_model fails.
	ErrContextCreate = errors.New("unable to create llama context")

	// ErrSamplerCreate is returned when sampler chain creation fails.
	ErrSamplerCreate = errors.New("unable to create sampling context")

	// ErrBatchAlloc is returned when batch allocation fails.
	ErrBatchAlloc = errors.New("unable to allocate batch")

	// ErrTokenize is returned when tokenization fails.
	ErrTokenize = errors.New("tokenization failed")
)
