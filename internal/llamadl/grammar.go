package llamadl

import (
	"runtime"
	"sync"
	"unsafe"
)

// TokenData holds a token ID and its logit value (used by Grammar.Apply).
type TokenData struct {
	ID    int32
	Logit float32
}

// Grammar wraps a llama_sampler created via llama_sampler_init_grammar.
// This replaces the old custom grammar implementation from sampling_ext.cpp.
type Grammar struct {
	sampler uintptr
	lib     *Library
	mu      sync.Mutex
}

// NewGrammar creates a grammar sampler from a GBNF grammar string.
// The vocab pointer is obtained from model.Vocab().
func NewGrammar(lib *Library, vocab uintptr, grammar string) *Grammar {
	grammarPtr, grammarBuf := cstr(grammar)
	rootPtr, rootBuf := cstr("root")
	smpl := lib.fnLlamaSamplerInitGrammar(vocab, grammarPtr, rootPtr)
	runtime.KeepAlive(grammarBuf)
	runtime.KeepAlive(rootBuf)
	if smpl == 0 {
		return nil
	}
	return &Grammar{sampler: smpl, lib: lib}
}

// Apply applies the grammar constraint to the given token logits.
func (g *Grammar) Apply(tokens []TokenData) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sampler == 0 || len(tokens) == 0 {
		return
	}
	tds := make([]cTokenData, len(tokens))
	for i, t := range tokens {
		tds[i] = cTokenData{ID: t.ID, Logit: t.Logit, P: 0}
	}
	arr := cTokenDataArray{
		Data:     uintptr(unsafe.Pointer(&tds[0])),
		Size:     uintptr(len(tds)),
		Selected: -1,
		Sorted:   false,
	}
	g.lib.fnLlamaSamplerApply(g.sampler, uintptr(unsafe.Pointer(&arr)))
	for i := range tokens {
		tokens[i].Logit = tds[i].Logit
	}
	runtime.KeepAlive(tds)
}

// Accept accepts a token into the grammar state.
func (g *Grammar) Accept(token int32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sampler == 0 {
		return
	}
	g.lib.fnLlamaSamplerAccept(g.sampler, token)
}

// Free releases the grammar sampler.
func (g *Grammar) Free() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sampler != 0 {
		g.lib.fnLlamaSamplerFree(g.sampler)
		g.sampler = 0
	}
}
