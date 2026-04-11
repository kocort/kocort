package ffi

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ContextParams holds context creation parameters (Go-friendly).
type ContextParams struct {
	c   cContextParams
	lib *Library
}

// NewContextParams creates context parameters with the specified settings.
func NewContextParams(lib *Library, numCtx int, batchSize int, numSeqMax int, threads int, flashAttention FlashAttentionType, kvCacheType string) ContextParams {
	params := lib.fnLlamaContextDefaultParams()

	params.NCtx = uint32(numCtx)
	params.NBatch = uint32(batchSize * numSeqMax)
	params.NUbatch = uint32(batchSize)
	params.NSeqMax = uint32(numSeqMax)
	params.NThreads = int32(threads)
	params.NThreadsBatch = int32(threads)
	params.Embeddings = true

	switch flashAttention {
	case FlashAttentionEnabled:
		params.FlashAttnType = LlamaFlashAttnTypeEnabled
	case FlashAttentionDisabled:
		params.FlashAttnType = LlamaFlashAttnTypeDisabled
	case FlashAttentionAuto:
		params.FlashAttnType = LlamaFlashAttnTypeAuto
	}

	params.TypeK = kvCacheTypeFromStr(strings.ToLower(kvCacheType))
	params.TypeV = kvCacheTypeFromStr(strings.ToLower(kvCacheType))

	return ContextParams{c: params, lib: lib}
}

// kvCacheTypeFromStr converts a string to the corresponding GGML type value.
func kvCacheTypeFromStr(s string) int32 {
	switch s {
	case "q8_0":
		return GGMLTypeQ8_0
	case "q4_0":
		return GGMLTypeQ4_0
	default:
		return GGMLTypeF16
	}
}

// Context wraps a llama_context pointer.
type Context struct {
	ptr        uintptr // *llama_context
	lib        *Library
	model      *Model
	numThreads int

	// abort callback management
	abortFlag atomic.Bool
	abortMu   sync.Mutex
	abortCb   uintptr // purego callback pointer
}

// NewContextWithModel creates a new llama context for the given model.
func NewContextWithModel(lib *Library, model *Model, params ContextParams) (*Context, error) {
	ctxPtr := lib.fnLlamaInitFromModel(model.ptr, params.c)
	if ctxPtr == 0 {
		return nil, ErrContextCreate
	}

	c := &Context{
		ptr:        ctxPtr,
		lib:        lib,
		model:      model,
		numThreads: int(params.c.NThreads),
	}
	c.InstallAbortFlagCallback()

	return c, nil
}

// Decode submits a batch for decoding.
func (c *Context) Decode(batch *Batch) error {
	code := int(c.lib.fnLlamaDecode(c.ptr, batch.c))
	if code < 0 {
		return fmt.Errorf("llama_decode failed with code %d", code)
	}
	switch code {
	case 0:
		return nil
	case 1:
		return ErrKvCacheFull
	case 2:
		return ErrDecodeAborted
	default:
		return fmt.Errorf("llama_decode returned unexpected code %d", code)
	}
}

// SetAbortCallback sets a custom abort callback for decode operations.
func (c *Context) SetAbortCallback(callback func() bool) {
	c.abortMu.Lock()
	defer c.abortMu.Unlock()

	// Clear previous callback
	c.clearAbortCallbackLocked()

	if c.ptr == 0 || callback == nil {
		return
	}

	cb := newAbortCallbackPlatform(func(userData uintptr) bool {
		return callback()
	})
	c.abortCb = cb
	c.lib.fnLlamaSetAbortCallback(c.ptr, cb, 0)
}

// RequestAbort signals the abort flag.
func (c *Context) RequestAbort() {
	c.abortFlag.Store(true)
}

// ResetAbort clears the abort flag.
func (c *Context) ResetAbort() {
	c.abortFlag.Store(false)
}

// InstallAbortFlagCallback sets the default abort callback based on the abort flag.
func (c *Context) InstallAbortFlagCallback() {
	c.SetAbortCallback(func() bool {
		return c.abortFlag.Load()
	})
}

func (c *Context) clearAbortCallbackLocked() {
	if c.ptr != 0 {
		c.lib.fnLlamaSetAbortCallback(c.ptr, 0, 0)
	}
	c.abortCb = 0
}

// Model returns the model associated with this context.
func (c *Context) Model() *Model {
	// If we don't already have it cached, get from llama
	if c.model == nil {
		mPtr := c.lib.fnLlamaGetModel(c.ptr)
		c.model = &Model{ptr: mPtr, lib: c.lib}
	}
	return c.model
}

// KvCacheSeqAdd shifts token positions in the KV cache.
func (c *Context) KvCacheSeqAdd(seqId int, p0 int, p1 int, delta int) {
	mem := c.lib.fnLlamaGetMemory(c.ptr)
	c.lib.fnLlamaMemorySeqAdd(mem, int32(seqId), int32(p0), int32(p1), int32(delta))
}

// KvCacheSeqRm removes a range of tokens from a sequence in the KV cache.
func (c *Context) KvCacheSeqRm(seqId int, p0 int, p1 int) bool {
	mem := c.lib.fnLlamaGetMemory(c.ptr)
	return c.lib.fnLlamaMemorySeqRm(mem, int32(seqId), int32(p0), int32(p1))
}

// KvCacheSeqCp copies a sequence range in the KV cache.
func (c *Context) KvCacheSeqCp(srcSeqId int, dstSeqId int, p0 int, p1 int) {
	mem := c.lib.fnLlamaGetMemory(c.ptr)
	c.lib.fnLlamaMemorySeqCp(mem, int32(srcSeqId), int32(dstSeqId), int32(p0), int32(p1))
}

// KvCacheClear clears the entire KV cache.
func (c *Context) KvCacheClear() {
	mem := c.lib.fnLlamaGetMemory(c.ptr)
	c.lib.fnLlamaMemoryClear(mem, true)
}

// KvCacheCanShift returns whether the KV cache supports shifting.
func (c *Context) KvCacheCanShift() bool {
	mem := c.lib.fnLlamaGetMemory(c.ptr)
	return c.lib.fnLlamaMemoryCanShift(mem)
}

// KvCacheSeqPosMin returns the minimum position of tokens in the KV cache for the given sequence.
func (c *Context) KvCacheSeqPosMin(seqId int) int {
	mem := c.lib.fnLlamaGetMemory(c.ptr)
	return int(c.lib.fnLlamaMemorySeqPosMin(mem, int32(seqId)))
}

// KvCacheSeqPosMax returns the maximum position of tokens in the KV cache for the given sequence.
func (c *Context) KvCacheSeqPosMax(seqId int) int {
	mem := c.lib.fnLlamaGetMemory(c.ptr)
	return int(c.lib.fnLlamaMemorySeqPosMax(mem, int32(seqId)))
}

// PerfReset resets the performance counters for this context.
func (c *Context) PerfReset() {
	c.lib.fnLlamaPerfContextReset(c.ptr)
}

// PerfPrint prints performance statistics for this context to the llama log.
func (c *Context) PerfPrint() {
	c.lib.fnLlamaPerfContextPrint(c.ptr)
}

// NCtx returns the actual context size.
func (c *Context) NCtx() int {
	return int(c.lib.fnLlamaNCtx(c.ptr))
}

// Free releases the context resources.
func (c *Context) Free() {
	c.abortMu.Lock()
	defer c.abortMu.Unlock()

	c.clearAbortCallbackLocked()
	if c.ptr != 0 {
		c.lib.fnLlamaFree(c.ptr)
		c.ptr = 0
	}
}

// GetEmbeddingsSeq returns the embeddings for a sequence.
func (c *Context) GetEmbeddingsSeq(seqId int) []float32 {
	ePtr := c.lib.fnLlamaGetEmbeddingsSeq(c.ptr, int32(seqId))
	if ePtr == 0 {
		return nil
	}
	nEmbd := c.Model().NEmbd()
	src := unsafe.Slice((*float32)(unsafe.Pointer(ePtr)), nEmbd)
	result := make([]float32, nEmbd)
	copy(result, src)
	return result
}

// GetEmbeddingsIth returns the embeddings at position i.
func (c *Context) GetEmbeddingsIth(i int) []float32 {
	ePtr := c.lib.fnLlamaGetEmbeddingsIth(c.ptr, int32(i))
	if ePtr == 0 {
		return nil
	}
	nEmbd := c.Model().NEmbd()
	src := unsafe.Slice((*float32)(unsafe.Pointer(ePtr)), nEmbd)
	result := make([]float32, nEmbd)
	copy(result, src)
	return result
}

// GetLogitsIth returns a copy of the logits at position i.
func (c *Context) GetLogitsIth(i int) []float32 {
	lPtr := c.lib.fnLlamaGetLogitsIth(c.ptr, int32(i))
	if lPtr == 0 {
		return nil
	}
	vocabSize := c.Model().NumVocab()
	src := unsafe.Slice((*float32)(unsafe.Pointer(lPtr)), vocabSize)
	result := make([]float32, vocabSize)
	copy(result, src)
	return result
}

// GetLogitsIthDirect returns a zero-copy slice of the logits at position i.
// The returned slice references C memory and is only valid until the next Decode call.
// Use this for performance-critical paths (e.g. logprob computation) where the data
// is consumed immediately.
func (c *Context) GetLogitsIthDirect(i int) []float32 {
	lPtr := c.lib.fnLlamaGetLogitsIth(c.ptr, int32(i))
	if lPtr == 0 {
		return nil
	}
	vocabSize := c.Model().NumVocab()
	return unsafe.Slice((*float32)(unsafe.Pointer(lPtr)), vocabSize)
}

// Synchronize waits for pending operations to complete.
func (c *Context) Synchronize() {
	c.lib.fnLlamaSynchronize(c.ptr)
}
