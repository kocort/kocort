package engine

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kocort/kocort/internal/localmodel/ffi"
)

// sequence tracks one in-flight generation request through the engine's decode loop.
type sequence struct {
	// iBatch is the batch index of this sequence's last output token.
	iBatch int

	// numPredicted counts how many tokens have been generated so far.
	numPredicted int

	// inputs still awaiting evaluation.
	inputs []input

	// pendingInputs are added to a Batch but not yet submitted to Decode.
	pendingInputs []input

	// pendingResponses buffers generated token strings for stop-sequence checking.
	pendingResponses []string

	// pendingLogprobs buffers logprob entries for buffered tokens.
	pendingLogprobs []LogprobEntry

	// slot is the assigned KV cache slot.
	slot *kvSlot

	// responses is the channel where decoded fragments are sent.
	responses chan fragment

	// quit is a signal channel to abort this sequence early.
	quit chan struct{}

	// embedding is used in embedding-only mode to return the result.
	embedding chan []float32

	// sampler is the token sampling context.
	sampler *ffi.SamplingContext

	// numPredict is the max tokens to generate (<=0 means unlimited).
	numPredict int

	// stop sequences that terminate generation.
	stop []string

	// numKeep is how many initial tokens to preserve when shifting context.
	numKeep int

	// embeddingOnly means only compute embeddings, don't generate tokens.
	embeddingOnly bool

	// shift enables context shifting when the window is full.
	shift bool

	// logprobs indicates whether to compute log probabilities.
	logprobs bool

	// topLogprobs is how many top-K logprobs to return per token.
	topLogprobs int

	// doneReason records why this sequence finished.
	doneReason DoneReason

	// timing metrics
	promptDuration time.Duration
	genDuration    time.Duration
	numDecoded     int
	numPromptInput int
}

// seqParams collects parameters for creating a new sequence.
type seqParams struct {
	NumPredict  int
	Stop        []string
	NumKeep     int
	Sampling    *ffi.SamplingParams
	Embedding   bool
	Shift       bool
	Truncate    bool
	Logprobs    bool
	TopLogprobs int
}

// ── Stop-sequence utilities ──────────────────────────────────────────────────

// matchStop checks if any stop string is present in the accumulated text.
func matchStop(text string, stops []string) (bool, string) {
	for _, s := range stops {
		if strings.Contains(text, s) {
			return true, s
		}
	}
	return false, ""
}

// hasStopSuffix returns true if text ends with a prefix of any stop string.
func hasStopSuffix(text string, stops []string) bool {
	for _, s := range stops {
		for i := 1; i <= len(s); i++ {
			if strings.HasSuffix(text, s[:i]) {
				return true
			}
		}
	}
	return false
}

// trimStop removes a stop string from the buffered pieces and returns
// whether the last piece was partially truncated.
func trimStop(pieces []string, stop string) ([]string, bool) {
	joined := strings.Join(pieces, "")
	idx := strings.Index(joined, stop)
	if idx == -1 {
		return pieces, false
	}
	joined = joined[:idx]

	var result []string
	truncated := false
	pos := 0
	for _, piece := range pieces {
		if pos >= len(joined) {
			break
		}
		end := pos + len(piece)
		if end > len(joined) {
			end = len(joined)
			truncated = true
		}
		result = append(result, joined[pos:end])
		pos = end
	}
	return result, truncated
}

// hasIncompleteUTF8 returns true if text ends with an incomplete multibyte sequence.
func hasIncompleteUTF8(text string) bool {
	if len(text) == 0 {
		return false
	}
	for i := 1; i < 5 && i <= len(text); i++ {
		c := text[len(text)-i]
		if (c & 0xc0) == 0x80 {
			continue
		}
		if (c & 0xe0) == 0xc0 {
			return i < 2
		}
		if (c & 0xf0) == 0xe0 {
			return i < 3
		}
		if (c & 0xf8) == 0xf0 {
			return i < 4
		}
		break
	}
	return false
}

// flush sends all buffered responses through the sequence channel.
// Returns false if the sequence was aborted.
func (s *sequence) flush() bool {
	joined := strings.Join(s.pendingResponses, "")
	lps := s.pendingLogprobs
	s.pendingResponses = s.pendingResponses[:0]
	s.pendingLogprobs = s.pendingLogprobs[:0]

	// Strip trailing invalid UTF-8.
	for !utf8.ValidString(joined) {
		joined = joined[:len(joined)-1]
	}
	if len(joined) == 0 {
		return true
	}

	select {
	case s.responses <- fragment{text: joined, logprobs: lps}:
		return true
	case <-s.quit:
		return false
	}
}
