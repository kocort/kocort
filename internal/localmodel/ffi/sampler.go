package ffi

import (
	"runtime"
	"unsafe"
)

// SamplingParams holds Go-friendly sampling configuration.
// Matches the fields used by the existing llama.SamplingParams.
type SamplingParams struct {
	TopK           int
	TopP           float32
	MinP           float32
	TypicalP       float32
	Temp           float32
	RepeatLastN    int
	PenaltyRepeat  float32
	PenaltyFreq    float32
	PenaltyPresent float32
	PenalizeNl     bool
	Seed           uint32
	Grammar        string
}

// SamplingContext wraps a llama_sampler chain pointer.
type SamplingContext struct {
	chain uintptr // *llama_sampler (chain)
	lib   *Library
}

// NewSamplingContext builds a sampler chain using the official llama_sampler_chain API.
// This replaces the old common_sampler_cinit from sampling_ext.cpp.
func NewSamplingContext(lib *Library, model *Model, params SamplingParams) (*SamplingContext, error) {
	// Get default chain params
	chainParams := lib.fnLlamaSamplerChainDefaultParams()

	chain := lib.fnLlamaSamplerChainInit(uintptr(unsafe.Pointer(&chainParams)))
	if chain == 0 {
		return nil, ErrSamplerCreate
	}

	// 1. Penalties (must be before top-k)
	if params.RepeatLastN != 0 {
		s := lib.fnLlamaSamplerInitPenalties(
			int32(params.RepeatLastN),
			params.PenaltyRepeat,
			params.PenaltyFreq,
			params.PenaltyPresent,
		)
		lib.fnLlamaSamplerChainAdd(chain, s)
	}

	// 2. Top-K
	if params.TopK > 0 {
		lib.fnLlamaSamplerChainAdd(chain, lib.fnLlamaSamplerInitTopK(int32(params.TopK)))
	}

	// 3. Typical-P
	if params.TypicalP > 0 && params.TypicalP < 1.0 {
		lib.fnLlamaSamplerChainAdd(chain, lib.fnLlamaSamplerInitTypical(params.TypicalP, 1))
	}

	// 4. Top-P
	if params.TopP > 0 && params.TopP < 1.0 {
		lib.fnLlamaSamplerChainAdd(chain, lib.fnLlamaSamplerInitTopP(params.TopP, 1))
	}

	// 5. Min-P
	if params.MinP > 0 {
		lib.fnLlamaSamplerChainAdd(chain, lib.fnLlamaSamplerInitMinP(params.MinP, 1))
	}

	// 6. Temperature
	if params.Temp > 0 {
		lib.fnLlamaSamplerChainAdd(chain, lib.fnLlamaSamplerInitTemp(params.Temp))
	}

	// 7. Distribution sampler or greedy
	if params.Temp > 0 {
		lib.fnLlamaSamplerChainAdd(chain, lib.fnLlamaSamplerInitDist(params.Seed))
	} else {
		lib.fnLlamaSamplerChainAdd(chain, lib.fnLlamaSamplerInitGreedy())
	}

	// 8. Grammar (if provided)
	if params.Grammar != "" {
		vocab := lib.fnLlamaModelGetVocab(model.ptr)
		grammarPtr, grammarBuf := cstr(params.Grammar)
		rootPtr, rootBuf := cstr("root")
		s := lib.fnLlamaSamplerInitGrammar(vocab, grammarPtr, rootPtr)
		runtime.KeepAlive(grammarBuf)
		runtime.KeepAlive(rootBuf)
		if s != 0 {
			lib.fnLlamaSamplerChainAdd(chain, s)
		}
	}

	sc := &SamplingContext{chain: chain, lib: lib}
	runtime.SetFinalizer(sc, func(s *SamplingContext) { s.Free() })

	return sc, nil
}

// Sample samples a single token from the chain.
func (s *SamplingContext) Sample(ctx *Context, idx int) int {
	return int(s.lib.fnLlamaSamplerSample(s.chain, ctx.ptr, int32(idx)))
}

// Accept accepts a generated token into the sampler state.
func (s *SamplingContext) Accept(id int, applyGrammar bool) {
	// The official API's accept always processes all chain elements
	// including grammar, so applyGrammar is implicitly true.
	s.lib.fnLlamaSamplerAccept(s.chain, int32(id))
}

// Reset resets the sampler chain state.
func (s *SamplingContext) Reset() {
	s.lib.fnLlamaSamplerReset(s.chain)
}

// Free releases the sampler chain.
func (s *SamplingContext) Free() {
	if s.chain != 0 {
		s.lib.fnLlamaSamplerFree(s.chain)
		s.chain = 0
	}
}
