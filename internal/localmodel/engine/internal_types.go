package engine

import "github.com/kocort/kocort/internal/localmodel/api"

// ── Engine-internal types ────────────────────────────────────────────────────
// These types are only used within the engine package and are not exported.

// input represents a single slot in the prompt: either a token id or an embedding vector.
type input struct {
	token int
	embed []float32
}

// fragment is what flows through the sequence response channel.
type fragment struct {
	text     string
	logprobs []api.LogprobEntry
}
