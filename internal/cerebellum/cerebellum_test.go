package cerebellum

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/localmodel"
)

func TestNewManagerDefaultsStopped(t *testing.T) {
	m := NewManager(config.CerebellumConfig{})
	if m.Status() != StatusStopped {
		t.Fatalf("expected status %q, got %q", StatusStopped, m.Status())
	}
	if m.ModelID() != "" {
		t.Fatalf("expected empty modelID, got %q", m.ModelID())
	}
	if len(m.Models()) != 0 {
		t.Fatalf("expected 0 models, got %d", len(m.Models()))
	}
	if m.LastError() != "" {
		t.Fatalf("expected empty lastError, got %q", m.LastError())
	}
}

func TestStartWithoutModelReturnsError(t *testing.T) {
	m := NewManager(config.CerebellumConfig{})
	if err := m.Start(); err == nil {
		t.Fatal("expected error when starting without a model")
	}
	if m.Status() != StatusError {
		t.Fatalf("expected status %q, got %q", StatusError, m.Status())
	}
}

func skipIfStubBackend(t *testing.T) {
	t.Helper()
	m := NewManager(config.CerebellumConfig{})
	if m.Local().IsStub() {
		t.Skip("test requires llama.cpp support (use -tags llamacpp)")
	}
}

func TestStartStopLifecycle(t *testing.T) {
	skipIfStubBackend(t)
	dir := t.TempDir()
	createFakeModel(t, dir, "test-model.gguf")

	m := NewManager(config.CerebellumConfig{ModelsDir: dir})
	if len(m.Models()) != 1 {
		t.Fatalf("expected 1 model, got %d", len(m.Models()))
	}

	if err := m.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	m.WaitReady()
	if m.Status() != StatusRunning {
		t.Fatalf("expected %q, got %q", StatusRunning, m.Status())
	}
	if m.ModelID() != "test-model" {
		t.Fatalf("expected model ID %q, got %q", "test-model", m.ModelID())
	}

	if err := m.Stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	m.WaitReady()
	if m.Status() != StatusStopped {
		t.Fatalf("expected %q, got %q", StatusStopped, m.Status())
	}
}

func TestRestartLifecycle(t *testing.T) {
	skipIfStubBackend(t)
	dir := t.TempDir()
	createFakeModel(t, dir, "model-a.gguf")

	m := NewManager(config.CerebellumConfig{ModelsDir: dir})
	if err := m.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	m.WaitReady()
	if err := m.Restart(); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	m.WaitReady()
	if m.Status() != StatusRunning {
		t.Fatalf("expected %q after restart, got %q", StatusRunning, m.Status())
	}
}

func TestSelectModel(t *testing.T) {
	dir := t.TempDir()
	createFakeModel(t, dir, "alpha.gguf")
	createFakeModel(t, dir, "beta.gguf")

	m := NewManager(config.CerebellumConfig{ModelsDir: dir})
	if len(m.Models()) != 2 {
		t.Fatalf("expected 2 models, got %d", len(m.Models()))
	}

	if err := m.SelectModel("beta"); err != nil {
		t.Fatalf("select model failed: %v", err)
	}
	if m.ModelID() != "beta" {
		t.Fatalf("expected modelID %q, got %q", "beta", m.ModelID())
	}
}

func TestSelectModelNotFound(t *testing.T) {
	m := NewManager(config.CerebellumConfig{})
	if err := m.SelectModel("nonexistent"); err == nil {
		t.Fatal("expected error when selecting nonexistent model")
	}
}

func TestSelectModelWhileRunningRestarts(t *testing.T) {
	skipIfStubBackend(t)
	dir := t.TempDir()
	createFakeModel(t, dir, "model-x.gguf")
	createFakeModel(t, dir, "model-y.gguf")

	m := NewManager(config.CerebellumConfig{ModelsDir: dir})
	_ = m.Start()
	m.WaitReady()
	if m.Status() != StatusRunning {
		t.Fatalf("expected running, got %q", m.Status())
	}

	if err := m.SelectModel("model-y"); err != nil {
		t.Fatalf("select model while running failed: %v", err)
	}
	m.WaitReady()
	if m.ModelID() != "model-y" {
		t.Fatalf("expected modelID %q, got %q", "model-y", m.ModelID())
	}
	if m.Status() != StatusRunning {
		t.Fatalf("expected running after model switch, got %q", m.Status())
	}
}

func TestSnapshotReturnsCorrectState(t *testing.T) {
	skipIfStubBackend(t)
	dir := t.TempDir()
	createFakeModel(t, dir, "snap-model.gguf")

	m := NewManager(config.CerebellumConfig{ModelsDir: dir})
	snap := m.Snapshot(true)
	if !snap.Enabled {
		t.Fatal("expected enabled=true")
	}
	if snap.Status != StatusStopped {
		t.Fatalf("expected status %q, got %q", StatusStopped, snap.Status)
	}
	if len(snap.Models) != 1 {
		t.Fatalf("expected 1 model in snapshot, got %d", len(snap.Models))
	}

	_ = m.Start()
	m.WaitReady()
	snap = m.Snapshot(false)
	if snap.Enabled {
		t.Fatal("expected enabled=false")
	}
	if snap.Status != StatusRunning {
		t.Fatalf("expected status %q, got %q", StatusRunning, snap.Status)
	}
}

func TestDiscoverModelsIgnoresNonGGUF(t *testing.T) {
	dir := t.TempDir()
	createFakeModel(t, dir, "good.gguf")
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir.gguf"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewManager(config.CerebellumConfig{ModelsDir: dir})
	if len(m.Models()) != 1 {
		t.Fatalf("expected 1 model (only .gguf files), got %d", len(m.Models()))
	}
}

func TestStartIdempotent(t *testing.T) {
	skipIfStubBackend(t)
	dir := t.TempDir()
	createFakeModel(t, dir, "m.gguf")

	m := NewManager(config.CerebellumConfig{ModelsDir: dir})
	_ = m.Start()
	m.WaitReady()
	if err := m.Start(); err != nil {
		t.Fatalf("second start should be idempotent: %v", err)
	}
	if m.Status() != StatusRunning {
		t.Fatalf("expected running, got %q", m.Status())
	}
}

func TestStopIdempotent(t *testing.T) {
	m := NewManager(config.CerebellumConfig{})
	if err := m.Stop(); err != nil {
		t.Fatalf("stopping already stopped should be idempotent: %v", err)
	}
	if m.Status() != StatusStopped {
		t.Fatalf("expected stopped, got %q", m.Status())
	}
}

func TestConfiguredModelIDUsed(t *testing.T) {
	skipIfStubBackend(t)
	dir := t.TempDir()
	createFakeModel(t, dir, "first.gguf")
	createFakeModel(t, dir, "second.gguf")

	m := NewManager(config.CerebellumConfig{ModelsDir: dir, ModelID: "second"})
	if err := m.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	m.WaitReady()
	if m.ModelID() != "second" {
		t.Fatalf("expected configured modelID %q, got %q", "second", m.ModelID())
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1610612736, "1.5 GB"},
	}
	for _, tc := range tests {
		got := localmodel.FormatBytes(tc.input)
		if got != tc.expected {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestHumanModelName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Qwen3.5-0.8B-Q4_K_M", "Qwen3.5 0.8B Q4 K M"},
		{"simple", "Simple"},
		{"", ""},
	}
	for _, tc := range tests {
		got := localmodel.HumanModelName(tc.input)
		if got != tc.expected {
			t.Errorf("HumanModelName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// createFakeModel creates a dummy file with .gguf extension.
func createFakeModel(t *testing.T, dir string, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("fake-model-data"), 0o644); err != nil {
		t.Fatal(err)
	}
}
