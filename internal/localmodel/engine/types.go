package engine

// This file provides backward-compatible type aliases. Canonical definitions
// have been moved to the api/ package (internal/localmodel/api).
//
// Existing code that uses engine.ChatMessage, engine.ChatCompletionRequest,
// etc. continues to work transparently — Go type aliases are fully compatible.

import "github.com/kocort/kocort/internal/localmodel/api"

// ── Sampling ─────────────────────────────────────────────────────────────────

type SamplingConfig = api.SamplingConfig

var DefaultSamplingConfig = api.DefaultSamplingConfig

// ── Logprob Types ────────────────────────────────────────────────────────────

type TokenLogprob = api.TokenLogprob
type LogprobEntry = api.LogprobEntry

// ── Image Data ───────────────────────────────────────────────────────────────

type ImageData = api.ImageData

// ── Native Completion Types ──────────────────────────────────────────────────

type CompletionRequest = api.CompletionRequest
type CompletionOpts = api.CompletionOpts
type CompletionChunk = api.CompletionChunk

// ── Load/Health/Embedding Types ──────────────────────────────────────────────

type HealthResponse = api.HealthResponse
type EmbeddingRequest = api.EmbeddingRequest
type EmbeddingResponse = api.EmbeddingResponse

// ── OpenAI-Compatible Chat Types ─────────────────────────────────────────────

type ChatMessage = api.ChatMessage
type ContentPart = api.ContentPart
type ImageURL = api.ImageURL

// Tool type aliases — canonical definitions live in chatfmt, re-exported via api.
type ToolCall = api.ToolCall
type ToolFunction = api.ToolFunction
type Tool = api.Tool
type ToolDefFunc = api.ToolDefFunc

type ChatCompletionRequest = api.ChatCompletionRequest
type StreamOptions = api.StreamOptions
type ReasoningParam = api.ReasoningParam
type ResponseFormat = api.ResponseFormat

type ChatChoice = api.ChatChoice
type ChatLogprobs = api.ChatLogprobs
type OpenAILogprob = api.OpenAILogprob
type OpenAITokenLogprob = api.OpenAITokenLogprob
type ChatCompletionUsage = api.ChatCompletionUsage
type ChatCompletionResponse = api.ChatCompletionResponse

type ChunkDelta = api.ChunkDelta
type ChunkChoice = api.ChunkChoice
type ChatCompletionChunk = api.ChatCompletionChunk

// ── OpenAI Text Completions ──────────────────────────────────────────────────

type TextCompletionRequest = api.TextCompletionRequest
type TextCompletionChoice = api.TextCompletionChoice
type TextCompletionResponse = api.TextCompletionResponse
type TextCompletionChunk = api.TextCompletionChunk

// ── OpenAI Models ────────────────────────────────────────────────────────────

type ModelEntry = api.ModelEntry
type ModelList = api.ModelList

// ── OpenAI Error ─────────────────────────────────────────────────────────────

type APIError = api.APIError
type APIErrorDetail = api.APIErrorDetail

// ── Helpers ──────────────────────────────────────────────────────────────────

var (
	BoolPtr    = api.BoolPtr
	Float64Ptr = api.Float64Ptr
)
