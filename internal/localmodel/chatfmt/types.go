package chatfmt

import "encoding/json"

// ── Tool types ───────────────────────────────────────────────────────────────
// Canonical definitions shared between chatfmt and the OpenAI-compat API layer.

// ToolCall is a tool call emitted by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Index    int          `json:"index"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function portion of a tool call.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a tool definition passed to the model.
type Tool struct {
	Type     string      `json:"type"`
	Function ToolDefFunc `json:"function"`
}

// ToolDefFunc is the function definition within a tool spec.
type ToolDefFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ── Message ──────────────────────────────────────────────────────────────────
// Message is a normalized internal message for prompt rendering.
// It is converted from the OpenAI ChatMessage at the API boundary.

type Message struct {
	Role       string
	Content    string
	Reasoning  string
	ToolCalls  []ToolCall
	Name       string // tool name (for role=tool)
	ToolCallID string // tool_call_id (for role=tool)
	ImageCount int    // number of images (for placeholder generation)
}

// ── Internal stream-parser event ─────────────────────────────────────────────
// parsedEvent is an internal tagged union used by stream parsers.
type parsedEvent struct {
	kind string // "content", "thinking", or "tool"
	data string
}
