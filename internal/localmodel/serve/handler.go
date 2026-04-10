package serve

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"

	"github.com/kocort/kocort/internal/localmodel/engine"
)

// ── Handler helpers ──────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("[handler] json encode error", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, engine.APIError{Error: engine.APIErrorDetail{
		Message: msg,
		Type:    "invalid_request_error",
	}})
}

func writeSSE(w http.ResponseWriter, data []byte) {
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ── Route installer ──────────────────────────────────────────────────────────

// installHandlers registers all OpenAI-compatible HTTP routes on the given mux.
func (s *Server) installHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletion)
	mux.HandleFunc("POST /v1/completions", s.handleTextCompletion)
	mux.HandleFunc("POST /v1/embeddings", s.handleEmbedding)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /health", s.handleHealth)

	// Native endpoints (ollama-compatible).
	mux.HandleFunc("POST /completion", s.handleNativeCompletion)
	mux.HandleFunc("POST /embedding", s.handleNativeEmbedding)
}

// ── GET /health ──────────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := s.engine.Status()
	switch status {
	case engine.StatusReady:
		writeJSON(w, http.StatusOK, engine.HealthResponse{Status: "ok"})
	case engine.StatusLoading:
		writeJSON(w, http.StatusServiceUnavailable, engine.HealthResponse{Status: "loading", Progress: 0.5})
	default:
		writeJSON(w, http.StatusServiceUnavailable, engine.HealthResponse{Status: status.String()})
	}
}

// ── GET /v1/models ───────────────────────────────────────────────────────────

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, engine.ModelList{
		Object: "list",
		Data: []engine.ModelEntry{{
			ID:      s.modelName,
			Object:  "model",
			Created: s.created,
			OwnedBy: "local",
		}},
	})
}

// ── POST /v1/chat/completions ────────────────────────────────────────────────

func (s *Server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req engine.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	// Enable thinking from reasoning field if present.
	if req.Reasoning != nil || req.ReasoningEffort != nil {
		req.EnableThinking = engine.BoolPtr(true)
	}

	ch, err := s.engine.ChatCompletion(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		for chunk := range ch {
			data, err := json.Marshal(chunk)
			if err != nil {
				slog.Error("[handler] marshal stream chunk", "err", err)
				continue
			}
			writeSSE(w, data)
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	} else {
		// Non-streaming: collect the single chunk and convert to full response.
		var result engine.ChatCompletionChunk
		for chunk := range ch {
			result = chunk
		}

		resp := engine.ChatCompletionResponse{
			ID:                result.ID,
			Object:            "chat.completion",
			Created:           result.Created,
			Model:             result.Model,
			SystemFingerprint: result.SystemFingerprint,
		}

		if result.Usage != nil {
			resp.Usage = *result.Usage
		}

		for _, c := range result.Choices {
			resp.Choices = append(resp.Choices, engine.ChatChoice{
				Index: c.Index,
				Message: engine.ChatMessage{
					Role:      orDefault(c.Delta.Role, "assistant"),
					Content:   c.Delta.Content,
					Reasoning: c.Delta.Reasoning,
					ToolCalls: c.Delta.ToolCalls,
				},
				FinishReason: c.FinishReason,
				Logprobs:     c.Logprobs,
			})
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// ── POST /v1/completions ─────────────────────────────────────────────────────

func (s *Server) handleTextCompletion(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req engine.TextCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	ch, err := s.engine.TextCompletion(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		for chunk := range ch {
			data, err := json.Marshal(chunk)
			if err != nil {
				slog.Error("[handler] marshal text stream chunk", "err", err)
				continue
			}
			writeSSE(w, data)
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	} else {
		var result engine.TextCompletionChunk
		for chunk := range ch {
			result = chunk
		}

		resp := engine.TextCompletionResponse{
			ID:                result.ID,
			Object:            "text_completion",
			Created:           result.Created,
			Model:             result.Model,
			SystemFingerprint: result.SystemFingerprint,
			Choices:           result.Choices,
		}
		if result.Usage != nil {
			resp.Usage = *result.Usage
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// ── POST /v1/embeddings ──────────────────────────────────────────────────────

func (s *Server) handleEmbedding(w http.ResponseWriter, r *http.Request) {
	// Accept both OpenAI format {"input": "text"} and simple {"content": "text"}.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	var text string
	if v, ok := raw["input"]; ok {
		switch t := v.(type) {
		case string:
			text = t
		case []any:
			var parts []string
			for _, item := range t {
				if s, ok := item.(string); ok {
					parts = append(parts, s)
				}
			}
			text = strings.Join(parts, " ")
		}
	} else if v, ok := raw["content"]; ok {
		text, _ = v.(string)
	}

	if text == "" {
		writeError(w, http.StatusBadRequest, "input or content is required")
		return
	}

	embedding, promptTokens, err := s.engine.Embedding(r.Context(), text)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return in OpenAI embedding format.
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{{
			"object":    "embedding",
			"embedding": embedding,
			"index":     0,
		}},
		"model": s.modelName,
		"usage": map[string]int{
			"prompt_tokens": promptTokens,
			"total_tokens":  promptTokens,
		},
	})
}

// ── POST /completion (native) ────────────────────────────────────────────────

func (s *Server) handleNativeCompletion(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req engine.CompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	opts := req.Options
	if opts == nil {
		opts = &engine.CompletionOpts{}
	}

	sampling := &engine.SamplingConfig{
		Temperature:   defaultFloat32(opts.Temperature, 0.8),
		TopK:          defaultInt(opts.TopK, 40),
		TopP:          defaultFloat32(opts.TopP, 0.9),
		MinP:          opts.MinP,
		TypicalP:      defaultFloat32(opts.TypicalP, 1.0),
		RepeatLastN:   defaultInt(opts.RepeatLastN, 64),
		RepeatPenalty: defaultFloat32(opts.RepeatPenalty, 1.1),
		FreqPenalty:   opts.FrequencyPenalty,
		PresPenalty:   opts.PresencePenalty,
		Grammar:       req.Grammar,
	}

	numPredict := opts.NumPredict
	if numPredict == 0 {
		numPredict = -1
	}

	ch, err := s.engine.NativeCompletion(r.Context(), req.Prompt, req.Images, numPredict, opts.Stop, opts.NumKeep, sampling, req.Shift, req.Truncate, req.Logprobs, req.TopLogprobs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	for chunk := range ch {
		data, err := json.Marshal(chunk)
		if err != nil {
			slog.Error("[handler] marshal native chunk", "err", err)
			continue
		}
		w.Write(data)
		w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// ── POST /embedding (native) ─────────────────────────────────────────────────

func (s *Server) handleNativeEmbedding(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req engine.EmbeddingRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	embedding, promptTokens, err := s.engine.Embedding(r.Context(), req.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, engine.EmbeddingResponse{
		Embedding:       embedding,
		PromptEvalCount: promptTokens,
	})
}

// ── Helper utilities ─────────────────────────────────────────────────────────

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func defaultFloat32(v, fallback float32) float32 {
	if v == 0 {
		return fallback
	}
	return v
}

func defaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

// genID generates a random ID with the given prefix.
func genID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, rand.Intn(999999))
}
