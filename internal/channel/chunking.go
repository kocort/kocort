// Canonical implementation of channel text chunking utilities.
//
// Pure text-splitting functions for outbound channel delivery, including
// markdown-aware chunking that preserves fenced code blocks.
package channel

import (
	"strings"
	"unicode"

	"github.com/kocort/kocort/internal/config"
)

// ---------------------------------------------------------------------------
// Local interfaces for outbound capabilities (mirror runtime channel ifaces)
// ---------------------------------------------------------------------------

// OutboundTextChunkLimitProvider is implemented by channel outbounds that
// declare a preferred text chunk limit.
type OutboundTextChunkLimitProvider interface {
	TextChunkLimit() int
}

// OutboundTextChunker is implemented by channel outbounds that perform
// their own text chunking.
type OutboundTextChunker interface {
	ChunkText(text string, limit int) []string
}

// OutboundChunkerModeProvider is implemented by channel outbounds that
// declare a preferred chunker mode (e.g. "markdown", "text").
type OutboundChunkerModeProvider interface {
	ChunkerMode() string
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	DefaultChannelChunkLimit = 4000
	DefaultChannelChunkMode  = "length"
)

// ---------------------------------------------------------------------------
// Config resolution helpers
// ---------------------------------------------------------------------------

// ResolveChannelAccountChunkConfig returns account-specific chunk config, if any.
func ResolveChannelAccountChunkConfig(cfg config.ChannelConfig, accountID string) map[string]any {
	if len(cfg.Accounts) == 0 {
		return nil
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil
	}
	entry, ok := cfg.Accounts[accountID]
	if !ok {
		return nil
	}
	accountCfg, ok := entry.(map[string]any)
	if !ok {
		return nil
	}
	return accountCfg
}

// ResolveConfiguredChannelChunkLimit returns the configured chunk limit (account or channel level).
func ResolveConfiguredChannelChunkLimit(cfg config.ChannelConfig, accountID string) (int, bool) {
	if accountCfg := ResolveChannelAccountChunkConfig(cfg, accountID); len(accountCfg) > 0 {
		switch value := accountCfg["textChunkLimit"].(type) {
		case float64:
			if value > 0 {
				return int(value), true
			}
		case int:
			if value > 0 {
				return value, true
			}
		}
	}
	if cfg.TextChunkLimit > 0 {
		return cfg.TextChunkLimit, true
	}
	return 0, false
}

// ResolveChannelChunkLimit returns the effective chunk limit for a channel.
func ResolveChannelChunkLimit(cfg config.ChannelConfig, accountID string) int {
	if limit, ok := ResolveConfiguredChannelChunkLimit(cfg, accountID); ok {
		return limit
	}
	return DefaultChannelChunkLimit
}

// ResolveOutboundTextChunkLimit returns the effective chunk limit, considering
// outbound provider preferences. The outbound parameter is type-asserted to
// OutboundTextChunkLimitProvider.
func ResolveOutboundTextChunkLimit(outbound any, cfg config.ChannelConfig, accountID string) int {
	if provider, ok := outbound.(OutboundTextChunkLimitProvider); ok {
		if limit := provider.TextChunkLimit(); limit > 0 {
			if cfgLimit, hasCfgLimit := ResolveConfiguredChannelChunkLimit(cfg, accountID); hasCfgLimit {
				return cfgLimit
			}
			return limit
		}
	}
	return ResolveChannelChunkLimit(cfg, accountID)
}

// ResolveChannelChunkMode returns the effective chunk mode ("length" or "newline").
func ResolveChannelChunkMode(cfg config.ChannelConfig, accountID string) string {
	if accountCfg := ResolveChannelAccountChunkConfig(cfg, accountID); len(accountCfg) > 0 {
		if mode, ok := accountCfg["chunkMode"].(string); ok {
			trimmed := strings.TrimSpace(strings.ToLower(mode))
			if trimmed == "newline" {
				return "newline"
			}
			if trimmed == "length" {
				return "length"
			}
		}
	}
	mode := strings.TrimSpace(strings.ToLower(cfg.ChunkMode))
	if mode == "" {
		return DefaultChannelChunkMode
	}
	if mode == "newline" {
		return "newline"
	}
	return DefaultChannelChunkMode
}

// ---------------------------------------------------------------------------
// Outbound chunking entry points
// ---------------------------------------------------------------------------

// ChunkOutboundText splits text according to channel config chunk settings.
func ChunkOutboundText(text string, cfg config.ChannelConfig, accountID string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	limit := ResolveChannelChunkLimit(cfg, accountID)
	mode := ResolveChannelChunkMode(cfg, accountID)
	if limit <= 0 {
		return []string{text}
	}
	if mode != "newline" && len(text) <= limit {
		return []string{text}
	}
	return ChunkMarkdownTextWithMode(text, limit, mode)
}

// ChunkOutboundTextForAdapter splits text using adapter-specific chunking
// when available, falling back to config-based chunking. The outbound
// parameter is type-asserted to OutboundTextChunker and related interfaces.
func ChunkOutboundTextForAdapter(text string, outbound any, cfg config.ChannelConfig, accountID string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	limit := ResolveOutboundTextChunkLimit(outbound, cfg, accountID)
	if limit <= 0 {
		return []string{text}
	}
	if chunker, ok := outbound.(OutboundTextChunker); ok {
		mode := "text"
		if provider, ok := outbound.(OutboundChunkerModeProvider); ok {
			if trimmed := strings.TrimSpace(strings.ToLower(provider.ChunkerMode())); trimmed != "" {
				mode = trimmed
			}
		}
		channelMode := ResolveChannelChunkMode(cfg, accountID)
		if mode == "markdown" {
			blocks := ChunkMarkdownTextWithMode(text, limit, channelMode)
			if len(blocks) == 0 && text != "" {
				blocks = []string{text}
			}
			out := make([]string, 0, len(blocks))
			for _, block := range blocks {
				chunks := CompactNonEmpty(chunker.ChunkText(block, limit))
				if len(chunks) == 0 && strings.TrimSpace(block) != "" {
					chunks = []string{block}
				}
				out = append(out, chunks...)
			}
			return CompactNonEmpty(out)
		}
		if len(text) <= limit {
			return []string{text}
		}
		return CompactNonEmpty(chunker.ChunkText(text, limit))
	}
	return ChunkOutboundText(text, cfg, accountID)
}

// ---------------------------------------------------------------------------
// Core text-splitting functions
// ---------------------------------------------------------------------------

// ChunkByLength splits text into chunks of at most limit bytes.
func ChunkByLength(text string, limit int) []string {
	if text == "" {
		return nil
	}
	if limit <= 0 || len(text) <= limit {
		return []string{text}
	}
	out := make([]string, 0, (len(text)/limit)+1)
	for len(text) > 0 {
		if len(text) <= limit {
			out = append(out, strings.TrimSpace(text))
			break
		}
		out = append(out, strings.TrimSpace(text[:limit]))
		text = text[limit:]
	}
	return CompactNonEmpty(out)
}

// ChunkByParagraph splits text on paragraph boundaries (\n\n), merging
// adjacent paragraphs that fit within the limit.
func ChunkByParagraph(text string, limit int, splitLongParagraphs bool) []string {
	if text == "" {
		return nil
	}
	if limit <= 0 {
		return []string{text}
	}
	parts := SplitParagraphs(text)
	if len(parts) <= 1 {
		if !splitLongParagraphs {
			return []string{text}
		}
		return ChunkByLength(text, limit)
	}
	out := make([]string, 0, len(parts))
	current := ""
	for _, part := range parts {
		candidate := part
		if current != "" {
			candidate = current + "\n\n" + part
		}
		if len(candidate) <= limit {
			current = candidate
			continue
		}
		if current != "" {
			out = append(out, strings.TrimSpace(current))
			current = ""
		}
		if len(part) <= limit {
			current = part
			continue
		}
		if !splitLongParagraphs {
			out = append(out, part)
			continue
		}
		out = append(out, ChunkByLength(part, limit)...)
	}
	if strings.TrimSpace(current) != "" {
		out = append(out, strings.TrimSpace(current))
	}
	return CompactNonEmpty(out)
}

// ---------------------------------------------------------------------------
// Fence span parsing
// ---------------------------------------------------------------------------

// FenceSpan represents a fenced code block region in markdown text.
type FenceSpan struct {
	Start    int
	End      int
	OpenLine string
	Marker   string
	Indent   string
}

// ParseFenceSpans identifies all fenced code block spans in the buffer.
func ParseFenceSpans(buffer string) []FenceSpan {
	spans := make([]FenceSpan, 0)
	type openFence struct {
		start      int
		markerChar byte
		markerLen  int
		openLine   string
		marker     string
		indent     string
	}
	var open *openFence
	offset := 0
	for offset <= len(buffer) {
		nextNewline := strings.IndexByte(buffer[offset:], '\n')
		lineEnd := len(buffer)
		if nextNewline >= 0 {
			lineEnd = offset + nextNewline
		}
		line := buffer[offset:lineEnd]
		indent, marker, ok := MatchFenceLine(line)
		if ok {
			markerChar := marker[0]
			markerLen := len(marker)
			if open == nil {
				open = &openFence{
					start:      offset,
					markerChar: markerChar,
					markerLen:  markerLen,
					openLine:   line,
					marker:     marker,
					indent:     indent,
				}
			} else if open.markerChar == markerChar && markerLen >= open.markerLen {
				spans = append(spans, FenceSpan{
					Start:    open.start,
					End:      lineEnd,
					OpenLine: open.openLine,
					Marker:   open.marker,
					Indent:   open.indent,
				})
				open = nil
			}
		}
		if nextNewline < 0 {
			break
		}
		offset = lineEnd + 1
	}
	if open != nil {
		spans = append(spans, FenceSpan{
			Start:    open.start,
			End:      len(buffer),
			OpenLine: open.openLine,
			Marker:   open.marker,
			Indent:   open.indent,
		})
	}
	return spans
}

// MatchFenceLine checks whether a line is a markdown fence delimiter.
func MatchFenceLine(line string) (indent string, marker string, ok bool) {
	if line == "" {
		return "", "", false
	}
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	indent = line[:i]
	if i >= len(line) {
		return "", "", false
	}
	char := line[i]
	if char != '`' && char != '~' {
		return "", "", false
	}
	j := i
	for j < len(line) && line[j] == char {
		j++
	}
	if j-i < 3 {
		return "", "", false
	}
	return indent, line[i:j], true
}

// FindFenceSpanAt returns the fence span containing the given index, or nil.
func FindFenceSpanAt(spans []FenceSpan, index int) *FenceSpan {
	low, high := 0, len(spans)-1
	for low <= high {
		mid := (low + high) / 2
		span := spans[mid]
		if index <= span.Start {
			high = mid - 1
			continue
		}
		if index >= span.End {
			low = mid + 1
			continue
		}
		return &span
	}
	return nil
}

// IsSafeFenceBreak returns true if breaking at the given index would not
// split a fenced code block.
func IsSafeFenceBreak(spans []FenceSpan, index int) bool {
	return FindFenceSpanAt(spans, index) == nil
}

// ---------------------------------------------------------------------------
// Markdown-aware chunking
// ---------------------------------------------------------------------------

// ChunkMarkdownTextWithMode chunks text according to mode ("newline" or default).
func ChunkMarkdownTextWithMode(text string, limit int, mode string) []string {
	if mode == "newline" {
		paragraphChunks := ChunkByParagraph(text, limit, false)
		out := make([]string, 0, len(paragraphChunks))
		for _, chunk := range paragraphChunks {
			nested := ChunkMarkdownText(chunk, limit)
			if len(nested) == 0 && chunk != "" {
				out = append(out, chunk)
				continue
			}
			out = append(out, nested...)
		}
		return CompactNonEmpty(out)
	}
	return CompactNonEmpty(ChunkMarkdownText(text, limit))
}

// ChunkMarkdownText splits text respecting markdown fence boundaries.
func ChunkMarkdownText(text string, limit int) []string {
	if text == "" {
		return nil
	}
	if limit <= 0 || len(text) <= limit {
		return []string{text}
	}
	chunks := make([]string, 0)
	spans := ParseFenceSpans(text)
	start := 0
	var reopenFence *FenceSpan
	for start < len(text) {
		reopenPrefix := ""
		if reopenFence != nil {
			reopenPrefix = reopenFence.OpenLine + "\n"
		}
		contentLimit := chunkMax(1, limit-len(reopenPrefix))
		if len(text)-start <= contentLimit {
			finalChunk := reopenPrefix + text[start:]
			if finalChunk != "" {
				chunks = append(chunks, finalChunk)
			}
			break
		}
		windowEnd := chunkMin(len(text), start+contentLimit)
		softBreak := PickSafeBreakIndex(text, start, windowEnd, spans)
		breakIdx := windowEnd
		if softBreak > start {
			breakIdx = softBreak
		}
		initialFence := (*FenceSpan)(nil)
		if !IsSafeFenceBreak(spans, breakIdx) {
			initialFence = FindFenceSpanAt(spans, breakIdx)
		}
		fenceToSplit := initialFence
		if initialFence != nil {
			closeLine := initialFence.Indent + initialFence.Marker
			maxIdxIfNeedNewline := start + (contentLimit - (len(closeLine) + 1))
			if maxIdxIfNeedNewline <= start {
				fenceToSplit = nil
				breakIdx = windowEnd
			} else {
				minProgressIdx := chunkMin(len(text), chunkMax(start+1, initialFence.Start+len(initialFence.OpenLine)+2))
				maxIdxIfAlreadyNewline := start + (contentLimit - len(closeLine))
				pickedNewline := false
				lastNewline := strings.LastIndex(text[start:chunkMax(0, maxIdxIfAlreadyNewline)], "\n")
				for lastNewline >= 0 {
					candidateBreak := start + lastNewline + 1
					if candidateBreak < minProgressIdx {
						break
					}
					candidateFence := FindFenceSpanAt(spans, candidateBreak)
					if candidateFence != nil && candidateFence.Start == initialFence.Start {
						breakIdx = candidateBreak
						pickedNewline = true
						break
					}
					if lastNewline == 0 {
						lastNewline = -1
					} else {
						lastNewline = strings.LastIndex(text[start:start+lastNewline], "\n")
					}
				}
				if !pickedNewline {
					if minProgressIdx > maxIdxIfAlreadyNewline {
						fenceToSplit = nil
						breakIdx = windowEnd
					} else {
						breakIdx = chunkMax(minProgressIdx, maxIdxIfNeedNewline)
					}
				}
			}
			fenceAtBreak := FindFenceSpanAt(spans, breakIdx)
			if fenceAtBreak == nil || fenceAtBreak.Start != initialFence.Start {
				fenceToSplit = nil
			}
		}
		rawContent := text[start:breakIdx]
		if rawContent == "" {
			break
		}
		rawChunk := reopenPrefix + rawContent
		brokeOnSeparator := breakIdx < len(text) && unicode.IsSpace(rune(text[breakIdx]))
		nextStart := chunkMin(len(text), breakIdx+boolToInt(brokeOnSeparator))
		if fenceToSplit != nil {
			closeLine := fenceToSplit.Indent + fenceToSplit.Marker
			if strings.HasSuffix(rawChunk, "\n") {
				rawChunk += closeLine
			} else {
				rawChunk += "\n" + closeLine
			}
			reopenFence = fenceToSplit
		} else {
			nextStart = SkipLeadingNewlines(text, nextStart)
			reopenFence = nil
		}
		chunks = append(chunks, rawChunk)
		start = nextStart
	}
	return CompactNonEmpty(chunks)
}

// ---------------------------------------------------------------------------
// Break-point scanning
// ---------------------------------------------------------------------------

// SkipLeadingNewlines advances start past any leading '\n' characters.
func SkipLeadingNewlines(value string, start int) int {
	i := start
	for i < len(value) && value[i] == '\n' {
		i++
	}
	return i
}

// PickSafeBreakIndex finds the best break index that does not split a fence.
func PickSafeBreakIndex(text string, start int, end int, spans []FenceSpan) int {
	lastNewline, lastWhitespace := ScanParenAwareBreakpoints(text, start, end, func(index int) bool {
		return IsSafeFenceBreak(spans, index)
	})
	if lastNewline > start {
		return lastNewline
	}
	if lastWhitespace > start {
		return lastWhitespace
	}
	return -1
}

// ScanParenAwareBreakpoints scans text[start:end] for newline and whitespace
// break candidates, skipping positions inside parentheses.
func ScanParenAwareBreakpoints(text string, start int, end int, isAllowed func(index int) bool) (int, int) {
	lastNewline := -1
	lastWhitespace := -1
	depth := 0
	for i := start; i < end; i++ {
		if isAllowed != nil && !isAllowed(i) {
			continue
		}
		char := text[i]
		if char == '(' {
			depth++
			continue
		}
		if char == ')' && depth > 0 {
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		if char == '\n' {
			lastNewline = i
		} else if unicode.IsSpace(rune(char)) {
			lastWhitespace = i
		}
	}
	return lastNewline, lastWhitespace
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

func chunkMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func chunkMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// SplitParagraphs splits text on double-newline boundaries, respecting fence spans.
func SplitParagraphs(text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	parts := make([]string, 0)
	spans := ParseFenceSpans(normalized)
	lastIndex := 0
	for i := 0; i < len(normalized)-1; i++ {
		if normalized[i] != '\n' {
			continue
		}
		if normalized[i+1] != '\n' {
			continue
		}
		if !IsSafeFenceBreak(spans, i) {
			continue
		}
		parts = append(parts, normalized[lastIndex:i])
		for i+1 < len(normalized) && normalized[i+1] == '\n' {
			i++
		}
		lastIndex = i + 1
	}
	parts = append(parts, normalized[lastIndex:])
	return CompactNonEmpty(parts)
}

// CompactNonEmpty filters out empty/whitespace-only strings and trims the rest.
func CompactNonEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
