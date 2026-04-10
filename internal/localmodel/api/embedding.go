package api

// ── Embedding Types ──────────────────────────────────────────────────────────

// EmbeddingRequest is the request body for POST /embedding.
type EmbeddingRequest struct {
	Content string `json:"content"`
}

// EmbeddingResponse is returned by POST /embedding.
type EmbeddingResponse struct {
	Embedding       []float32 `json:"embedding"`
	PromptEvalCount int       `json:"prompt_eval_count"`
}
