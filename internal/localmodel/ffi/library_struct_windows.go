package ffi

import (
	"fmt"
	"syscall"
	"unsafe"
)

// registerStructPassingLlamaFuncs registers llama functions that pass or return
// C structs by value. On Windows, purego does not support struct arguments, so
// we resolve the raw symbol addresses and call them via syscall.SyscallN which
// follows the Microsoft x64 calling convention:
//   - Structs > 8 bytes are passed by pointer (caller allocates a copy).
//   - Struct return values > 8 bytes use a hidden first pointer argument.
//   - Structs of exactly 8 bytes are passed/returned in a register.
func (l *Library) registerStructPassingLlamaFuncs() error {
	h := l.hLlama

	// ── llama_model_default_params → returns cModelParams (72 bytes, hidden return ptr) ──
	symModelDefaultParams, err := findSymbol(h, "llama_model_default_params")
	if err != nil {
		return fmt.Errorf("find llama_model_default_params: %w", err)
	}
	l.fnLlamaModelDefaultParams = func() cModelParams {
		var result cModelParams
		syscall.SyscallN(symModelDefaultParams, uintptr(unsafe.Pointer(&result)))
		return result
	}

	// ── llama_model_load_from_file(path, params) → params is 72 bytes, passed by ptr ──
	symModelLoadFromFile, err := findSymbol(h, "llama_model_load_from_file")
	if err != nil {
		return fmt.Errorf("find llama_model_load_from_file: %w", err)
	}
	l.fnLlamaModelLoadFromFile = func(path *byte, params cModelParams) uintptr {
		r1, _, _ := syscall.SyscallN(symModelLoadFromFile,
			uintptr(unsafe.Pointer(path)),
			uintptr(unsafe.Pointer(&params)))
		return r1
	}

	// ── llama_context_default_params → returns cContextParams (136 bytes, hidden return ptr) ──
	symCtxDefaultParams, err := findSymbol(h, "llama_context_default_params")
	if err != nil {
		return fmt.Errorf("find llama_context_default_params: %w", err)
	}
	l.fnLlamaContextDefaultParams = func() cContextParams {
		var result cContextParams
		syscall.SyscallN(symCtxDefaultParams, uintptr(unsafe.Pointer(&result)))
		return result
	}

	// ── llama_init_from_model(model, params) → params is 136 bytes, passed by ptr ──
	symInitFromModel, err := findSymbol(h, "llama_init_from_model")
	if err != nil {
		return fmt.Errorf("find llama_init_from_model: %w", err)
	}
	l.fnLlamaInitFromModel = func(model uintptr, params cContextParams) uintptr {
		r1, _, _ := syscall.SyscallN(symInitFromModel,
			model,
			uintptr(unsafe.Pointer(&params)))
		return r1
	}

	// ── llama_decode(ctx, batch) → batch is 56 bytes, passed by ptr ──
	symDecode, err := findSymbol(h, "llama_decode")
	if err != nil {
		return fmt.Errorf("find llama_decode: %w", err)
	}
	l.fnLlamaDecode = func(ctx uintptr, batch cBatch) int32 {
		r1, _, _ := syscall.SyscallN(symDecode,
			ctx,
			uintptr(unsafe.Pointer(&batch)))
		return int32(r1)
	}

	// ── llama_batch_init(nTokens, embd, nSeqMax) → returns cBatch (56 bytes, hidden return ptr) ──
	symBatchInit, err := findSymbol(h, "llama_batch_init")
	if err != nil {
		return fmt.Errorf("find llama_batch_init: %w", err)
	}
	l.fnLlamaBatchInit = func(nTokens int32, embd int32, nSeqMax int32) cBatch {
		var result cBatch
		syscall.SyscallN(symBatchInit,
			uintptr(unsafe.Pointer(&result)),
			uintptr(nTokens),
			uintptr(embd),
			uintptr(nSeqMax))
		return result
	}

	// ── llama_batch_free(batch) → batch is 56 bytes, passed by ptr ──
	symBatchFree, err := findSymbol(h, "llama_batch_free")
	if err != nil {
		return fmt.Errorf("find llama_batch_free: %w", err)
	}
	l.fnLlamaBatchFree = func(batch cBatch) {
		syscall.SyscallN(symBatchFree, uintptr(unsafe.Pointer(&batch)))
	}

	// ── llama_sampler_chain_default_params → returns cSamplerChainParams (8 bytes, in register) ──
	symSamplerChainDefaultParams, err := findSymbol(h, "llama_sampler_chain_default_params")
	if err != nil {
		return fmt.Errorf("find llama_sampler_chain_default_params: %w", err)
	}
	l.fnLlamaSamplerChainDefaultParams = func() cSamplerChainParams {
		r1, _, _ := syscall.SyscallN(symSamplerChainDefaultParams)
		return *(*cSamplerChainParams)(unsafe.Pointer(&r1))
	}

	return nil
}

// registerStructPassingMtmdFuncs registers mtmd functions that pass or return
// C structs by value using syscall.SyscallN on Windows.
func (l *Library) registerStructPassingMtmdFuncs() error {
	// ── mtmd_context_params_default → returns cMtmdContextParams (56 bytes, hidden return ptr) ──
	symMtmdCtxParamsDefault, err := findSymbol(l.hMtmd, "mtmd_context_params_default")
	if err != nil {
		return fmt.Errorf("find mtmd_context_params_default: %w", err)
	}
	l.fnMtmdContextParamsDefault = func() cMtmdContextParams {
		var result cMtmdContextParams
		syscall.SyscallN(symMtmdCtxParamsDefault, uintptr(unsafe.Pointer(&result)))
		return result
	}

	return nil
}
