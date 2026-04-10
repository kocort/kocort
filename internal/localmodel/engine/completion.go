package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/localmodel/chatfmt"
	"github.com/kocort/kocort/internal/localmodel/ffi"
)

// ChatCompletion creates a streaming or non-streaming chat completion.
// Returns a channel of ChatCompletionChunk that the caller reads until closed.
func (e *Engine) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (<-chan ChatCompletionChunk, error) {
	if req.RawPrompt == "" && len(req.Messages) == 0 {
		return nil, fmt.Errorf("messages is required")
	}
	if e.status != StatusReady {
		return nil, fmt.Errorf("engine not ready (status: %s)", e.status)
	}

	var prompt string
	var images []ImageData
	var renderedMsgs []chatfmt.Message

	if req.RawPrompt != "" {
		prompt = req.RawPrompt
	} else {
		var err error
		prompt, images, renderedMsgs, err = e.buildPrompt(&req)
		if err != nil {
			return nil, err
		}
	}

	thinking := e.thinkingMode(&req)

	slog.Info("[engine] ChatCompletion", "prompt", prompt, "thinking", thinking)

	// Build sampling config.
	cfg := DefaultSamplingConfig()
	if req.Temperature != nil {
		cfg.Temperature = float32(*req.Temperature)
	} else {
		cfg.Temperature = 1.0
	}
	if req.TopP != nil {
		cfg.TopP = float32(*req.TopP)
	} else {
		cfg.TopP = 1.0
	}
	if req.FrequencyPenalty != nil {
		cfg.FreqPenalty = float32(*req.FrequencyPenalty)
	}
	if req.PresencePenalty != nil {
		cfg.PresPenalty = float32(*req.PresencePenalty)
	}
	if req.Seed != nil {
		cfg.Seed = uint32(*req.Seed)
	}

	stops := parseStopSequences(req.Stop)
	stops = append(stops, e.format.StopTokens()...)

	numPredict := -1
	if req.MaxTokens != nil {
		numPredict = *req.MaxTokens
	}

	var grammar string
	if req.ResponseFormat != nil {
		switch strings.ToLower(strings.TrimSpace(req.ResponseFormat.Type)) {
		case "json_object":
			grammar = `root ::= "{" ws (kv ("," ws kv)*)? "}" ws
kv   ::= string ":" ws value
value ::= string | number | "true" | "false" | "null" | object | array
object ::= "{" ws (kv ("," ws kv)*)? "}" ws
array  ::= "[" ws (value ("," ws value)*)? "]" ws
string ::= "\"" ([^"\\] | "\\" .)* "\""
number ::= "-"? [0-9]+ ("." [0-9]+)?
ws     ::= [ \t\n]*`
		case "json_schema":
			if req.ResponseFormat.JSONSchema != nil {
				g := ffi.SchemaToGrammar(req.ResponseFormat.JSONSchema)
				if g != nil {
					grammar = string(g)
				}
			}
		}
	}

	// If caller provides sampling override, use it.
	var samplingCfg *SamplingConfig
	if req.SamplingOverride != nil {
		samplingCfg = req.SamplingOverride
	} else {
		cfg.Grammar = grammar
		samplingCfg = &cfg
	}

	logprobs := req.Logprobs != nil && *req.Logprobs

	llamaSampling := &ffi.SamplingParams{
		TopK:           samplingCfg.TopK,
		TopP:           samplingCfg.TopP,
		MinP:           samplingCfg.MinP,
		TypicalP:       samplingCfg.TypicalP,
		Temp:           samplingCfg.Temperature,
		RepeatLastN:    samplingCfg.RepeatLastN,
		PenaltyRepeat:  samplingCfg.RepeatPenalty,
		PenaltyFreq:    samplingCfg.FreqPenalty,
		PenaltyPresent: samplingCfg.PresPenalty,
		Seed:           samplingCfg.Seed,
		Grammar:        samplingCfg.Grammar,
	}

	seq, err := e.newSequence(prompt, images, seqParams{
		NumPredict:  numPredict,
		Stop:        stops,
		NumKeep:     0,
		Sampling:    llamaSampling,
		Shift:       true,
		Truncate:    true,
		Logprobs:    logprobs,
		TopLogprobs: req.TopLogprobs,
	})
	if err != nil {
		return nil, fmt.Errorf("create sequence: %w", err)
	}

	if err := e.placeSequence(ctx, seq); err != nil {
		return nil, err
	}

	id := fmt.Sprintf("chatcmpl-%d", rand.Intn(999999))
	model := req.Model
	if model == "" {
		model = "local"
	}

	// Create parser: non-ChatML formats always need a parser;
	// ChatML only needs one when thinking or tools are present.
	var parser chatfmt.StreamParser
	fmtName := e.format.Name()
	needParser := fmtName != "chatml" || thinking != chatfmt.ThinkingOff || len(req.Tools) > 0
	if needParser {
		var lastMsg *chatfmt.Message
		if len(renderedMsgs) > 0 {
			lastMsg = &renderedMsgs[len(renderedMsgs)-1]
		}
		parser = e.format.NewParser(req.Tools, lastMsg, thinking)
	}

	out := make(chan ChatCompletionChunk, 128)
	go func() {
		defer close(out)
		if req.Stream {
			e.streamChat(ctx, seq, out, id, model, parser)
		} else {
			e.nonStreamChat(ctx, seq, out, id, model, parser)
		}
	}()

	return out, nil
}

// streamChat emits chunks as tokens are generated.
func (e *Engine) streamChat(ctx context.Context, seq *sequence, out chan<- ChatCompletionChunk, id, model string, parser chatfmt.StreamParser) {
	// Initial role chunk.
	out <- ChatCompletionChunk{
		ID: id, Object: "chat.completion.chunk", Created: time.Now().Unix(),
		Model: model, SystemFingerprint: "fp_local",
		Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{Role: "assistant"}}},
	}

	var toolCallSent bool

	for {
		select {
		case <-ctx.Done():
			e.cancelSeq(seq)
			return
		case frag, ok := <-seq.responses:
			if ok {
				slog.Info("[engine] streamChat got fragment", "text", frag.text, "logprobs_len", len(frag.logprobs))
				var lp *ChatLogprobs
				if len(frag.logprobs) > 0 {
					lp = &ChatLogprobs{Content: toOpenAILogprobs(frag.logprobs)}
				}

				if parser != nil {
					thinking, content, toolCalls := parser.Add(frag.text)
					if thinking != "" {
						out <- ChatCompletionChunk{
							ID: id, Object: "chat.completion.chunk", Created: time.Now().Unix(),
							Model: model, SystemFingerprint: "fp_local",
							Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{Reasoning: thinking}, Logprobs: lp}},
						}
						lp = nil
					}
					if len(toolCalls) > 0 {
						out <- ChatCompletionChunk{
							ID: id, Object: "chat.completion.chunk", Created: time.Now().Unix(),
							Model: model, SystemFingerprint: "fp_local",
							Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{ToolCalls: toolCalls}, Logprobs: lp}},
						}
						lp = nil
						toolCallSent = true
					}
					if content != "" {
						out <- ChatCompletionChunk{
							ID: id, Object: "chat.completion.chunk", Created: time.Now().Unix(),
							Model: model, SystemFingerprint: "fp_local",
							Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{Content: content}, Logprobs: lp}},
						}
					}
				} else {
					out <- ChatCompletionChunk{
						ID: id, Object: "chat.completion.chunk", Created: time.Now().Unix(),
						Model: model, SystemFingerprint: "fp_local",
						Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{Content: frag.text}, Logprobs: lp}},
					}
				}
			} else {
				fr := toFinishReason(seq.doneReason)
				if toolCallSent {
					s := "tool_calls"
					fr = &s
				}
				out <- ChatCompletionChunk{
					ID: id, Object: "chat.completion.chunk", Created: time.Now().Unix(),
					Model: model, SystemFingerprint: "fp_local",
					Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{}, FinishReason: fr}},
				}
				return
			}
		}
	}
}

// nonStreamChat collects all tokens and emits a single chunk.
func (e *Engine) nonStreamChat(ctx context.Context, seq *sequence, out chan<- ChatCompletionChunk, id, model string, parser chatfmt.StreamParser) {
	var sb strings.Builder
	var allLogprobs []LogprobEntry

	for {
		select {
		case <-ctx.Done():
			e.cancelSeq(seq)
			return
		case frag, ok := <-seq.responses:
			if ok {
				sb.WriteString(frag.text)
				allLogprobs = append(allLogprobs, frag.logprobs...)
			} else {
				content := sb.String()
				var reasoning string
				var toolCalls []ToolCall
				if parser != nil {
					reasoning, content, toolCalls = parser.Add(content)
				}

				fr := toFinishReason(seq.doneReason)
				if len(toolCalls) > 0 {
					s := "tool_calls"
					fr = &s
				}

				chunk := ChatCompletionChunk{
					ID: id, Object: "chat.completion.chunk", Created: time.Now().Unix(),
					Model: model, SystemFingerprint: "fp_local",
					Choices: []ChunkChoice{{
						Index:        0,
						Delta:        ChunkDelta{Role: "assistant", Content: content, Reasoning: reasoning, ToolCalls: toolCalls},
						FinishReason: fr,
					}},
					Usage: &ChatCompletionUsage{
						PromptTokens:     seq.numPromptInput,
						CompletionTokens: seq.numDecoded,
						TotalTokens:      seq.numPromptInput + seq.numDecoded,
					},
				}

				if len(allLogprobs) > 0 {
					chunk.Choices[0].Logprobs = &ChatLogprobs{Content: toOpenAILogprobs(allLogprobs)}
				}

				out <- chunk
				return
			}
		}
	}
}

// TextCompletion creates a text (non-chat) completion.
func (e *Engine) TextCompletion(ctx context.Context, req TextCompletionRequest) (<-chan TextCompletionChunk, error) {
	if e.status != StatusReady {
		return nil, fmt.Errorf("engine not ready (status: %s)", e.status)
	}

	cfg := DefaultSamplingConfig()
	if req.Temperature != nil {
		cfg.Temperature = float32(*req.Temperature)
	} else {
		cfg.Temperature = 1.0
	}
	if req.TopP != 0 {
		cfg.TopP = float32(req.TopP)
	} else {
		cfg.TopP = 1.0
	}
	if req.Seed != nil {
		cfg.Seed = uint32(*req.Seed)
	}

	numPredict := -1
	if req.MaxTokens != nil {
		numPredict = *req.MaxTokens
	}

	llamaSampling := &ffi.SamplingParams{
		TopK:     cfg.TopK,
		TopP:     cfg.TopP,
		MinP:     cfg.MinP,
		TypicalP: cfg.TypicalP,
		Temp:     cfg.Temperature,
		Seed:     cfg.Seed,
	}

	stops := parseStopSequences(req.Stop)

	seq, err := e.newSequence(req.Prompt, nil, seqParams{
		NumPredict: numPredict,
		Stop:       stops,
		Sampling:   llamaSampling,
		Shift:      true,
		Truncate:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("create sequence: %w", err)
	}

	if err := e.placeSequence(ctx, seq); err != nil {
		return nil, err
	}

	id := fmt.Sprintf("cmpl-%d", rand.Intn(999999))
	model := req.Model
	if model == "" {
		model = "local"
	}

	out := make(chan TextCompletionChunk, 128)
	go func() {
		defer close(out)
		if req.Stream {
			e.streamText(ctx, seq, out, id, model)
		} else {
			e.nonStreamText(ctx, seq, out, id, model)
		}
	}()

	return out, nil
}

func (e *Engine) streamText(ctx context.Context, seq *sequence, out chan<- TextCompletionChunk, id, model string) {
	for {
		select {
		case <-ctx.Done():
			e.cancelSeq(seq)
			return
		case frag, ok := <-seq.responses:
			if ok {
				out <- TextCompletionChunk{
					ID: id, Object: "text_completion", Created: time.Now().Unix(),
					Model: model, SystemFingerprint: "fp_local",
					Choices: []TextCompletionChoice{{Text: frag.text, Index: 0}},
				}
			} else {
				out <- TextCompletionChunk{
					ID: id, Object: "text_completion", Created: time.Now().Unix(),
					Model: model, SystemFingerprint: "fp_local",
					Choices: []TextCompletionChoice{{Index: 0, FinishReason: toFinishReason(seq.doneReason)}},
				}
				return
			}
		}
	}
}

func (e *Engine) nonStreamText(ctx context.Context, seq *sequence, out chan<- TextCompletionChunk, id, model string) {
	var sb strings.Builder

	for {
		select {
		case <-ctx.Done():
			e.cancelSeq(seq)
			return
		case frag, ok := <-seq.responses:
			if ok {
				sb.WriteString(frag.text)
			} else {
				out <- TextCompletionChunk{
					ID: id, Object: "text_completion", Created: time.Now().Unix(),
					Model: model, SystemFingerprint: "fp_local",
					Choices: []TextCompletionChoice{{
						Text: sb.String(), Index: 0, FinishReason: toFinishReason(seq.doneReason),
					}},
					Usage: &ChatCompletionUsage{
						PromptTokens:     seq.numPromptInput,
						CompletionTokens: seq.numDecoded,
						TotalTokens:      seq.numPromptInput + seq.numDecoded,
					},
				}
				return
			}
		}
	}
}

// Embedding computes the embedding for the given text.
func (e *Engine) Embedding(ctx context.Context, text string) ([]float32, int, error) {
	if e.status != StatusReady {
		return nil, 0, fmt.Errorf("engine not ready (status: %s)", e.status)
	}

	seq, err := e.newSequence(text, nil, seqParams{
		Embedding: true,
		Truncate:  false,
	})
	if err != nil {
		return nil, 0, err
	}

	if err := e.placeSequence(ctx, seq); err != nil {
		return nil, 0, err
	}

	select {
	case embed := <-seq.embedding:
		return embed, seq.numPromptInput, nil
	case <-ctx.Done():
		e.cancelSeq(seq)
		return nil, 0, ctx.Err()
	}
}

// ── Conversion helpers ───────────────────────────────────────────────────────

func toFinishReason(d DoneReason) *string {
	var s string
	switch d {
	case DoneStop:
		s = "stop"
	case DoneLength:
		s = "length"
	default:
		return nil
	}
	return &s
}

func toOpenAILogprobs(lps []LogprobEntry) []OpenAILogprob {
	result := make([]OpenAILogprob, len(lps))
	for i, lp := range lps {
		result[i] = OpenAILogprob{
			Token:   lp.Token,
			Logprob: lp.Logprob,
		}
		if len(lp.TopLogprobs) > 0 {
			result[i].TopLogprobs = make([]OpenAITokenLogprob, len(lp.TopLogprobs))
			for j, tlp := range lp.TopLogprobs {
				result[i].TopLogprobs[j] = OpenAITokenLogprob{Token: tlp.Token, Logprob: tlp.Logprob}
			}
		}
	}
	return result
}
