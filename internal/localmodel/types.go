// Package localmodel re-exports key inference types so that external consumers
// (backend, cerebellum, api/service) import only "localmodel" rather than
// reaching into the engine or api sub-packages.
package localmodel

import "github.com/kocort/kocort/internal/localmodel/api"

// ── OpenAI-Compatible Chat Types ─────────────────────────────────────────────

type ChatMessage = api.ChatMessage
type ChatCompletionRequest = api.ChatCompletionRequest
type ChatCompletionChunk = api.ChatCompletionChunk
type ChunkChoice = api.ChunkChoice
type ChunkDelta = api.ChunkDelta
type ToolCall = api.ToolCall
type ToolFunction = api.ToolFunction
type Tool = api.Tool
type ToolDefFunc = api.ToolDefFunc

// ── Helpers ──────────────────────────────────────────────────────────────────

var BoolPtr = api.BoolPtr
