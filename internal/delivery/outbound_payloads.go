package delivery

import (
	"regexp"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

var plainTextSurfaces = map[string]struct{}{
	"whatsapp":   {},
	"signal":     {},
	"sms":        {},
	"irc":        {},
	"telegram":   {},
	"imessage":   {},
	"googlechat": {},
}

// NormalizeReplyPayloadForDelivery normalises a reply payload for delivery,
// merging media URLs and stripping reasoning content.
func NormalizeReplyPayloadForDelivery(payload core.ReplyPayload) (core.ReplyPayload, bool) {
	if payload.IsReasoning {
		return core.ReplyPayload{}, false
	}
	mediaURLs := mergeNormalizedMediaURLs(
		payload.MediaURLs,
		func() []string {
			if strings.TrimSpace(payload.MediaURL) == "" {
				return nil
			}
			return []string{payload.MediaURL}
		}(),
	)
	next := payload
	next.Text = strings.TrimSpace(next.Text)
	next.MediaURLs = mediaURLs
	if len(mediaURLs) > 1 {
		next.MediaURL = ""
	} else if len(mediaURLs) == 1 {
		next.MediaURL = mediaURLs[0]
		next.MediaURLs = nil // clear the slice so normalizedMediaURLs never counts this URL twice
	} else {
		next.MediaURL = ""
	}
	hasChannelData := next.ChannelData != nil && len(next.ChannelData) > 0
	if next.Text == "" && next.MediaURL == "" && len(next.MediaURLs) == 0 && !hasChannelData {
		return core.ReplyPayload{}, false
	}
	return next, true
}

func mergeNormalizedMediaURLs(lists ...[]string) []string {
	seen := map[string]struct{}{}
	merged := make([]string, 0)
	for _, list := range lists {
		for _, entry := range list {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			merged = append(merged, trimmed)
		}
	}
	return merged
}

// IsPlainTextSurface reports whether the given (already-normalised) channel ID
// is a plain-text surface that requires HTML stripping.
func IsPlainTextSurface(normalizedChannelID string) bool {
	_, ok := plainTextSurfaces[strings.TrimSpace(strings.ToLower(normalizedChannelID))]
	return ok
}

var (
	plainTextAutolinkPattern     = regexp.MustCompile(`(?i)<((?:https?:\/\/|mailto:)[^<>\s]+)>`)
	plainTextBreakPattern        = regexp.MustCompile(`(?i)<br\s*\/?>`)
	plainTextBlockPattern        = regexp.MustCompile(`(?i)</?(p|div)>`)
	plainTextBoldBPattern        = regexp.MustCompile(`(?is)<b>(.*?)</b>`)
	plainTextBoldStrongPattern   = regexp.MustCompile(`(?is)<strong>(.*?)</strong>`)
	plainTextItalicIPattern      = regexp.MustCompile(`(?is)<i>(.*?)</i>`)
	plainTextItalicEmPattern     = regexp.MustCompile(`(?is)<em>(.*?)</em>`)
	plainTextStrikeSPattern      = regexp.MustCompile(`(?is)<s>(.*?)</s>`)
	plainTextStrikeStrikePattern = regexp.MustCompile(`(?is)<strike>(.*?)</strike>`)
	plainTextStrikeDelPattern    = regexp.MustCompile(`(?is)<del>(.*?)</del>`)
	plainTextCodePattern         = regexp.MustCompile(`(?is)<code>(.*?)</code>`)
	plainTextHeadingPattern      = regexp.MustCompile(`(?is)<h[1-6][^>]*>(.*?)</h[1-6]>`)
	plainTextListItemPattern     = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	plainTextTagPattern          = regexp.MustCompile(`(?i)</?[a-z][a-z0-9]*\b[^>]*>`)
	plainTextNewlinePattern      = regexp.MustCompile(`\n{3,}`)
)

// SanitizeForPlainText strips HTML tags and converts formatting to plain-text equivalents.
func SanitizeForPlainText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	sanitized := text
	sanitized = plainTextAutolinkPattern.ReplaceAllString(sanitized, `$1`)
	sanitized = plainTextBreakPattern.ReplaceAllString(sanitized, "\n")
	sanitized = plainTextBlockPattern.ReplaceAllString(sanitized, "\n")
	sanitized = plainTextBoldBPattern.ReplaceAllString(sanitized, `*${1}*`)
	sanitized = plainTextBoldStrongPattern.ReplaceAllString(sanitized, `*${1}*`)
	sanitized = plainTextItalicIPattern.ReplaceAllString(sanitized, `_${1}_`)
	sanitized = plainTextItalicEmPattern.ReplaceAllString(sanitized, `_${1}_`)
	sanitized = plainTextStrikeSPattern.ReplaceAllString(sanitized, `~${1}~`)
	sanitized = plainTextStrikeStrikePattern.ReplaceAllString(sanitized, `~${1}~`)
	sanitized = plainTextStrikeDelPattern.ReplaceAllString(sanitized, `~${1}~`)
	sanitized = plainTextCodePattern.ReplaceAllString(sanitized, "`${1}`")
	sanitized = plainTextHeadingPattern.ReplaceAllString(sanitized, "\n*${1}*\n")
	sanitized = plainTextListItemPattern.ReplaceAllString(sanitized, "• ${1}\n")
	sanitized = plainTextTagPattern.ReplaceAllString(sanitized, "")
	sanitized = plainTextNewlinePattern.ReplaceAllString(sanitized, "\n\n")
	return sanitized
}
