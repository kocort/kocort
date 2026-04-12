package ffi

import (
	"fmt"
	"runtime"
	"slices"
	"unsafe"
)

// Batch wraps llama_batch for token or embedding submission.
type Batch struct {
	c         cBatch
	lib       *Library
	batchSize int
	maxSeq    int
	embedSize int
}

// NewBatch creates a new batch via llama_batch_init.
// batchSize is the max entries per sequence, maxSeq is the number of parallel sequences,
// embedSize is non-zero for embedding-mode batches.
func NewBatch(lib *Library, batchSize int, maxSeq int, embedSize int) (*Batch, error) {
	c := lib.fnLlamaBatchInit(int32(batchSize*maxSeq), int32(embedSize), int32(maxSeq))

	b := &Batch{
		c:         c,
		lib:       lib,
		batchSize: batchSize,
		maxSeq:    maxSeq,
		embedSize: embedSize,
	}

	// Check if allocations succeeded
	allocSize := batchSize * maxSeq
	nilPointer := (embedSize == 0 && c.Token == nil) || (embedSize != 0 && c.Embd == nil) ||
		c.Pos == nil || c.NSeqID == nil || c.SeqID == nil || c.Logits == nil

	if !nilPointer {
		// Also check that seq_id pointers are non-nil
		seqIDPtrs := unsafe.Slice(c.SeqID, allocSize)
		nilPointer = slices.Contains(seqIDPtrs, (*int32)(nil))
	}

	if nilPointer {
		lib.fnLlamaBatchFree(c)
		return nil, fmt.Errorf("%w (batchSize=%d maxSeq=%d embedSize=%d)", ErrBatchAlloc, batchSize, maxSeq, embedSize)
	}

	return b, nil
}

// Size returns the max entries per sequence.
func (b *Batch) Size() int {
	return b.batchSize
}

func (b *Batch) allocSize() int {
	return b.batchSize * b.maxSeq
}

// NumTokens returns the number of entries currently in the batch.
func (b *Batch) NumTokens() int {
	return int(b.c.NTokens)
}

// IsEmbedding returns whether this batch operates in embedding mode.
func (b *Batch) IsEmbedding() bool {
	return b.embedSize != 0
}

// Add adds a token (or embedding) to the batch with the given position, logits flag,
// and sequence IDs.
func (b *Batch) Add(token int, embed []float32, pos int, logits bool, seqIds ...int) {
	idx := int(b.c.NTokens)
	alloc := b.allocSize()

	if !b.IsEmbedding() {
		// Set token
		tokenSlice := unsafe.Slice(b.c.Token, alloc)
		tokenSlice[idx] = int32(token)
	} else {
		// Set embedding
		embdSlice := unsafe.Slice(b.c.Embd, alloc*b.embedSize)
		copy(embdSlice[idx*b.embedSize:], embed)
	}

	// Set position
	posSlice := unsafe.Slice(b.c.Pos, alloc)
	posSlice[idx] = int32(pos)

	// Set number of seq IDs
	nSeqIDSlice := unsafe.Slice(b.c.NSeqID, alloc)
	nSeqIDSlice[idx] = int32(len(seqIds))

	// Set seq IDs
	seqIDPtrs := unsafe.Slice(b.c.SeqID, alloc)
	seqIDArr := unsafe.Slice(seqIDPtrs[idx], len(seqIds))
	for i, s := range seqIds {
		seqIDArr[i] = int32(s)
	}

	// Set logits flag
	logitsSlice := unsafe.Slice(b.c.Logits, alloc)
	if logits {
		logitsSlice[idx] = 1
	} else {
		logitsSlice[idx] = 0
	}

	b.c.NTokens++
	runtime.KeepAlive(b)
}

// Clear resets the batch token count.
func (b *Batch) Clear() {
	b.c.NTokens = 0
}

// Free releases the batch memory.
func (b *Batch) Free() {
	b.batchSize = 0
	b.lib.fnLlamaBatchFree(b.c)
}
