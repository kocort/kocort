package tool

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Shared truncation & output utilities — mirrors pi-coding-agent truncate.ts
// ---------------------------------------------------------------------------

const (
	defaultMaxOutputLines    = 2000
	defaultMaxOutputBytes    = 50 * 1024 // 50 KB
	defaultGrepMaxLineLength = 500
)

// truncationResult describes how content was truncated.
type truncationResult struct {
	Content               string
	Truncated             bool
	TruncatedBy           string // "lines" or "bytes"
	TotalLines            int
	TotalBytes            int
	OutputLines           int
	OutputBytes           int
	FirstLineExceedsLimit bool
}

// truncateOutputHead keeps the first N lines / bytes (head truncation).
func truncateOutputHead(content string, maxLines, maxBytes int) truncationResult {
	if maxLines <= 0 {
		maxLines = defaultMaxOutputLines
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxOutputBytes
	}
	totalBytes := len(content)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return truncationResult{
			Content:     content,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
		}
	}

	// First line alone exceeds byte limit.
	if len(lines[0]) > maxBytes {
		return truncationResult{
			Truncated:             true,
			TruncatedBy:           "bytes",
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			FirstLineExceedsLimit: true,
		}
	}

	out := make([]string, 0, min(totalLines, maxLines))
	outBytes := 0
	truncatedBy := "lines"
	for i := 0; i < len(lines) && i < maxLines; i++ {
		lb := len(lines[i])
		if i > 0 {
			lb++ // newline separator
		}
		if outBytes+lb > maxBytes {
			truncatedBy = "bytes"
			break
		}
		out = append(out, lines[i])
		outBytes += lb
	}
	if len(out) >= maxLines && outBytes <= maxBytes {
		truncatedBy = "lines"
	}
	outputContent := strings.Join(out, "\n")
	return truncationResult{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(out),
		OutputBytes: len(outputContent),
	}
}

// truncateLineContent truncates a single line to maxChars runes,
// appending "... [truncated]" when clipped.
func truncateLineContent(line string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = defaultGrepMaxLineLength
	}
	if utf8.RuneCountInString(line) <= maxChars {
		return line, false
	}
	runes := []rune(line)
	return string(runes[:maxChars]) + "... [truncated]", true
}

// formatOutputSize formats bytes as human-readable size.
func formatOutputSize(bytes int) string {
	if bytes >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
	if bytes >= 1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%dB", bytes)
}

// ---------------------------------------------------------------------------
// Binary detection
// ---------------------------------------------------------------------------

// isBinaryContent returns true if the data appears to be binary (contains
// null bytes in the first 8 KiB).
func isBinaryContent(data []byte) bool {
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	for _, b := range check {
		if b == 0 {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Common ignore directories (used by pure-Go fallback when rg/fd unavailable)
// ---------------------------------------------------------------------------

var defaultIgnoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".tox":         true,
	".venv":        true,
	"venv":         true,
	".next":        true,
	".nuxt":        true,
	".cache":       true,
}

// ---------------------------------------------------------------------------
// Glob pattern matching with ** (doublestar) support
// ---------------------------------------------------------------------------

// matchGlobPattern matches a glob pattern (which may contain **) against a
// forward-slash normalised relative file path.
//
// Supported patterns:
//
//	**/*.ext        → match basename anywhere
//	prefix/**       → everything under prefix
//	prefix/**/*.ext → match basename under prefix
//	*.ext           → match basename (single segment)
func matchGlobPattern(pattern, relPath string) bool {
	pattern = filepath.ToSlash(pattern)
	relPath = filepath.ToSlash(relPath)

	if !strings.Contains(pattern, "**") {
		ok, _ := filepath.Match(pattern, relPath)
		if ok {
			return true
		}
		// Single-segment patterns: also match against basename.
		if !strings.Contains(pattern, "/") {
			ok, _ = filepath.Match(pattern, pathBase(relPath))
			return ok
		}
		return false
	}

	// **/*.ext or **/name — match against basename anywhere.
	if strings.HasPrefix(pattern, "**/") {
		rest := pattern[3:]
		if !strings.Contains(rest, "**") && !strings.Contains(rest, "/") {
			ok, _ := filepath.Match(rest, pathBase(relPath))
			return ok
		}
	}

	// prefix/** — everything under prefix.
	if strings.HasSuffix(pattern, "/**") {
		prefix := pattern[:len(pattern)-3]
		return relPath == prefix || strings.HasPrefix(relPath, prefix+"/")
	}

	// prefix/**/rest
	if idx := strings.Index(pattern, "/**/"); idx >= 0 {
		prefix := pattern[:idx]
		rest := pattern[idx+4:]
		if !strings.HasPrefix(relPath, prefix+"/") {
			return false
		}
		remaining := relPath[len(prefix)+1:]
		if !strings.Contains(rest, "**") && !strings.Contains(rest, "/") {
			ok, _ := filepath.Match(rest, pathBase(remaining))
			return ok
		}
		return matchGlobPattern(rest, remaining)
	}

	// Last resort: match basename with last component of pattern.
	basePat := pathBase(pattern)
	if !strings.Contains(basePat, "**") {
		ok, _ := filepath.Match(basePat, pathBase(relPath))
		return ok
	}
	return false
}

// pathBase returns the last element of a forward-slash path.
func pathBase(p string) string {
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}
