package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/kocort/kocort/internal/localmodel/ffi"
)

// NativeCompletion runs a raw prompt through the engine and streams CompletionChunks.
func (e *Engine) NativeCompletion(ctx context.Context, prompt string, images []ImageData, numPredict int, stops []string, numKeep int, sampling *SamplingConfig, shift, truncate, logprobs bool, topLogprobs int) (<-chan CompletionChunk, error) {
	if e.status != StatusReady {
		return nil, fmt.Errorf("engine not ready")
	}

	llamaSampling := toLlamaSampling(sampling)

	seq, err := e.newSequence(prompt, images, seqParams{
		NumPredict:  numPredict,
		Stop:        stops,
		NumKeep:     numKeep,
		Sampling:    llamaSampling,
		Shift:       shift,
		Truncate:    truncate,
		Logprobs:    logprobs,
		TopLogprobs: topLogprobs,
	})
	if err != nil {
		return nil, err
	}

	if err := e.placeSequence(ctx, seq); err != nil {
		return nil, err
	}

	out := make(chan CompletionChunk, 128)
	go func() {
		defer close(out)
		start := time.Now()
		var promptDone bool

		for {
			select {
			case <-ctx.Done():
				e.cancelSeq(seq)
				return
			case frag, ok := <-seq.responses:
				if !promptDone {
					promptDone = true
				}
				if ok {
					out <- CompletionChunk{
						Content:  frag.text,
						Logprobs: frag.logprobs,
					}
				} else {
					elapsed := time.Since(start)
					out <- CompletionChunk{
						Done:               true,
						DoneReason:         seq.doneReason.String(),
						PromptEvalCount:    seq.numPromptInput,
						PromptEvalDuration: seq.promptDuration,
						EvalCount:          seq.numDecoded,
						EvalDuration:       elapsed,
					}
					return
				}
			}
		}
	}()

	return out, nil
}

// toLlamaSampling converts a SamplingConfig to ffi.SamplingParams.
func toLlamaSampling(cfg *SamplingConfig) *ffi.SamplingParams {
	if cfg == nil {
		return nil
	}
	return &ffi.SamplingParams{
		TopK:           cfg.TopK,
		TopP:           cfg.TopP,
		MinP:           cfg.MinP,
		TypicalP:       cfg.TypicalP,
		Temp:           cfg.Temperature,
		RepeatLastN:    cfg.RepeatLastN,
		PenaltyRepeat:  cfg.RepeatPenalty,
		PenaltyFreq:    cfg.FreqPenalty,
		PenaltyPresent: cfg.PresPenalty,
		Seed:           cfg.Seed,
		Grammar:        cfg.Grammar,
	}
}
