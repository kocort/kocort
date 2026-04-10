package api

// ── OpenAI Error ─────────────────────────────────────────────────────────────

// APIError wraps an error in OpenAI format.
type APIError struct {
	Error APIErrorDetail `json:"error"`
}

// APIErrorDetail is the inner error detail.
type APIErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    *string `json:"code"`
}
