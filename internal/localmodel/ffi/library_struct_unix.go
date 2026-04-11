//go:build !windows

package ffi

import "github.com/kocort/purego"

// registerStructPassingLlamaFuncs registers llama functions that pass or return
// C structs by value. On non-Windows platforms purego handles this natively.
func (l *Library) registerStructPassingLlamaFuncs() error {
	h := l.hLlama

	purego.RegisterLibFunc(&l.fnLlamaModelDefaultParams, h, "llama_model_default_params")
	purego.RegisterLibFunc(&l.fnLlamaModelLoadFromFile, h, "llama_model_load_from_file")
	purego.RegisterLibFunc(&l.fnLlamaContextDefaultParams, h, "llama_context_default_params")
	purego.RegisterLibFunc(&l.fnLlamaInitFromModel, h, "llama_init_from_model")
	purego.RegisterLibFunc(&l.fnLlamaDecode, h, "llama_decode")
	purego.RegisterLibFunc(&l.fnLlamaBatchInit, h, "llama_batch_init")
	purego.RegisterLibFunc(&l.fnLlamaBatchFree, h, "llama_batch_free")
	purego.RegisterLibFunc(&l.fnLlamaSamplerChainDefaultParams, h, "llama_sampler_chain_default_params")

	return nil
}

// registerStructPassingMtmdFuncs registers mtmd functions that pass or return
// C structs by value. On non-Windows platforms purego handles this natively.
func (l *Library) registerStructPassingMtmdFuncs() error {
	purego.RegisterLibFunc(&l.fnMtmdContextParamsDefault, l.hMtmd, "mtmd_context_params_default")
	return nil
}
