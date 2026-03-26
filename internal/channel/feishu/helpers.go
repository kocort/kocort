// Pure Feishu/Lark helper functions.
//
// Text formatting, markdown detection, message parsing, media helpers,
// target normalization, and generic map accessors.
// This file depends ONLY on the adapter package — no other project internals.
package feishu

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/kocort/kocort/internal/channel/adapter"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	FeishuDefaultDomain     = "https://open.feishu.cn"
	FeishuDefaultLarkDomain = "https://open.larksuite.com"
	FeishuAckEmojiDefault   = "OK"
)

// ---------------------------------------------------------------------------
// Compiled regexps for markdown detection
// ---------------------------------------------------------------------------

var (
	FeishuMarkdownHeaderPattern        = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	FeishuMarkdownBulletPattern        = regexp.MustCompile(`^\s*(?:[-*+]\s+|\d+\.\s+)`)
	FeishuMarkdownLinkPattern          = regexp.MustCompile(`\[(?P<text>[^\]]+)\]\((?P<href>https?://[^)\s]+)\)`)
	FeishuMarkdownBoldPattern          = regexp.MustCompile(`\*\*[^*\n]+\*\*`)
	FeishuMarkdownInlineCodePattern    = regexp.MustCompile("`[^`\n]+`")
	FeishuMarkdownStrikethroughPattern = regexp.MustCompile(`~~[^~\n]+~~`)
)

// ---------------------------------------------------------------------------
// Markdown detection and formatting
// ---------------------------------------------------------------------------

// LooksLikeFeishuMarkdown returns true if the text contains markdown-like
// formatting that should trigger rich-text (post) rendering.
func LooksLikeFeishuMarkdown(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	switch {
	case strings.Contains(trimmed, "```"):
		return true
	case FeishuMarkdownHeaderPattern.MatchString(trimmed):
		return true
	case FeishuMarkdownBulletPattern.MatchString(trimmed):
		return true
	case FeishuMarkdownLinkPattern.MatchString(trimmed):
		return true
	case strings.Contains(trimmed, "\n# "):
		return true
	case strings.Contains(trimmed, "\n- "):
		return true
	case strings.Contains(trimmed, "\n1. "):
		return true
	case FeishuMarkdownBoldPattern.MatchString(trimmed):
		return true
	case FeishuMarkdownInlineCodePattern.MatchString(trimmed):
		return true
	case FeishuMarkdownStrikethroughPattern.MatchString(trimmed):
		return true
	default:
		return false
	}
}

// BuildFeishuMarkdownPost wraps Markdown text in a Feishu "post" message
// using the native "md" tag. The md tag is rendered server-side by Feishu,
// which means the original Markdown syntax is preserved and all formatting
// (bold, italic, strikethrough, code blocks, lists, links, etc.) is handled
// natively without any client-side parsing.
//
// Constraints (from Feishu docs):
//   - The md tag must occupy its own paragraph.
//   - md tags are send-only; when read back via the API they are converted
//     to equivalent non-md tags.
func BuildFeishuMarkdownPost(text string) (string, error) {
	post := larkim.NewMessagePost()
	content := larkim.NewMessagePostContent()
	content.AppendContent([]larkim.MessagePostElement{&mdTagElement{text: strings.TrimSpace(text)}})
	post.ZhCn(content.Build())
	return post.Build()
}

// mdTagElement implements MessagePostElement using Feishu's native "md" tag.
// The md tag accepts raw Markdown text and renders it server-side.
type mdTagElement struct {
	text string
}

func (e *mdTagElement) Tag() string { return "md" }
func (e *mdTagElement) IsPost()     {}
func (e *mdTagElement) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"tag": "md", "text": e.text})
}

// BuildFeishuPostLineElements parses a single line for markdown links and
// returns a slice of post elements (text and hyperlinks).
func BuildFeishuPostLineElements(line string) []larkim.MessagePostElement {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	matches := FeishuMarkdownLinkPattern.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return []larkim.MessagePostElement{&larkim.MessagePostText{Text: line}}
	}
	out := make([]larkim.MessagePostElement, 0, len(matches)*2+1)
	last := 0
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		if match[0] > last {
			out = append(out, &larkim.MessagePostText{Text: line[last:match[0]]})
		}
		linkText := line[match[2]:match[3]]
		linkHref := line[match[4]:match[5]]
		out = append(out, &larkim.MessagePostA{Text: linkText, Href: linkHref})
		last = match[1]
	}
	if last < len(line) {
		out = append(out, &larkim.MessagePostText{Text: line[last:]})
	}
	return out
}

// ---------------------------------------------------------------------------
// Inbound text parsing
// ---------------------------------------------------------------------------

// ResolveFeishuInboundText extracts the user-visible text from a raw Feishu
// message content string based on message type.
func ResolveFeishuInboundText(messageType, rawContent string) string {
	switch messageType {
	case "text":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(rawContent), &payload); err == nil {
			return strings.TrimSpace(payload.Text)
		}
	case "post":
		if text := ExtractFeishuPostText(rawContent); text != "" {
			return text
		}
		return FeishuPlaceholderText(messageType)
	case "image", "file", "audio", "media", "sticker", "share_chat", "share_calendar_event":
		return FeishuPlaceholderText(messageType)
	}
	return strings.TrimSpace(rawContent)
}

// FeishuPlaceholderText returns a placeholder string for a non-text message
// type.
func FeishuPlaceholderText(messageType string) string {
	switch messageType {
	case "image":
		return "[image]"
	case "file":
		return "[file]"
	case "audio":
		return "[audio]"
	case "media":
		return "[media]"
	case "post":
		return "[rich text message]"
	default:
		if strings.TrimSpace(messageType) == "" {
			return ""
		}
		return "[" + strings.TrimSpace(messageType) + "]"
	}
}

// ExtractFeishuPostText extracts concatenated text from a Feishu "post"
// message content JSON string.
func ExtractFeishuPostText(rawContent string) string {
	payload := resolveFeishuPostPayloadFromString(rawContent)
	if payload == nil {
		return ""
	}
	lines := make([]string, 0, len(payload.Content)+1)
	if title := strings.TrimSpace(payload.Title); title != "" {
		lines = append(lines, title)
	}
	for _, row := range payload.Content {
		items, ok := row.([]interface{})
		if !ok {
			continue
		}
		parts := make([]string, 0, len(items))
		for _, item := range items {
			elem, ok := item.(map[string]any)
			if !ok {
				continue
			}
			tag := strings.ToLower(strings.TrimSpace(StringFromMap(elem, "tag")))
			switch tag {
			case "text", "a", "emotion":
				if text := strings.TrimSpace(StringFromMap(elem, "text")); text != "" {
					parts = append(parts, text)
				}
			case "at":
				if text := strings.TrimSpace(FirstNonEmptyTrimmed(StringFromMap(elem, "user_name"), StringFromMap(elem, "open_id"), StringFromMap(elem, "user_id"))); text != "" {
					parts = append(parts, "@"+text)
				}
			case "img":
				parts = append(parts, "[image]")
			case "media":
				parts = append(parts, "[media]")
			case "code", "code_block", "pre":
				if text := strings.TrimSpace(FirstNonEmptyTrimmed(StringFromMap(elem, "text"), StringFromMap(elem, "content"))); text != "" {
					parts = append(parts, text)
				}
			}
		}
		line := strings.TrimSpace(strings.Join(parts, " "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// FeishuParsedMedia holds the parsed media keys from a Feishu message.
type FeishuParsedMedia struct {
	ImageKey string
	FileKey  string
	FileName string
}

// ParseFeishuMessageMedia parses media keys from a raw Feishu message
// content JSON string.
func ParseFeishuMessageMedia(rawContent, messageType string) FeishuParsedMedia {
	var payload map[string]any
	if err := json.Unmarshal([]byte(rawContent), &payload); err != nil {
		return FeishuParsedMedia{}
	}
	imageKey := strings.TrimSpace(StringFromMap(payload, "image_key"))
	fileKey := strings.TrimSpace(StringFromMap(payload, "file_key"))
	fileName := strings.TrimSpace(StringFromMap(payload, "file_name"))
	switch strings.ToLower(strings.TrimSpace(messageType)) {
	case "image":
		return FeishuParsedMedia{ImageKey: imageKey}
	case "file":
		return FeishuParsedMedia{FileKey: fileKey, FileName: fileName}
	case "audio", "sticker":
		return FeishuParsedMedia{FileKey: fileKey}
	case "video", "media":
		return FeishuParsedMedia{FileKey: fileKey, ImageKey: imageKey, FileName: fileName}
	default:
		return FeishuParsedMedia{ImageKey: imageKey, FileKey: fileKey, FileName: fileName}
	}
}

// FeishuPostMediaEntry describes an image or media file found in a post
// element.
type FeishuPostMediaEntry struct {
	FileKey      string
	FileName     string
	ResourceType string
}

// ParseFeishuPostMedia extracts media keys from a Feishu "post" message.
func ParseFeishuPostMedia(rawContent string) []FeishuPostMediaEntry {
	var parsed any
	if err := json.Unmarshal([]byte(rawContent), &parsed); err != nil {
		return nil
	}
	payload := resolveFeishuPostPayload(parsed)
	if payload == nil {
		return nil
	}
	out := make([]FeishuPostMediaEntry, 0)
	for _, row := range payload.Content {
		items, ok := row.([]interface{})
		if !ok {
			continue
		}
		for _, item := range items {
			elem, ok := item.(map[string]any)
			if !ok {
				continue
			}
			tag := strings.ToLower(strings.TrimSpace(StringFromMap(elem, "tag")))
			switch tag {
			case "img":
				if imageKey := strings.TrimSpace(StringFromMap(elem, "image_key")); imageKey != "" {
					out = append(out, FeishuPostMediaEntry{FileKey: imageKey, ResourceType: "image"})
				}
			case "media":
				if fileKey := strings.TrimSpace(StringFromMap(elem, "file_key")); fileKey != "" {
					out = append(out, FeishuPostMediaEntry{FileKey: fileKey, FileName: strings.TrimSpace(StringFromMap(elem, "file_name")), ResourceType: "file"})
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Post payload resolution
// ---------------------------------------------------------------------------

func resolveFeishuPostPayloadFromString(rawContent string) *feishuPostPayload {
	var parsed any
	if err := json.Unmarshal([]byte(rawContent), &parsed); err != nil {
		return nil
	}
	return resolveFeishuPostPayload(parsed)
}

type feishuPostPayload struct {
	Title   string
	Content []interface{}
}

func resolveFeishuPostPayload(value any) *feishuPostPayload {
	if payload := toFeishuPostPayload(value); payload != nil {
		return payload
	}
	record, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, nested := range record {
		if payload := toFeishuPostPayload(nested); payload != nil {
			return payload
		}
	}
	return nil
}

func toFeishuPostPayload(value any) *feishuPostPayload {
	record, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	content, ok := record["content"].([]interface{})
	if !ok {
		return nil
	}
	return &feishuPostPayload{
		Title:   strings.TrimSpace(StringFromMap(record, "title")),
		Content: content,
	}
}

// ---------------------------------------------------------------------------
// Target normalization
// ---------------------------------------------------------------------------

// NormalizeFeishuTarget parses a raw target string (e.g. "chat:oc_xxx" or
// "user:ou_xxx") into a receive-ID type and target ID.
func NormalizeFeishuTarget(raw string) (string, string) {
	target := strings.TrimSpace(raw)
	target = strings.TrimPrefix(target, "feishu:")
	target = strings.TrimPrefix(target, "lark:")
	switch {
	case strings.HasPrefix(target, "chat:"):
		return larkim.ReceiveIdTypeChatId, strings.TrimSpace(strings.TrimPrefix(target, "chat:"))
	case strings.HasPrefix(target, "user:"):
		return larkim.ReceiveIdTypeOpenId, strings.TrimSpace(strings.TrimPrefix(target, "user:"))
	case strings.HasPrefix(target, "oc_"):
		return larkim.ReceiveIdTypeChatId, target
	default:
		return larkim.ReceiveIdTypeOpenId, target
	}
}

// NormalizeFeishuDomain resolves a domain alias ("feishu", "lark") or URL
// into a canonical base URL.
func NormalizeFeishuDomain(raw string) string {
	value := strings.TrimSpace(raw)
	switch strings.ToLower(value) {
	case "", "feishu":
		return FeishuDefaultDomain
	case "lark":
		return FeishuDefaultLarkDomain
	default:
		return strings.TrimRight(value, "/")
	}
}

// ResolveFeishuReplyTarget determines the reply-to target and chat type
// from the raw chat type, sender, and chat ID.
func ResolveFeishuReplyTarget(rawChatType, from, chatID string) (string, adapter.ChatType) {
	switch strings.TrimSpace(strings.ToLower(rawChatType)) {
	case "group":
		return chatID, adapter.ChatTypeGroup
	case "topic_group":
		return chatID, adapter.ChatTypeTopic
	default:
		return from, adapter.ChatTypeDirect
	}
}

// ResolveFeishuAckEmojiType returns the emoji type for ack reactions,
// reading from the channel config.
func ResolveFeishuAckEmojiType(channelConfig map[string]any) string {
	if value := strings.TrimSpace(StringFromMap(channelConfig, "ackReactionEmojiType")); value != "" {
		if strings.EqualFold(value, "none") || strings.EqualFold(value, "off") {
			return ""
		}
		return value
	}
	return FeishuAckEmojiDefault
}

// FeishuMediaMaxBytesForChannel returns the configured max media size.
func FeishuMediaMaxBytesForChannel(channelConfig map[string]any, defaultMax int) int {
	maxMB := IntFromMap(channelConfig, "mediaMaxMb")
	if maxMB <= 0 {
		return defaultMax
	}
	return maxMB << 20
}

// ---------------------------------------------------------------------------
// File/media helpers
// ---------------------------------------------------------------------------

// InferFeishuFileType maps a file name and MIME type to a Feishu file-type
// constant.
func InferFeishuFileType(fileName, mimeType string) string {
	ext := strings.TrimSpace(strings.ToLower(filepath.Ext(fileName)))
	switch ext {
	case ".mp4":
		return larkim.FileTypeMp4
	case ".pdf":
		return larkim.FileTypePdf
	case ".doc", ".docx":
		return larkim.FileTypeDoc
	case ".xls", ".xlsx", ".csv":
		return larkim.FileTypeXls
	case ".ppt", ".pptx":
		return larkim.FileTypePpt
	case ".opus":
		return larkim.FileTypeOpus
	}
	if strings.Contains(mimeType, "pdf") {
		return larkim.FileTypePdf
	}
	if strings.Contains(mimeType, "word") {
		return larkim.FileTypeDoc
	}
	if strings.Contains(mimeType, "sheet") || strings.Contains(mimeType, "excel") || strings.Contains(mimeType, "csv") {
		return larkim.FileTypeXls
	}
	if strings.Contains(mimeType, "presentation") || strings.Contains(mimeType, "powerpoint") {
		return larkim.FileTypePpt
	}
	if strings.Contains(mimeType, "video/mp4") {
		return larkim.FileTypeMp4
	}
	return larkim.FileTypeStream
}

// DetectMediaMimeType detects the MIME type of a file from its path and
// content.
func DetectMediaMimeType(path string, content []byte) string {
	if ext := strings.TrimSpace(strings.ToLower(filepath.Ext(path))); ext != "" {
		if guessed := strings.TrimSpace(strings.Split(mime.TypeByExtension(ext), ";")[0]); guessed != "" {
			return guessed
		}
	}
	return strings.TrimSpace(strings.Split(http.DetectContentType(content), ";")[0])
}

// ExtensionForMimeType returns a file extension for a MIME type, or ""
// if none is known.
func ExtensionForMimeType(mimeType string) string {
	if mimeType == "" {
		return ""
	}
	if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 { // zero value fallback is intentional
		return exts[0]
	}
	return ""
}

// DecodeFeishuDataURL decodes a data: URL into raw bytes, MIME type, and
// suggested file name.
func DecodeFeishuDataURL(value string) ([]byte, string, string, error) {
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return nil, "", "", fmt.Errorf("not a data url")
	}
	idx := strings.Index(value, ",")
	if idx <= 0 {
		return nil, "", "", fmt.Errorf("invalid data url")
	}
	meta := value[5:idx]
	body := value[idx+1:]
	parts := strings.Split(meta, ";")
	mimeType := strings.TrimSpace(parts[0])
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if len(parts) < 2 || !strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), "base64") {
		return nil, "", "", fmt.Errorf("data url must be base64")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil {
		return nil, "", "", err
	}
	fileName := "attachment" + ExtensionForMimeType(mimeType)
	return decoded, mimeType, fileName, nil
}

// ---------------------------------------------------------------------------
// Generic map accessors
// ---------------------------------------------------------------------------

// StringFromMap reads a string value from a map by key.
func StringFromMap(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", typed)
	}
}

// BoolFromMap reads a boolean value from a map by key, returning (value, ok).
func BoolFromMap(raw map[string]any, key string) (bool, bool) {
	if raw == nil {
		return false, false
	}
	value, ok := raw[key]
	if !ok || value == nil {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		}
	}
	return false, false
}

// IntFromMap reads an integer value from a map by key.
func IntFromMap(raw map[string]any, key string) int {
	if raw == nil {
		return 0
	}
	value, ok := raw[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		number, _ := typed.Int64() // zero fallback is acceptable
		return int(number)
	case string:
		number, _ := strconv.Atoi(strings.TrimSpace(typed)) // zero fallback is acceptable
		return number
	default:
		return 0
	}
}

// FirstNonEmptyTrimmed returns the first non-empty (after trimming) string
// from the given values.
func FirstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// LarkString dereferences a *string safely, returning "" for nil.
func LarkString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
