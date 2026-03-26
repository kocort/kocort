package tool

import "strings"

// ---------------------------------------------------------------------------
// Fuzzy matching, BOM handling, line-ending utilities
// Mirrors pi-coding-agent edit-diff.ts
// ---------------------------------------------------------------------------

// detectLineEnding returns the dominant line ending in content.
func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx < 0 {
		return "\n"
	}
	if crlfIdx >= 0 && crlfIdx <= lfIdx {
		return "\r\n"
	}
	return "\n"
}

// normalizeToLF normalizes all line endings to LF.
func normalizeToLF(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// restoreLineEndings converts LF back to the specified ending.
func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// normalizeForFuzzyMatch strips trailing whitespace per line and normalises
// smart quotes, dashes, and special spaces to their ASCII equivalents.
func normalizeForFuzzyMatch(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	result := strings.Join(lines, "\n")
	r := strings.NewReplacer(
		// Smart single quotes → '
		"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'",
		// Smart double quotes → "
		"\u201C", "\"", "\u201D", "\"", "\u201E", "\"", "\u201F", "\"",
		// Various dashes/hyphens → -
		"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-",
		"\u2014", "-", "\u2015", "-", "\u2212", "-",
		// Special spaces → regular space
		"\u00A0", " ", "\u202F", " ", "\u205F", " ", "\u3000", " ",
	)
	result = r.Replace(result)
	// Unicode spaces U+2002 – U+200A
	for c := '\u2002'; c <= '\u200A'; c++ {
		result = strings.ReplaceAll(result, string(c), " ")
	}
	return result
}

// ---------------------------------------------------------------------------
// Fuzzy find
// ---------------------------------------------------------------------------

// fuzzyFindResult holds the result of a fuzzy text search.
type fuzzyFindResult struct {
	Found                 bool
	Index                 int
	MatchLength           int
	UsedFuzzyMatch        bool
	ContentForReplacement string // the (possibly normalised) content that should be used for replacement
}

// fuzzyFindText tries an exact match first, then falls back to fuzzy
// matching (whitespace + Unicode normalisation).
func fuzzyFindText(content, oldText string) fuzzyFindResult {
	// Exact match
	idx := strings.Index(content, oldText)
	if idx >= 0 {
		return fuzzyFindResult{
			Found:                 true,
			Index:                 idx,
			MatchLength:           len(oldText),
			ContentForReplacement: content,
		}
	}
	// Fuzzy match
	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	fuzzyIdx := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIdx < 0 {
		return fuzzyFindResult{Found: false, Index: -1}
	}
	return fuzzyFindResult{
		Found:                 true,
		Index:                 fuzzyIdx,
		MatchLength:           len(fuzzyOldText),
		UsedFuzzyMatch:        true,
		ContentForReplacement: fuzzyContent,
	}
}

// fuzzyCount counts occurrences of oldText (or its fuzzy equivalent) in
// content.  The exact check is tried first; if that yields zero the fuzzy
// normalised variants are compared instead.
func fuzzyCount(content, oldText string) int {
	n := strings.Count(content, oldText)
	if n > 0 {
		return n
	}
	return strings.Count(normalizeForFuzzyMatch(content), normalizeForFuzzyMatch(oldText))
}

// ---------------------------------------------------------------------------
// BOM handling
// ---------------------------------------------------------------------------

// stripBom strips a UTF-8 BOM from the front of content.
func stripBom(content string) (bom string, text string) {
	if strings.HasPrefix(content, "\xEF\xBB\xBF") {
		return "\xEF\xBB\xBF", content[3:]
	}
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", strings.TrimPrefix(content, "\uFEFF")
	}
	return "", content
}
