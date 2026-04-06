package localmodel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/localmodel/llamawrapper"
)

type downloadTestBackend struct{}

func (b *downloadTestBackend) Start(string, string, int, int, int, SamplingParams, bool) error { return nil }

func (b *downloadTestBackend) Stop() error { return nil }

func (b *downloadTestBackend) IsStub() bool { return false }

func (b *downloadTestBackend) HasVision() bool { return false }

func (b *downloadTestBackend) ContextSize() int { return 0 }

func (b *downloadTestBackend) SetSamplingParams(SamplingParams) {}

func (b *downloadTestBackend) CreateChatCompletionStream(context.Context, llamawrapper.ChatCompletionRequest, bool) (<-chan llamawrapper.ChatCompletionChunk, error) {
	ch := make(chan llamawrapper.ChatCompletionChunk)
	close(ch)
	return ch, nil
}

func TestManagerCancelDownload(t *testing.T) {
	modelsDir := t.TempDir()
	chunk := []byte(strings.Repeat("a", 1024))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)*1024))
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 1024; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer server.Close()

	mgr := NewManagerWithBackend(Config{ModelsDir: modelsDir}, &downloadTestBackend{}, []ModelPreset{{
		ID:          "demo",
		Name:        "Demo",
		Filename:    "demo.gguf",
		DownloadURL: server.URL,
	}})

	if err := mgr.DownloadModel("demo", nil); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := mgr.Snapshot()
		if snap.DownloadProgress != nil && snap.DownloadProgress.Active && snap.DownloadProgress.DownloadedBytes > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for download progress")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := mgr.CancelDownload(); err != nil {
		t.Fatalf("CancelDownload: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		snap := mgr.Snapshot()
		if snap.DownloadProgress != nil && !snap.DownloadProgress.Active {
			if !snap.DownloadProgress.Canceled {
				t.Fatal("expected download to be marked canceled")
			}
			if got := snap.DownloadProgress.Error; got != "" {
				t.Fatalf("expected empty error for canceled download, got %q", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for download cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := os.Stat(filepath.Join(modelsDir, "demo.gguf")); !os.IsNotExist(err) {
		t.Fatalf("expected final model file to be absent after cancel, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "demo.gguf.tmp")); !os.IsNotExist(err) {
		t.Fatalf("expected temp model file to be cleaned up after cancel, got err=%v", err)
	}
	if err := mgr.CancelDownload(); err == nil {
		t.Fatal("expected second cancel to fail when no download is active")
	}
}

func TestManagerStartRequiresExplicitSelectedModel(t *testing.T) {
	modelsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modelsDir, "demo.gguf"), []byte("fake-model"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	mgr := NewManagerWithBackend(Config{ModelsDir: modelsDir}, &downloadTestBackend{}, nil)
	if err := mgr.Start(); err == nil {
		t.Fatal("expected start without selected model to fail")
	}
	if got := mgr.ModelID(); got != "" {
		t.Fatalf("expected selected model to remain empty, got %q", got)
	}
}

func TestManagerDeleteSelectedModelClearsSelection(t *testing.T) {
	modelsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modelsDir, "demo.gguf"), []byte("fake-model"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	mgr := NewManagerWithBackend(Config{ModelsDir: modelsDir, ModelID: "demo"}, &downloadTestBackend{}, nil)
	if got := mgr.ModelID(); got != "demo" {
		t.Fatalf("expected selected model demo, got %q", got)
	}
	if err := mgr.DeleteModel("demo"); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if got := mgr.ModelID(); got != "" {
		t.Fatalf("expected selected model to be cleared, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "demo.gguf")); !os.IsNotExist(err) {
		t.Fatalf("expected model file removed, got err=%v", err)
	}
}

func TestManagerSnapshotAutoRefreshesModels(t *testing.T) {
	modelsDir := t.TempDir()
	mgr := NewManagerWithBackend(Config{ModelsDir: modelsDir}, &downloadTestBackend{}, nil)

	if got := len(mgr.Snapshot().Models); got != 0 {
		t.Fatalf("expected no models initially, got %d", got)
	}

	if err := os.WriteFile(filepath.Join(modelsDir, "fresh.gguf"), []byte("fake-model"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}

	snap := mgr.Snapshot()
	if len(snap.Models) != 1 {
		t.Fatalf("expected snapshot to auto-refresh to 1 model, got %d", len(snap.Models))
	}
	if snap.Models[0].ID != "fresh" {
		t.Fatalf("expected refreshed model ID %q, got %q", "fresh", snap.Models[0].ID)
	}

	if err := os.Remove(filepath.Join(modelsDir, "fresh.gguf")); err != nil {
		t.Fatalf("remove model: %v", err)
	}

	if got := len(mgr.Snapshot().Models); got != 0 {
		t.Fatalf("expected snapshot to auto-refresh after deletion, got %d models", got)
	}
}

func TestManagerSelectModelAutoRefreshesModels(t *testing.T) {
	modelsDir := t.TempDir()
	mgr := NewManagerWithBackend(Config{ModelsDir: modelsDir}, &downloadTestBackend{}, nil)

	if err := os.WriteFile(filepath.Join(modelsDir, "later.gguf"), []byte("fake-model"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}

	if err := mgr.SelectModel("later"); err != nil {
		t.Fatalf("expected SelectModel to find newly added model after refresh: %v", err)
	}
	if got := mgr.ModelID(); got != "later" {
		t.Fatalf("expected selected model %q, got %q", "later", got)
	}
}

func TestManagerDownloadSplitModel(t *testing.T) {
	modelsDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/part1.gguf":
			_, _ = w.Write([]byte("part-1"))
		case "/part2.gguf":
			_, _ = w.Write([]byte("part-2-data"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	mgr := NewManagerWithBackend(Config{ModelsDir: modelsDir}, &downloadTestBackend{}, []ModelPreset{ {
		ID:   "split-demo",
		Name: "Split Demo",
		Size: "~17 B",
		Files: []ModelPresetFile{
			{Filename: "split-demo-00001-of-00002.gguf", DownloadURL: server.URL + "/part1.gguf"},
			{Filename: "split-demo-00002-of-00002.gguf", DownloadURL: server.URL + "/part2.gguf"},
		},
	}})

	if err := mgr.DownloadModel("split-demo", &http.Client{}); err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}

	for _, name := range []string{"split-demo-00001-of-00002.gguf", "split-demo-00002-of-00002.gguf"} {
		if _, err := os.Stat(filepath.Join(modelsDir, name)); err != nil {
			t.Fatalf("expected downloaded shard %q: %v", name, err)
		}
	}

	models := mgr.Models()
	if len(models) != 1 {
		t.Fatalf("expected 1 grouped model, got %d", len(models))
	}
	if models[0].ID != "split-demo" {
		t.Fatalf("expected grouped model ID %q, got %q", "split-demo", models[0].ID)
	}
}

func TestManagerDeleteSplitModelRemovesAllShards(t *testing.T) {
	modelsDir := t.TempDir()
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("mega-model-0000%d-of-00003.gguf", i)
		if err := os.WriteFile(filepath.Join(modelsDir, name), []byte(strings.Repeat("x", i)), 0o644); err != nil {
			t.Fatalf("write shard %q: %v", name, err)
		}
	}

	mgr := NewManagerWithBackend(Config{ModelsDir: modelsDir, ModelID: "mega-model"}, &downloadTestBackend{}, nil)
	if err := mgr.DeleteModel("mega-model"); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if got := mgr.ModelID(); got != "" {
		t.Fatalf("expected selected model to be cleared, got %q", got)
	}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("mega-model-0000%d-of-00003.gguf", i)
		if _, err := os.Stat(filepath.Join(modelsDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected shard %q removed, got err=%v", name, err)
		}
	}
}

func TestScanModelsGroupsSplitShards(t *testing.T) {
	modelsDir := t.TempDir()
	files := map[string]string{
		"alpha-00001-of-00002.gguf": "abc",
		"alpha-00002-of-00002.gguf": "defg",
		"beta.gguf":                "xyz",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(modelsDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}

	models := scanModels(modelsDir)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "alpha" {
		t.Fatalf("expected first model ID %q, got %q", "alpha", models[0].ID)
	}
	if models[1].ID != "beta" {
		t.Fatalf("expected second model ID %q, got %q", "beta", models[1].ID)
	}
	if models[0].Size != FormatBytes(int64(len("abc")+len("defg"))) {
		t.Fatalf("expected grouped size %q, got %q", FormatBytes(int64(len("abc")+len("defg"))), models[0].Size)
	}
}
