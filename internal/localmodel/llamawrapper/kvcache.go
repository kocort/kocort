package llamawrapper

import (
	"errors"
	"log/slog"

	"github.com/kocort/kocort/internal/llama"
)

// kvSlot tracks the KV cache state for a single inference sequence.
type kvSlot struct {
	// id is the KV cache sequence ID (0..parallel-1).
	id int

	// inputs that have been committed to the KV cache.
	inputs []input

	// inUse indicates whether this slot is currently assigned to a sequence.
	inUse bool
}

// kvCache manages KV cache slots for multiple parallel sequences.
// It selects the slot with the longest matching prefix to maximize cache reuse.
type kvCache struct {
	ctx    *llama.Context
	slots  []kvSlot
	ctxLen int // per-slot context length
}

// newKVCache creates a new KV cache manager.
func newKVCache(ctx *llama.Context, totalCtx int, parallel int) (*kvCache, error) {
	if parallel <= 0 {
		return nil, errors.New("parallel must be > 0")
	}
	perSlot := totalCtx / parallel
	if perSlot < 1 {
		return nil, errors.New("context size too small for the given parallelism")
	}

	slots := make([]kvSlot, parallel)
	for i := range slots {
		slots[i] = kvSlot{id: i}
	}

	return &kvCache{
		ctx:    ctx,
		slots:  slots,
		ctxLen: perSlot,
	}, nil
}

// acquire finds the best available slot (longest matching prefix).
// It returns the slot, the remaining inputs that still need processing, and an error.
func (kv *kvCache) acquire(inputs []input) (*kvSlot, []input, error) {
	var best *kvSlot
	bestLen := -1

	for i := range kv.slots {
		s := &kv.slots[i]
		if s.inUse {
			continue
		}

		// Calculate the length of the common prefix.
		commonLen := 0
		for j := 0; j < len(s.inputs) && j < len(inputs); j++ {
			if inputs[j].embed != nil {
				break // don't compare embeddings
			}
			if s.inputs[j].token != inputs[j].token {
				break
			}
			commonLen = j + 1
		}

		if commonLen > bestLen || best == nil {
			best = s
			bestLen = commonLen
		}
	}

	if best == nil {
		return nil, nil, errors.New("no free cache slot")
	}

	best.inUse = true

	// Ensure at least 1 token still needs processing (never fully cached).
	if bestLen == len(inputs) && bestLen > 0 {
		bestLen--
	}

	// Trim the slot's KV cache to only the prefix we're keeping.
	if len(best.inputs) > 0 && kv.ctx != nil {
		if !kv.ctx.KvCacheSeqRm(best.id, bestLen, -1) {
			// Fallback: remove everything
			kv.ctx.KvCacheSeqRm(best.id, 0, -1)
			bestLen = 0
		}
	}

	best.inputs = inputs[:bestLen]

	slog.Debug("kvCache.acquire", "slot", best.id, "prefix", bestLen, "remaining", len(inputs)-bestLen)
	return best, inputs[bestLen:], nil
}

// release marks a slot as free.
func (kv *kvCache) release(s *kvSlot) {
	s.inUse = false
}

// shift evicts old tokens from a slot to make room for new ones, keeping numKeep.
func (kv *kvCache) shift(s *kvSlot, numKeep int) error {
	if !kv.ctx.KvCacheCanShift() {
		return errors.New("KV cache does not support shifting")
	}

	target := kv.ctxLen / 4
	if target < 1 {
		target = 1
	}
	discard := target
	if numKeep+discard > len(s.inputs) {
		discard = len(s.inputs) - numKeep
	}
	if discard <= 0 {
		return errors.New("nothing to discard during shift")
	}

	slog.Debug("kvCache.shift", "slot", s.id, "keep", numKeep, "discard", discard)

	// Remove the segment right after the keep region.
	kv.ctx.KvCacheSeqRm(s.id, numKeep, numKeep+discard)
	// Shift all subsequent positions back by discard.
	kv.ctx.KvCacheSeqAdd(s.id, numKeep+discard, len(s.inputs), -discard)

	newInputs := make([]input, 0, len(s.inputs)-discard)
	newInputs = append(newInputs, s.inputs[:numKeep]...)
	newInputs = append(newInputs, s.inputs[numKeep+discard:]...)
	s.inputs = newInputs

	return nil
}
