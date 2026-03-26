package llamawrapper

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ── Think block parser ───────────────────────────────────────────────────────

type thinkState int

const (
	thinkSeekOpen thinkState = iota // looking for <think>
	thinkEatOpenWS                  // eating whitespace after <think>
	thinkInside                     // inside <think> block
	thinkEatCloseWS                 // eating whitespace after </think>
	thinkDone                       // past </think>, normal content
)

// thinkParser separates <think>...</think> reasoning from visible content
// in a streaming token sequence.
type thinkParser struct {
	state thinkState
	acc   strings.Builder
}

func newThinkParser() *thinkParser {
	return &thinkParser{}
}

// Add processes a token piece and returns (thinking, content).
func (p *thinkParser) Add(piece string) (thinking, content string) {
	p.acc.WriteString(piece)

	var thinkBuf, contentBuf strings.Builder
	cont := true
	for cont {
		var t, c string
		t, c, cont = p.eat()
		thinkBuf.WriteString(t)
		contentBuf.WriteString(c)
	}
	return thinkBuf.String(), contentBuf.String()
}

func (p *thinkParser) eat() (thinking, content string, cont bool) {
	switch p.state {
	case thinkSeekOpen:
		trimmed := strings.TrimLeftFunc(p.acc.String(), unicode.IsSpace)
		if strings.HasPrefix(trimmed, thinkOpen) {
			after := strings.TrimLeftFunc(trimmed[len(thinkOpen):], unicode.IsSpace)
			p.acc.Reset()
			p.acc.WriteString(after)
			if after == "" {
				p.state = thinkEatOpenWS
			} else {
				p.state = thinkInside
			}
			return "", "", true
		}
		if strings.HasPrefix(thinkOpen, trimmed) || trimmed == "" {
			return "", "", false
		}
		p.state = thinkDone
		untrimmed := p.acc.String()
		p.acc.Reset()
		return "", untrimmed, false

	case thinkEatOpenWS:
		trimmed := strings.TrimLeftFunc(p.acc.String(), unicode.IsSpace)
		p.acc.Reset()
		if trimmed == "" {
			return "", "", false
		}
		p.state = thinkInside
		p.acc.WriteString(trimmed)
		return "", "", true

	case thinkInside:
		acc := p.acc.String()
		if strings.Contains(acc, thinkClose) {
			split := strings.SplitN(acc, thinkClose, 2)
			thinking = split[0]
			rem := strings.TrimLeftFunc(split[1], unicode.IsSpace)
			p.acc.Reset()
			if rem == "" {
				p.state = thinkEatCloseWS
			} else {
				p.state = thinkDone
			}
			return thinking, rem, false
		}
		if ol := suffixPrefixOverlap(acc, thinkClose); ol > 0 {
			thinking = acc[:len(acc)-ol]
			tail := acc[len(acc)-ol:]
			p.acc.Reset()
			p.acc.WriteString(tail)
			return thinking, "", false
		}
		p.acc.Reset()
		return acc, "", false

	case thinkEatCloseWS:
		trimmed := strings.TrimLeftFunc(p.acc.String(), unicode.IsSpace)
		p.acc.Reset()
		if trimmed != "" {
			p.state = thinkDone
		}
		return "", trimmed, false

	case thinkDone:
		acc := p.acc.String()
		p.acc.Reset()
		return "", acc, false
	}
	return "", "", false
}

// suffixPrefixOverlap returns the length of the longest suffix of s that is a prefix of delim.
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
