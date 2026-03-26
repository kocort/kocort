package delivery

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestNormalizeReplyPayloadForDelivery_MediaOnlyNotDropped(t *testing.T) {
	// A payload with only a single MediaURL and no text should NOT be dropped.
	payload := core.ReplyPayload{
		MediaURL: "file:///tmp/photo.png",
	}
	normalized, ok := NormalizeReplyPayloadForDelivery(payload)
	if !ok {
		t.Fatal("expected payload with single MediaURL to be kept, but it was dropped")
	}
	if normalized.MediaURL != "file:///tmp/photo.png" {
		t.Fatalf("expected MediaURL to be preserved, got %q", normalized.MediaURL)
	}
}

func TestNormalizeReplyPayloadForDelivery_MultipleMediaURLs(t *testing.T) {
	payload := core.ReplyPayload{
		MediaURLs: []string{"https://example.com/a.png", "https://example.com/b.png"},
	}
	normalized, ok := NormalizeReplyPayloadForDelivery(payload)
	if !ok {
		t.Fatal("expected payload with multiple MediaURLs to be kept, but it was dropped")
	}
	if len(normalized.MediaURLs) != 2 {
		t.Fatalf("expected 2 MediaURLs, got %d", len(normalized.MediaURLs))
	}
}

func TestNormalizeReplyPayloadForDelivery_EmptyDropped(t *testing.T) {
	payload := core.ReplyPayload{}
	_, ok := NormalizeReplyPayloadForDelivery(payload)
	if ok {
		t.Fatal("expected empty payload to be dropped")
	}
}

func TestNormalizeReplyPayloadForDelivery_TextOnly(t *testing.T) {
	payload := core.ReplyPayload{Text: "hello"}
	normalized, ok := NormalizeReplyPayloadForDelivery(payload)
	if !ok {
		t.Fatal("expected text-only payload to be kept")
	}
	if normalized.Text != "hello" {
		t.Fatalf("expected text %q, got %q", "hello", normalized.Text)
	}
}

func TestNormalizeReplyPayloadForDelivery_ReasoningDropped(t *testing.T) {
	payload := core.ReplyPayload{Text: "thinking...", IsReasoning: true}
	_, ok := NormalizeReplyPayloadForDelivery(payload)
	if ok {
		t.Fatal("expected reasoning payload to be dropped")
	}
}

func TestNormalizeReplyPayloadForDelivery_TextWithMedia(t *testing.T) {
	payload := core.ReplyPayload{
		Text:     "Check this image",
		MediaURL: "https://example.com/img.png",
	}
	normalized, ok := NormalizeReplyPayloadForDelivery(payload)
	if !ok {
		t.Fatal("expected text+media payload to be kept")
	}
	if normalized.Text != "Check this image" {
		t.Fatalf("unexpected text: %q", normalized.Text)
	}
	if normalized.MediaURL != "https://example.com/img.png" {
		t.Fatalf("unexpected MediaURL: %q", normalized.MediaURL)
	}
}

func TestNormalizeReplyPayloadForDelivery_ChannelDataOnly(t *testing.T) {
	payload := core.ReplyPayload{
		ChannelData: map[string]any{"key": "value"},
	}
	normalized, ok := NormalizeReplyPayloadForDelivery(payload)
	if !ok {
		t.Fatal("expected channelData-only payload to be kept")
	}
	if normalized.ChannelData["key"] != "value" {
		t.Fatalf("unexpected channelData: %v", normalized.ChannelData)
	}
}

func TestNormalizeReplyPayloadForDelivery_DedupMediaURLs(t *testing.T) {
	payload := core.ReplyPayload{
		MediaURL:  "https://example.com/a.png",
		MediaURLs: []string{"https://example.com/a.png", "https://example.com/b.png"},
	}
	normalized, ok := NormalizeReplyPayloadForDelivery(payload)
	if !ok {
		t.Fatal("expected payload to be kept")
	}
	// a.png is deduplicated, so we should have exactly 2 unique URLs
	total := 0
	if normalized.MediaURL != "" {
		total++
	}
	total += len(normalized.MediaURLs)
	if total != 2 {
		t.Fatalf("expected 2 unique media entries, got %d (MediaURL=%q, MediaURLs=%v)",
			total, normalized.MediaURL, normalized.MediaURLs)
	}
}
