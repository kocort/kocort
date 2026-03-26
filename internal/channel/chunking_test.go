package channel

import (
	"reflect"
	"testing"

	"github.com/kocort/kocort/internal/config"
)

type stubChunker struct{}

func (stubChunker) ChunkText(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	return []string{text[:limit], text[limit:]}
}

func (stubChunker) TextChunkLimit() int { return 5 }

func TestResolveConfiguredChannelChunkLimitPrefersAccountConfig(t *testing.T) {
	cfg := config.ChannelConfig{
		TextChunkLimit: 40,
		Accounts: map[string]any{
			"main": map[string]any{
				"textChunkLimit": 12,
			},
		},
	}
	limit, ok := ResolveConfiguredChannelChunkLimit(cfg, "main")
	if !ok || limit != 12 {
		t.Fatalf("expected account chunk limit 12, got %d ok=%v", limit, ok)
	}
}

func TestChunkOutboundTextForAdapterUsesAdapterChunker(t *testing.T) {
	cfg := config.ChannelConfig{}
	chunks := ChunkOutboundTextForAdapter("helloworld", stubChunker{}, cfg, "")
	want := []string{"hello", "world"}
	if !reflect.DeepEqual(chunks, want) {
		t.Fatalf("expected %v, got %v", want, chunks)
	}
}
