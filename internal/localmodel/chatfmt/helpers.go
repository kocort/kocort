package chatfmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ── Public helpers ───────────────────────────────────────────────────────────

// ContentWithImages renders the message content with [img-N] placeholders.
func (m Message) ContentWithImages(imgOffset int) string {
	if m.ImageCount == 0 {
		return m.Content
	}
	var sb strings.Builder
	for i := 0; i < m.ImageCount; i++ {
		sb.WriteString(fmt.Sprintf("[img-%d]", imgOffset+i))
	}
	sb.WriteString(m.Content)
	return sb.String()
}

// splitReasoning extracts reasoning from content containing <think> tags.
func splitReasoning(content, reasoning string, thinkingEnabled bool) (string, string) {
	if thinkingEnabled && reasoning != "" {
		return strings.TrimSpace(reasoning), content
	}
	if idx := strings.Index(content, thinkClose); idx != -1 {
		before := content[:idx]
		if open := strings.LastIndex(before, thinkOpen); open != -1 {
			reasoning = before[open+len(thinkOpen):]
		} else {
			reasoning = before
		}
		content = strings.TrimLeft(content[idx+len(thinkClose):], "\n")
	}
	return strings.TrimSpace(reasoning), content
}

// marshalSpaced marshals JSON with spaces after : and , (matches model training format).
func marshalSpaced(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(b)+len(b)/8)
	inStr := false
	esc := false
	for _, c := range b {
		if inStr {
			out = append(out, c)
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
			out = append(out, c)
		case ':':
			out = append(out, ':', ' ')
		case ',':
			out = append(out, ',', ' ')
		default:
			out = append(out, c)
		}
	}
	return out, nil
}

// orderedArg is a name/value pair preserving JSON key order.
type orderedArg struct {
	Name  string
	Value any
}

// parseOrderedArgs parses a JSON object while preserving key order.
func parseOrderedArgs(raw string) ([]orderedArg, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '{' {
		return nil, errors.New("expected JSON object")
	}
	var args []orderedArg
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		args = append(args, orderedArg{Name: key, Value: val})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return args, nil
}

// formatArgValue formats a value for XML-style tool call parameters.
func formatArgValue(v any) string {
	if v == nil {
		return "null"
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	}
	switch v.(type) {
	case map[string]any, []any:
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf("%v", v)
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// suffixPrefixOverlap returns the length of the longest suffix of s
// that is a prefix of delim.
func suffixPrefixOverlap(s, delim string) int {
	maxLen := len(delim)
	if maxLen > len(s) {
		maxLen = len(s)
	}
	for i := maxLen; i > 0; i-- {
		if strings.HasSuffix(s, delim[:i]) {
			return i
		}
	}
	return 0
}

// splitAtTag splits the builder content at the first occurrence of tag.
// Returns (before, after). The builder is reset and loaded with `after`.
func splitAtTag(sb *strings.Builder, tag string, trimAfter bool) (string, string) {
	split := strings.SplitN(sb.String(), tag, 2)
	if len(split) == 1 {
		sb.Reset()
		return split[0], ""
	}
	before := strings.TrimRightFunc(split[0], unicode.IsSpace)
	after := split[1]
	if trimAfter {
		after = strings.TrimLeftFunc(after, unicode.IsSpace)
	}
	sb.Reset()
	sb.WriteString(after)
	return before, after
}

// trailingWSLen returns the number of bytes of trailing whitespace in s.
func trailingWSLen(s string) int {
	remaining := s
	total := 0
	for len(remaining) > 0 {
		r, size := utf8.DecodeLastRuneInString(remaining)
		if r == utf8.RuneError && size == 1 {
			break
		}
		if !unicode.IsSpace(r) {
			break
		}
		total += size
		remaining = remaining[:len(remaining)-size]
	}
	return total
}
