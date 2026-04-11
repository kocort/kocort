package manager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kocort/kocort/internal/localmodel/catalog"
)

func TestResolveMMProjPath_RealDir(t *testing.T) {
	modelsDir := "E:\\workspace\\kocort\\cmd\\kocort\\.kocort\\models"
	if _, err := os.Stat(modelsDir); err != nil {
		t.Skip("models dir not available")
	}

	modelID := "gemma-4-e2b-it-q4_k_m"

	// Test 1: installedMMProjFiles (old approach — expected to fail)
	old := installedMMProjFiles(modelsDir, modelID)
	t.Logf("installedMMProjFiles result: %v", old)

	// Test 2: resolveMMProjPath with catalog
	presets := catalog.BuiltinCatalogPresets()
	result := resolveMMProjPath(modelsDir, modelID, presets)
	t.Logf("resolveMMProjPath result: %q", result)

	if result == "" {
		t.Fatal("resolveMMProjPath returned empty string, expected mmproj path")
	}
	if _, err := os.Stat(result); err != nil {
		t.Fatalf("resolved path does not exist: %v", err)
	}
}

func TestScanModelsDetectsVision_RealDir(t *testing.T) {
	modelsDir := "E:\\workspace\\kocort\\cmd\\kocort\\.kocort\\models"
	if _, err := os.Stat(modelsDir); err != nil {
		t.Skip("models dir not available")
	}

	// List actual files for context.
	entries, _ := os.ReadDir(modelsDir)
	for _, e := range entries {
		t.Logf("  file: %s", e.Name())
	}

	models := scanModels(modelsDir)
	t.Logf("scanModels found %d models", len(models))
	for _, m := range models {
		t.Logf("  model id=%q name=%q vision=%v caps=%+v", m.ID, m.Name, m.Capabilities.Vision, m.Capabilities)
	}

	// Find the gemma-4 model
	found := false
	for _, m := range models {
		if m.ID == "gemma-4-e2b-it-q4_k_m" {
			found = true
			if !m.Capabilities.Vision {
				t.Errorf("gemma-4 model should have vision=true but got false")
			}
			break
		}
	}
	if !found {
		t.Fatal("gemma-4-e2b-it-q4_k_m not found in scanModels result")
	}
}

func TestCompanionModelIDMismatch(t *testing.T) {
	// This demonstrates the core issue: mmproj with different quant suffix
	// produces a different model ID than the main model.
	mainFile := "gemma-4-E2B-it-Q4_K_M.gguf"
	mmprojFile := "mmproj-gemma-4-E2B-it-BF16.gguf"

	mainID := catalog.ModelIDFromFilename(mainFile)
	mmprojID := catalog.CompanionModelIDFromFilename(mmprojFile)

	t.Logf("main model ID:   %q", mainID)
	t.Logf("mmproj model ID: %q", mmprojID)

	if mainID == mmprojID {
		t.Log("IDs match (good)")
	} else {
		t.Logf("IDs DO NOT match: %q != %q — this is the root cause", mainID, mmprojID)
	}

	// Verify that with catalog filename they would match
	catalogMmprojFile := "mmproj-gemma-4-E2B-it-Q4_K_M.gguf"
	catalogID := catalog.CompanionModelIDFromFilename(catalogMmprojFile)
	t.Logf("catalog mmproj ID: %q", catalogID)
	if mainID != catalogID {
		t.Errorf("even catalog filename doesn't match: %q != %q", mainID, catalogID)
	}
}

func TestResolveMMProjPath_URLFallback(t *testing.T) {
	// Simulate the real scenario with a temp dir
	modelsDir := t.TempDir()

	// Create files with the URL-original names (as on real disk)
	os.WriteFile(filepath.Join(modelsDir, "gemma-4-E2B-it-Q4_K_M.gguf"), []byte("main"), 0o644)
	os.WriteFile(filepath.Join(modelsDir, "mmproj-gemma-4-E2B-it-BF16.gguf"), []byte("mmproj"), 0o644)

	presets := catalog.BuiltinCatalogPresets()
	result := resolveMMProjPath(modelsDir, "gemma-4-e2b-it-q4_k_m", presets)

	if result == "" {
		t.Fatal("expected to find mmproj via URL fallback, got empty")
	}
	expected := filepath.Join(modelsDir, "mmproj-gemma-4-E2B-it-BF16.gguf")
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestManagerStartPassesMmprojPath(t *testing.T) {
	// Simulate the real scenario: mmproj has URL-original name, modelID has mixed case.
	modelsDir := t.TempDir()
	os.WriteFile(filepath.Join(modelsDir, "gemma-4-E2B-it-Q4_K_M.gguf"), []byte("main"), 0o644)
	os.WriteFile(filepath.Join(modelsDir, "mmproj-gemma-4-E2B-it-BF16.gguf"), []byte("mmproj"), 0o644)

	backend := &mmprojCapturingBackend{}

	// Use MIXED CASE modelID, as would come from user config.
	mgr := NewManagerWithBackend(Config{
		ModelsDir: modelsDir,
		ModelID:   "gemma-4-E2B-it-Q4_K_M",
	}, backend, catalog.BuiltinCatalogPresets())
	defer mgr.Close()

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	mgr.WaitReady()

	expected := filepath.Join(modelsDir, "mmproj-gemma-4-E2B-it-BF16.gguf")
	if backend.mmprojPath != expected {
		t.Fatalf("expected mmprojPath=%q, got %q", expected, backend.mmprojPath)
	}
}

// mmprojCapturingBackend captures the mmprojPath passed to Start.
type mmprojCapturingBackend struct {
	mmprojPath string
}

func (b *mmprojCapturingBackend) Start(_ string, _, _, _ int, _ SamplingParams, _ bool, mmprojPath string) error {
	b.mmprojPath = mmprojPath
	return nil
}
func (b *mmprojCapturingBackend) Stop() error                  { return nil }
func (b *mmprojCapturingBackend) IsStub() bool                 { return false }
func (b *mmprojCapturingBackend) HasVision() bool               { return false }
func (b *mmprojCapturingBackend) ContextSize() int             { return 4096 }
func (b *mmprojCapturingBackend) SetSamplingParams(SamplingParams) {}
func (b *mmprojCapturingBackend) CreateChatCompletionStream(_ context.Context, _ ChatCompletionRequest, _ bool) (<-chan ChatCompletionChunk, error) {
	return nil, fmt.Errorf("not implemented")
}
