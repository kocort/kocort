package infra

// ContextBudget manages token budgets for prompt context allocation.
//
// It translates a model's context-window size into byte-level limits that
// LoadPromptContextFiles and friends can enforce. The heuristic is
// 1 token ≈ 4 characters (bytes in ASCII/Latin text). For CJK-heavy content
// the ratio is lower, but 4 is a safe conservative default.
type ContextBudget struct {
	// MaxContextTokens is the model's full context window.
	MaxContextTokens int
	// ReservedForOutput is the number of tokens reserved for generation.
	ReservedForOutput int
	// SystemPromptEst is the estimated system-prompt token count.
	SystemPromptEst int
	// HistoryEst is the estimated history/messages token count.
	HistoryEst int
	// Remaining is the remaining available tokens after reserves.
	Remaining int
}

// charsPerToken is the rough chars-per-token ratio used for estimation.
const charsPerToken = 4

// defaultMaxContextTokens is used when the model config doesn't specify a context window.
const defaultMaxContextTokens = 128_000

// defaultMaxOutputTokens is the default output-token reservation.
const defaultMaxOutputTokens = 8_192

// singleFileFraction is the max fraction of the context-file budget a
// single file may consume.
const singleFileFraction = 0.15

// totalFilesFraction is the max fraction of the available context budget
// that all context files combined may consume.
const totalFilesFraction = 0.40

// EstimateTokens returns a rough token estimate for a given text string
// using the chars/4 heuristic.
func EstimateTokens(text string) int {
	n := len(text)
	if n == 0 {
		return 0
	}
	return (n + charsPerToken - 1) / charsPerToken // ceiling division
}

// NewContextBudget creates a ContextBudget from model configuration values.
// If maxContext or maxOutput are ≤ 0, sensible defaults are applied.
func NewContextBudget(maxContext, maxOutput int) *ContextBudget {
	if maxContext <= 0 {
		maxContext = defaultMaxContextTokens
	}
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutputTokens
	}
	remaining := maxContext - maxOutput
	if remaining < 1024 {
		remaining = 1024 // absolute floor
	}
	return &ContextBudget{
		MaxContextTokens:  maxContext,
		ReservedForOutput: maxOutput,
		Remaining:         remaining,
	}
}

// FitsInBudget reports whether the given token count fits in the remaining budget.
func (b *ContextBudget) FitsInBudget(tokens int) bool {
	return tokens <= b.Remaining
}

// Consume deducts tokens from the remaining budget (e.g. after accounting
// for history or system prompt). It never drops below zero.
func (b *ContextBudget) Consume(tokens int) {
	b.Remaining -= tokens
	if b.Remaining < 0 {
		b.Remaining = 0
	}
}

// SingleFileTokenLimit returns the maximum tokens a single context file may
// use, computed as Remaining × singleFileFraction.
func (b *ContextBudget) SingleFileTokenLimit() int {
	limit := int(float64(b.Remaining) * singleFileFraction)
	if limit < 256 {
		limit = 256
	}
	return limit
}

// TotalFilesTokenLimit returns the maximum tokens all context files combined
// may use, computed as Remaining × totalFilesFraction.
func (b *ContextBudget) TotalFilesTokenLimit() int {
	limit := int(float64(b.Remaining) * totalFilesFraction)
	if limit < 512 {
		limit = 512
	}
	return limit
}

// SingleFileByteLimit converts the single-file token limit to a byte limit.
func (b *ContextBudget) SingleFileByteLimit() int {
	return b.SingleFileTokenLimit() * charsPerToken
}

// TotalFilesByteLimit converts the total-files token limit to a byte limit.
func (b *ContextBudget) TotalFilesByteLimit() int {
	return b.TotalFilesTokenLimit() * charsPerToken
}

// AllocateContextFiles filters and truncates context files so they fit
// within the budget. Files are processed in order; earlier files have
// priority. Returns the surviving (possibly truncated) slice.
func (b *ContextBudget) AllocateContextFiles(files []PromptContextFile) []PromptContextFile {
	if len(files) == 0 {
		return nil
	}

	singleByteLimit := b.SingleFileByteLimit()
	totalByteLimit := b.TotalFilesByteLimit()

	var out []PromptContextFile
	totalUsed := 0

	for _, f := range files {
		content := f.Content
		if len(content) == 0 {
			continue
		}

		truncated := f.Truncated

		// Enforce single-file limit.
		if len(content) > singleByteLimit {
			content = content[:singleByteLimit]
			truncated = true
		}

		// Enforce total budget.
		if totalUsed+len(content) > totalByteLimit {
			remaining := totalByteLimit - totalUsed
			if remaining <= 0 {
				break
			}
			content = content[:remaining]
			truncated = true
		}

		if len(content) == 0 {
			continue
		}

		out = append(out, PromptContextFile{
			Path:      f.Path,
			Title:     f.Title,
			Content:   content,
			Truncated: truncated,
		})
		totalUsed += len(content)
	}
	return out
}
