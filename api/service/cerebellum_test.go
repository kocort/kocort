package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	internalcerebellum "github.com/kocort/kocort/internal/cerebellum"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/runtime"
)

type cerebellumTestAuditRecorder struct{}

func (m *cerebellumTestAuditRecorder) Record(_ context.Context, _ core.AuditEvent) error {
	return nil
}

func (m *cerebellumTestAuditRecorder) List(_ context.Context, _ core.AuditQuery) ([]core.AuditEvent, error) {
	return nil, nil
}

type cerebellumTestLogger struct{}

func (m *cerebellumTestLogger) LogAuditEvent(_ core.AuditEvent) {}

func (m *cerebellumTestLogger) Reload(_ config.LoggingConfig, _ string) error {
	return nil
}

func TestCerebellumStopPersistsAutoStartPreference(t *testing.T) {
	rt := newCerebellumServiceTestRuntime(t, false)

	if err := persistCerebellumAutoStart(rt, true); err != nil {
		t.Fatalf("persistCerebellumAutoStart(true): %v", err)
	}
	if rt.Config.Cerebellum.AutoStart == nil || !*rt.Config.Cerebellum.AutoStart {
		t.Fatalf("expected runtime autoStart=true after persisting manual start, got %+v", rt.Config.Cerebellum.AutoStart)
	}

	if err := CerebellumStop(rt); err != nil {
		t.Fatalf("CerebellumStop: %v", err)
	}
	rt.Cerebellum.WaitReady()
	if rt.Config.Cerebellum.AutoStart == nil || *rt.Config.Cerebellum.AutoStart {
		t.Fatalf("expected runtime autoStart=false after manual stop, got %+v", rt.Config.Cerebellum.AutoStart)
	}

	state := BuildCerebellumState(rt)
	if state == nil {
		t.Fatal("expected cerebellum state to be available")
	}
	if state.AutoStart {
		t.Fatal("expected API state autoStart=false after manual stop")
	}
	if got := rt.Cerebellum.Status(); got != internalcerebellum.StatusStopped {
		t.Fatalf("expected cerebellum to remain stopped, got %s", got)
	}
}

func TestRuntimeBuilderRespectsDisabledCerebellumAutoStart(t *testing.T) {
	rt := newCerebellumServiceTestRuntime(t, false)

	if got := rt.Cerebellum.Status(); got != internalcerebellum.StatusStopped {
		t.Fatalf("expected cerebellum to remain stopped when autoStart=false, got %s", got)
	}
	if rt.Config.Cerebellum.AutoStart == nil || *rt.Config.Cerebellum.AutoStart {
		t.Fatalf("expected runtime config autoStart=false, got %+v", rt.Config.Cerebellum.AutoStart)
	}
	state := BuildCerebellumState(rt)
	if state == nil || state.AutoStart {
		t.Fatalf("expected API state autoStart=false, got %+v", state)
	}
}

func TestBuildCerebellumStateIncludesModelsDir(t *testing.T) {
	rt := newCerebellumServiceTestRuntime(t, false)

	state := BuildCerebellumState(rt)
	if state == nil {
		t.Fatal("expected cerebellum state to be available")
	}
	if state.ModelsDir != rt.Config.Cerebellum.ModelsDir {
		t.Fatalf("expected ModelsDir %q, got %q", rt.Config.Cerebellum.ModelsDir, state.ModelsDir)
	}
	if len(state.Catalog) == 0 || state.Catalog[0].Description == nil {
		t.Fatal("expected catalog description to be present")
	}
	if state.Catalog[0].Description.Zh == "" || state.Catalog[0].Description.En == "" {
		t.Fatalf("expected bilingual catalog description, got %+v", state.Catalog[0].Description)
	}
}

func newCerebellumServiceTestRuntime(t *testing.T, autoStart bool) *runtime.Runtime {
	t.Helper()

	configDir := t.TempDir()
	stateDir := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	modelsDir := filepath.Join(t.TempDir(), "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatalf("mkdir models dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelsDir, "demo.gguf"), []byte("fake-model"), 0o644); err != nil {
		t.Fatalf("write fake model: %v", err)
	}
	enabled := true

	rt, err := runtime.NewRuntimeBuilder(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "main",
				Default:   true,
				Workspace: workspace,
				Model:     config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		BrainMode: "cloud",
		Cerebellum: config.CerebellumConfig{
			Enabled:   &enabled,
			ModelID:   "demo",
			ModelsDir: modelsDir,
			AutoStart: &autoStart,
		},
		Channels: config.ChannelsConfig{Entries: map[string]config.ChannelConfig{}},
	}, config.RuntimeConfigParams{
		StateDir:  stateDir,
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
		ConfigLoad: config.ConfigLoadOptions{
			ConfigDir: configDir,
		},
	}).
		WithAudit(&cerebellumTestAuditRecorder{}).
		WithLogger(&cerebellumTestLogger{}).
		Build()
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	return rt
}
