package ffi

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/kocort/purego"
)

// Library holds dlopen handles and all function pointers loaded from llama.cpp shared libraries.
type Library struct {
	mu sync.Mutex

	// dlopen handles — load order matters (dependencies first).
	hGgmlBase uintptr
	hGgmlCPU  uintptr
	hGgml     uintptr
	hLlama    uintptr
	hMtmd     uintptr // optional

	libDir string

	// ── libllama functions ──────────────────────────────────────────────

	fnLlamaBackendInit func()
	fnLlamaLogSet      func(callback uintptr, userData uintptr)

	// Model
	fnLlamaModelDefaultParams func() cModelParams
	fnLlamaModelLoadFromFile  func(path *byte, params cModelParams) uintptr
	fnLlamaModelFree          func(model uintptr)
	fnLlamaModelNEmbd         func(model uintptr) int32
	fnLlamaModelNCtxTrain     func(model uintptr) int32
	fnLlamaModelGetVocab      func(model uintptr) uintptr

	// Context
	fnLlamaContextDefaultParams func() cContextParams
	fnLlamaInitFromModel        func(model uintptr, params cContextParams) uintptr
	fnLlamaFree                 func(ctx uintptr)
	fnLlamaDecode               func(ctx uintptr, batch cBatch) int32
	fnLlamaSynchronize          func(ctx uintptr)
	fnLlamaNCtx                 func(ctx uintptr) uint32
	fnLlamaGetModel             func(ctx uintptr) uintptr
	fnLlamaSetAbortCallback     func(ctx uintptr, callback uintptr, data uintptr)

	// Logits / Embeddings
	fnLlamaGetLogitsIth     func(ctx uintptr, i int32) uintptr
	fnLlamaGetEmbeddingsSeq func(ctx uintptr, seqID int32) uintptr
	fnLlamaGetEmbeddingsIth func(ctx uintptr, i int32) uintptr

	// KV Cache (memory API)
	fnLlamaGetMemory      func(ctx uintptr) uintptr
	fnLlamaMemorySeqAdd   func(mem uintptr, seqID int32, p0 int32, p1 int32, delta int32)
	fnLlamaMemorySeqRm    func(mem uintptr, seqID int32, p0 int32, p1 int32) bool
	fnLlamaMemorySeqCp    func(mem uintptr, srcSeq int32, dstSeq int32, p0 int32, p1 int32)
	fnLlamaMemoryClear    func(mem uintptr, data bool)
	fnLlamaMemoryCanShift func(mem uintptr) bool

	// Tokenizer
	fnLlamaTokenize     func(vocab uintptr, text *byte, textLen int32, tokens *int32, nTokensMax int32, addSpecial bool, parseSpecial bool) int32
	fnLlamaTokenToPiece func(vocab uintptr, token int32, buf *byte, length int32, lstrip int32, special bool) int32

	// Vocab
	fnLlamaVocabNTokens   func(vocab uintptr) int32
	fnLlamaVocabIsEog     func(vocab uintptr, token int32) bool
	fnLlamaVocabGetAddBos func(vocab uintptr) bool

	// Batch
	fnLlamaBatchInit func(nTokens int32, embd int32, nSeqMax int32) cBatch
	fnLlamaBatchFree func(batch cBatch)

	// LoRA
	fnLlamaAdapterLoraInit func(model uintptr, path *byte) uintptr
	fnLlamaSetAdaptersLora func(ctx uintptr, adapters uintptr, nAdapters uintptr, scales uintptr) int32

	// Sampler chain
	fnLlamaSamplerChainDefaultParams func() cSamplerChainParams
	fnLlamaSamplerChainInit          func(params uintptr) uintptr
	fnLlamaSamplerChainAdd           func(chain uintptr, sampler uintptr)
	fnLlamaSamplerSample             func(chain uintptr, ctx uintptr, idx int32) int32
	fnLlamaSamplerAccept             func(chain uintptr, token int32)
	fnLlamaSamplerReset              func(chain uintptr)
	fnLlamaSamplerFree               func(chain uintptr)
	fnLlamaSamplerApply              func(chain uintptr, curP uintptr)

	// Individual samplers
	fnLlamaSamplerInitGreedy    func() uintptr
	fnLlamaSamplerInitDist      func(seed uint32) uintptr
	fnLlamaSamplerInitTopK      func(k int32) uintptr
	fnLlamaSamplerInitTopP      func(p float32, minKeep uintptr) uintptr
	fnLlamaSamplerInitMinP      func(p float32, minKeep uintptr) uintptr
	fnLlamaSamplerInitTypical   func(p float32, minKeep uintptr) uintptr
	fnLlamaSamplerInitTemp      func(t float32) uintptr
	fnLlamaSamplerInitPenalties func(penaltyLastN int32, penaltyRepeat float32, penaltyFreq float32, penaltyPresent float32) uintptr
	fnLlamaSamplerInitGrammar   func(vocab uintptr, grammar *byte, root *byte) uintptr

	// Model metadata & info
	fnLlamaModelMetaValStr func(model uintptr, key *byte, buf *byte, bufSize uintptr) int32
	fnLlamaModelMetaCount  func(model uintptr) int32
	fnLlamaModelDesc       func(model uintptr, buf *byte, bufSize uintptr) int32
	fnLlamaModelSize       func(model uintptr) uint64
	fnLlamaModelNParams    func(model uintptr) uint64
	fnLlamaModelNLayer     func(model uintptr) int32
	fnLlamaModelChatTpl    func(model uintptr, name *byte) *byte

	// System info
	fnLlamaPrintSystemInfo func() *byte

	// Performance
	fnLlamaPerfContextReset func(ctx uintptr)
	fnLlamaPerfContextPrint func(ctx uintptr)

	// Memory position tracking
	fnLlamaMemorySeqPosMin func(mem uintptr, seqId int32) int32
	fnLlamaMemorySeqPosMax func(mem uintptr, seqId int32) int32

	// ── libggml functions ───────────────────────────────────────────────

	fnGgmlBackendDevCount        func() uintptr
	fnGgmlBackendDevGet          func(i uintptr) uintptr
	fnGgmlBackendLoadAllFromPath func(path *byte)

	// ── libggml-base functions ──────────────────────────────────────────

	fnGgmlBackendDevType     func(dev uintptr) int32
	fnGgmlBackendDevGetProps func(dev uintptr, props uintptr)
	fnGgufInitFromFile       func(path *byte, params uintptr) uintptr
	fnGgufFindKey            func(ctx uintptr, key *byte) int32
	fnGgufGetValStr          func(ctx uintptr, keyID int32) *byte
	fnGgufFree               func(ctx uintptr)

	// ── libmtmd functions (optional) ────────────────────────────────────

	fnMtmdContextParamsDefault    func() cMtmdContextParams
	fnMtmdInitFromFile            func(path *byte, model uintptr, params uintptr) uintptr
	fnMtmdFree                    func(ctx uintptr)
	fnMtmdInputChunksInit         func() uintptr
	fnMtmdInputChunksFree         func(chunks uintptr)
	fnMtmdInputChunksSize         func(chunks uintptr) uintptr
	fnMtmdInputChunksGet          func(chunks uintptr, i uintptr) uintptr
	fnMtmdInputChunkGetType       func(chunk uintptr) int32
	fnMtmdInputChunkGetTokensText func(chunk uintptr, n *uintptr) uintptr
	fnMtmdInputChunkGetNTokens    func(chunk uintptr) int32
	fnMtmdTokenize                func(ctx uintptr, chunks uintptr, text uintptr, bitmaps *uintptr, n int32) int32
	fnMtmdEncodeChunk             func(ctx uintptr, chunk uintptr) int32
	fnMtmdGetOutputEmbd           func(ctx uintptr) uintptr
	fnMtmdDefaultMarker           func() *byte
	fnMtmdBitmapFree              func(bm uintptr)
	fnMtmdHelperBitmapInitFromBuf func(ctx uintptr, data *byte, length uintptr) uintptr
	fnMtmdInputTextInit           func(marker *byte, addSpecial bool, parseSpecial bool) uintptr
	fnMtmdInputTextFree           func(text uintptr)

	mtmdAvailable bool
}

// Open loads all required shared libraries from libDir and registers function pointers.
// The load order respects library dependencies:
//  1. libggml-base (foundation)
//  2. libggml-cpu (CPU compute backend)
//  3. libggml (backend registry, depends on libggml-base)
//  4. libllama (core inference, depends on libggml + libggml-base)
//  5. Calls ggml_backend_load_all_from_path to discover GPU backends
//  6. libmtmd (optional multimodal, depends on libllama)
func Open(libDir string) (*Library, error) {
	l := &Library{libDir: libDir}

	var err error

	// 1. libggml-base
	l.hGgmlBase, err = dlopenLib(libDir, "ggml-base")
	if err != nil {
		return nil, fmt.Errorf("open libggml-base: %w", err)
	}

	// 2. libggml-cpu
	//    On Windows the release ships arch-specific ggml-cpu-*.dll variants
	//    (e.g. ggml-cpu-haswell.dll) instead of a single ggml-cpu.dll.
	//    These are loaded dynamically by ggml_backend_load_all_from_path (step 5).
	l.hGgmlCPU, err = dlopenLib(libDir, "ggml-cpu")
	if err != nil && runtime.GOOS != "windows" {
		l.Close()
		return nil, fmt.Errorf("open libggml-cpu: %w", err)
	}

	// 3. libggml
	l.hGgml, err = dlopenLib(libDir, "ggml")
	if err != nil {
		l.Close()
		return nil, fmt.Errorf("open libggml: %w", err)
	}

	// 4. libllama
	l.hLlama, err = dlopenLib(libDir, "llama")
	if err != nil {
		l.Close()
		return nil, fmt.Errorf("open libllama: %w", err)
	}

	// Register all functions
	if err := l.registerGgmlBaseFuncs(); err != nil {
		l.Close()
		return nil, err
	}
	if err := l.registerGgmlFuncs(); err != nil {
		l.Close()
		return nil, err
	}
	if err := l.registerLlamaFuncs(); err != nil {
		l.Close()
		return nil, err
	}

	// 5. Load GPU backends from the same directory
	//    On Windows, GPU backend DLLs (e.g. ggml-cuda.dll) depend on runtime
	//    libraries (cublas64_13.dll, cudart64_13.dll) that live next to them.
	//    SetDllDirectoryW ensures the DLL loader can find these dependencies.
	setDllSearchDir(libDir)
	pathPtr, pathBuf := cstr(libDir)
	_ = pathBuf // prevent GC
	l.fnGgmlBackendLoadAllFromPath(pathPtr)
	runtime.KeepAlive(pathBuf)
	setDllSearchDir("") // restore default

	// 6. Try to load libmtmd (optional)
	l.hMtmd, err = dlopenLib(libDir, "mtmd")
	if err == nil {
		if regErr := l.registerMtmdFuncs(); regErr == nil {
			l.mtmdAvailable = true
		}
	}
	// mtmd failure is not fatal

	return l, nil
}

// Close releases all dlopen handles.
func (l *Library) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.hMtmd != 0 {
		closeLib(l.hMtmd)
		l.hMtmd = 0
	}
	if l.hLlama != 0 {
		closeLib(l.hLlama)
		l.hLlama = 0
	}
	if l.hGgml != 0 {
		closeLib(l.hGgml)
		l.hGgml = 0
	}
	if l.hGgmlCPU != 0 {
		closeLib(l.hGgmlCPU)
		l.hGgmlCPU = 0
	}
	if l.hGgmlBase != 0 {
		closeLib(l.hGgmlBase)
		l.hGgmlBase = 0
	}
}

// IsMtmdAvailable returns whether multimodal support is available.
func (l *Library) IsMtmdAvailable() bool {
	return l.mtmdAvailable
}

// LibDir returns the directory from which libraries were loaded.
func (l *Library) LibDir() string {
	return l.libDir
}

// ── Private helpers ─────────────────────────────────────────────────────────

func dlopenLib(dir, baseName string) (uintptr, error) {
	path := filepath.Join(dir, LibName(baseName))
	h, err := openLib(path)
	if err != nil {
		return 0, fmt.Errorf("dlopen %s: %w", path, err)
	}
	return h, nil
}

func (l *Library) registerGgmlBaseFuncs() error {
	// ggml-base: gguf_* and backend_dev_type/get_props
	purego.RegisterLibFunc(&l.fnGgmlBackendDevType, l.hGgmlBase, "ggml_backend_dev_type")
	purego.RegisterLibFunc(&l.fnGgmlBackendDevGetProps, l.hGgmlBase, "ggml_backend_dev_get_props")
	purego.RegisterLibFunc(&l.fnGgufInitFromFile, l.hGgmlBase, "gguf_init_from_file")
	purego.RegisterLibFunc(&l.fnGgufFindKey, l.hGgmlBase, "gguf_find_key")
	purego.RegisterLibFunc(&l.fnGgufGetValStr, l.hGgmlBase, "gguf_get_val_str")
	purego.RegisterLibFunc(&l.fnGgufFree, l.hGgmlBase, "gguf_free")
	return nil
}

func (l *Library) registerGgmlFuncs() error {
	// ggml: backend device enumeration and loading
	purego.RegisterLibFunc(&l.fnGgmlBackendDevCount, l.hGgml, "ggml_backend_dev_count")
	purego.RegisterLibFunc(&l.fnGgmlBackendDevGet, l.hGgml, "ggml_backend_dev_get")
	purego.RegisterLibFunc(&l.fnGgmlBackendLoadAllFromPath, l.hGgml, "ggml_backend_load_all_from_path")
	return nil
}

func (l *Library) registerLlamaFuncs() error {
	h := l.hLlama

	// Backend init
	purego.RegisterLibFunc(&l.fnLlamaBackendInit, h, "llama_backend_init")
	purego.RegisterLibFunc(&l.fnLlamaLogSet, h, "llama_log_set")

	// Model (non-struct functions only; struct-passing ones are in registerStructPassingLlamaFuncs)
	purego.RegisterLibFunc(&l.fnLlamaModelFree, h, "llama_model_free")
	purego.RegisterLibFunc(&l.fnLlamaModelNEmbd, h, "llama_model_n_embd")
	purego.RegisterLibFunc(&l.fnLlamaModelNCtxTrain, h, "llama_model_n_ctx_train")
	purego.RegisterLibFunc(&l.fnLlamaModelGetVocab, h, "llama_model_get_vocab")

	// Context (non-struct functions only)
	purego.RegisterLibFunc(&l.fnLlamaFree, h, "llama_free")
	purego.RegisterLibFunc(&l.fnLlamaSynchronize, h, "llama_synchronize")
	purego.RegisterLibFunc(&l.fnLlamaNCtx, h, "llama_n_ctx")
	purego.RegisterLibFunc(&l.fnLlamaGetModel, h, "llama_get_model")
	purego.RegisterLibFunc(&l.fnLlamaSetAbortCallback, h, "llama_set_abort_callback")

	// Logits & Embeddings
	purego.RegisterLibFunc(&l.fnLlamaGetLogitsIth, h, "llama_get_logits_ith")
	purego.RegisterLibFunc(&l.fnLlamaGetEmbeddingsSeq, h, "llama_get_embeddings_seq")
	purego.RegisterLibFunc(&l.fnLlamaGetEmbeddingsIth, h, "llama_get_embeddings_ith")

	// KV cache (memory API)
	purego.RegisterLibFunc(&l.fnLlamaGetMemory, h, "llama_get_memory")
	purego.RegisterLibFunc(&l.fnLlamaMemorySeqAdd, h, "llama_memory_seq_add")
	purego.RegisterLibFunc(&l.fnLlamaMemorySeqRm, h, "llama_memory_seq_rm")
	purego.RegisterLibFunc(&l.fnLlamaMemorySeqCp, h, "llama_memory_seq_cp")
	purego.RegisterLibFunc(&l.fnLlamaMemoryClear, h, "llama_memory_clear")
	purego.RegisterLibFunc(&l.fnLlamaMemoryCanShift, h, "llama_memory_can_shift")

	// Tokenizer
	purego.RegisterLibFunc(&l.fnLlamaTokenize, h, "llama_tokenize")
	purego.RegisterLibFunc(&l.fnLlamaTokenToPiece, h, "llama_token_to_piece")

	// Vocab
	purego.RegisterLibFunc(&l.fnLlamaVocabNTokens, h, "llama_vocab_n_tokens")
	purego.RegisterLibFunc(&l.fnLlamaVocabIsEog, h, "llama_vocab_is_eog")
	purego.RegisterLibFunc(&l.fnLlamaVocabGetAddBos, h, "llama_vocab_get_add_bos")

	// Batch — struct-passing functions are in registerStructPassingLlamaFuncs

	// LoRA
	purego.RegisterLibFunc(&l.fnLlamaAdapterLoraInit, h, "llama_adapter_lora_init")
	purego.RegisterLibFunc(&l.fnLlamaSetAdaptersLora, h, "llama_set_adapters_lora")

	// Sampler chain (default params is struct-returning, handled in registerStructPassingLlamaFuncs)
	purego.RegisterLibFunc(&l.fnLlamaSamplerChainInit, h, "llama_sampler_chain_init")
	purego.RegisterLibFunc(&l.fnLlamaSamplerChainAdd, h, "llama_sampler_chain_add")
	purego.RegisterLibFunc(&l.fnLlamaSamplerSample, h, "llama_sampler_sample")
	purego.RegisterLibFunc(&l.fnLlamaSamplerAccept, h, "llama_sampler_accept")
	purego.RegisterLibFunc(&l.fnLlamaSamplerReset, h, "llama_sampler_reset")
	purego.RegisterLibFunc(&l.fnLlamaSamplerFree, h, "llama_sampler_free")
	purego.RegisterLibFunc(&l.fnLlamaSamplerApply, h, "llama_sampler_apply")

	// Individual samplers
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitGreedy, h, "llama_sampler_init_greedy")
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitDist, h, "llama_sampler_init_dist")
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitTopK, h, "llama_sampler_init_top_k")
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitTopP, h, "llama_sampler_init_top_p")
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitMinP, h, "llama_sampler_init_min_p")
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitTypical, h, "llama_sampler_init_typical")
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitTemp, h, "llama_sampler_init_temp")
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitPenalties, h, "llama_sampler_init_penalties")
	purego.RegisterLibFunc(&l.fnLlamaSamplerInitGrammar, h, "llama_sampler_init_grammar")

	// Model metadata & info
	purego.RegisterLibFunc(&l.fnLlamaModelMetaValStr, h, "llama_model_meta_val_str")
	purego.RegisterLibFunc(&l.fnLlamaModelMetaCount, h, "llama_model_meta_count")
	purego.RegisterLibFunc(&l.fnLlamaModelDesc, h, "llama_model_desc")
	purego.RegisterLibFunc(&l.fnLlamaModelSize, h, "llama_model_size")
	purego.RegisterLibFunc(&l.fnLlamaModelNParams, h, "llama_model_n_params")
	purego.RegisterLibFunc(&l.fnLlamaModelNLayer, h, "llama_model_n_layer")
	purego.RegisterLibFunc(&l.fnLlamaModelChatTpl, h, "llama_model_chat_template")

	// System info
	purego.RegisterLibFunc(&l.fnLlamaPrintSystemInfo, h, "llama_print_system_info")

	// Performance
	purego.RegisterLibFunc(&l.fnLlamaPerfContextReset, h, "llama_perf_context_reset")
	purego.RegisterLibFunc(&l.fnLlamaPerfContextPrint, h, "llama_perf_context_print")

	// Memory position tracking
	purego.RegisterLibFunc(&l.fnLlamaMemorySeqPosMin, h, "llama_memory_seq_pos_min")
	purego.RegisterLibFunc(&l.fnLlamaMemorySeqPosMax, h, "llama_memory_seq_pos_max")

	// Register struct-passing functions via platform-specific implementation
	if err := l.registerStructPassingLlamaFuncs(); err != nil {
		return err
	}

	return nil
}

func (l *Library) registerMtmdFuncs() error {
	h := l.hMtmd

	// mtmd_context_params_default returns a struct — handled platform-specifically
	if err := l.registerStructPassingMtmdFuncs(); err != nil {
		return err
	}
	purego.RegisterLibFunc(&l.fnMtmdInitFromFile, h, "mtmd_init_from_file")
	purego.RegisterLibFunc(&l.fnMtmdFree, h, "mtmd_free")
	purego.RegisterLibFunc(&l.fnMtmdInputChunksInit, h, "mtmd_input_chunks_init")
	purego.RegisterLibFunc(&l.fnMtmdInputChunksFree, h, "mtmd_input_chunks_free")
	purego.RegisterLibFunc(&l.fnMtmdInputChunksSize, h, "mtmd_input_chunks_size")
	purego.RegisterLibFunc(&l.fnMtmdInputChunksGet, h, "mtmd_input_chunks_get")
	purego.RegisterLibFunc(&l.fnMtmdInputChunkGetType, h, "mtmd_input_chunk_get_type")
	purego.RegisterLibFunc(&l.fnMtmdInputChunkGetTokensText, h, "mtmd_input_chunk_get_tokens_text")
	purego.RegisterLibFunc(&l.fnMtmdInputChunkGetNTokens, h, "mtmd_input_chunk_get_n_tokens")
	purego.RegisterLibFunc(&l.fnMtmdTokenize, h, "mtmd_tokenize")
	purego.RegisterLibFunc(&l.fnMtmdEncodeChunk, h, "mtmd_encode_chunk")
	purego.RegisterLibFunc(&l.fnMtmdGetOutputEmbd, h, "mtmd_get_output_embd")
	purego.RegisterLibFunc(&l.fnMtmdDefaultMarker, h, "mtmd_default_marker")
	purego.RegisterLibFunc(&l.fnMtmdBitmapFree, h, "mtmd_bitmap_free")
	purego.RegisterLibFunc(&l.fnMtmdHelperBitmapInitFromBuf, h, "mtmd_helper_bitmap_init_from_buf")

	// Optional: mtmd_input_text_init/free were removed in later llama.cpp versions.
	if sym, err := findSymbol(h, "mtmd_input_text_init"); err == nil {
		purego.RegisterFunc(&l.fnMtmdInputTextInit, sym)
	}
	if sym, err := findSymbol(h, "mtmd_input_text_free"); err == nil {
		purego.RegisterFunc(&l.fnMtmdInputTextFree, sym)
	}

	return nil
}
