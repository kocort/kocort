package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// FallbackAttempt records one backend invocation attempt during fallback.
type FallbackAttempt struct {
	Provider string
	Model    string
	Error    string
	Reason   string
}

// ModelFallbackResult holds the outcome of a fallback run including the
// successful result and all prior failed attempts.
type ModelFallbackResult struct {
	Result   core.AgentRunResult
	Provider string
	Model    string
	Attempts []FallbackAttempt
}

// CandidateRunner is a function that attempts a backend run for a specific
// provider/model/thinkLevel combination.  The caller is responsible for
// closing over any additional context (e.g. AgentRunContext).
type CandidateRunner func(ctx context.Context, provider, model string, thinkLevel string, isFallbackRetry bool) (core.AgentRunResult, error)

// RunWithModelFallback iterates through the fallback candidates in
// selection.Fallbacks, calling run for each until one succeeds.
func RunWithModelFallback(
	ctx context.Context,
	selection core.ModelSelection,
	run CandidateRunner,
) (ModelFallbackResult, error) {
	candidates := selection.Fallbacks
	if len(candidates) == 0 {
		candidates = []core.ModelCandidate{{Provider: selection.Provider, Model: selection.Model}}
	}
	var attempts []FallbackAttempt
	var lastErr error
	for i, candidate := range candidates {
		result, err := run(ctx, candidate.Provider, candidate.Model, selection.ThinkLevel, i > 0)
		if err == nil {
			return ModelFallbackResult{
				Result:   result,
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Attempts: attempts,
			}, nil
		}
		lastErr = err
		attempts = append(attempts, FallbackAttempt{
			Provider: candidate.Provider,
			Model:    candidate.Model,
			Error:    err.Error(),
			Reason:   "unknown",
		})
	}
	if len(attempts) == 1 && lastErr != nil {
		return ModelFallbackResult{}, lastErr
	}
	var parts []string
	for _, attempt := range attempts {
		part := attempt.Provider + "/" + attempt.Model + ": " + attempt.Error
		if attempt.Reason != "" {
			part += " (" + attempt.Reason + ")"
		}
		parts = append(parts, part)
	}
	return ModelFallbackResult{}, fmt.Errorf("all models failed (%d): %s", len(attempts), strings.Join(parts, " | "))
}
