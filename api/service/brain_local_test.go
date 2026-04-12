package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/localmodel"
	"github.com/kocort/kocort/runtime"
)

type brainLocalTestBackend struct {
	running     bool
	contextSize int
}

func (b *brainLocalTestBackend) Start(_ string, _ int, contextSize, _ int, _ localmodel.SamplingParams, _ bool, _ string) error {
	b.running = true
	b.contextSize = contextSize
	return nil
}

func (b *brainLocalTestBackend) Stop() error {
	b.running = false
	return nil
}

func (b *brainLocalTestBackend) IsStub() bool {
	return false
}

func (b *brainLocalTestBackend) ContextSize() int {
	return b.contextSize
}

func (b *brainLocalTestBackend) SetSamplingParams(localmodel.SamplingParams) {}

func (b *brainLocalTestBackend) HasVision() bool { return false }

func (b *brainLocalTestBackend) CreateChatCompletionStream(context.Context, localmodel.ChatCompletionRequest, bool) (<-chan localmodel.ChatCompletionChunk, error) {
	ch := make(chan localmodel.ChatCompletionChunk)
	close(ch)
	return ch, nil
}

type brainLocalTestAuditRecorder struct{}

func (m *brainLocalTestAuditRecorder) Record(_ context.Context, _ core.AuditEvent) error {
	return nil
}

func (m *brainLocalTestAuditRecorder) List(_ context.Context, _ core.AuditQuery) ([]core.AuditEvent, error) {
	return nil, nil
}

type brainLocalTestLogger struct{}

func (m *brainLocalTestLogger) LogAuditEvent(_ core.AuditEvent) {}

func (m *brainLocalTestLogger) Reload(_ config.LoggingConfig, _ string) error {
	return nil
}

func TestBrainLocalStartStopPersistAutoStartPreference(t *testing.T) {
	rt, _ := newBrainLocalServiceTestRuntime(t, "local", false)

	if err := BrainLocalStart(rt); err != nil {
		t.Fatalf("BrainLocalStart: %v", err)
	}
	rt.BrainLocal.WaitReady()
	if rt.Config.BrainLocal.AutoStart == nil || !*rt.Config.BrainLocal.AutoStart {
		t.Fatalf("expected runtime autoStart=true after manual start, got %+v", rt.Config.BrainLocal.AutoStart)
	}

	if err := BrainLocalStop(rt); err != nil {
		t.Fatalf("BrainLocalStop: %v", err)
	}
	rt.BrainLocal.WaitReady()
	if rt.Config.BrainLocal.AutoStart == nil || *rt.Config.BrainLocal.AutoStart {
		t.Fatalf("expected runtime autoStart=false after manual stop, got %+v", rt.Config.BrainLocal.AutoStart)
	}
}

func TestBrainModeSwitchToLocalRespectsDisabledAutoStart(t *testing.T) {
	rt, _ := newBrainLocalServiceTestRuntime(t, "cloud", false)

	if err := BrainModeSwitch(rt, "local", nil); err != nil {
		t.Fatalf("BrainModeSwitch: %v", err)
	}
	if got := rt.BrainLocal.Status(); got != localmodel.StatusStopped {
		t.Fatalf("expected brain-local model to remain stopped when autoStart=false, got %s", got)
	}
	if rt.Config.BrainMode != "local" {
		t.Fatalf("expected runtime brain mode to persist as local, got %q", rt.Config.BrainMode)
	}
}

func TestBuildBrainLocalStateIncludesModelsDir(t *testing.T) {
	rt, _ := newBrainLocalServiceTestRuntime(t, "local", false)

	state := BuildBrainLocalState(rt)
	if state == nil {
		t.Fatal("expected brain-local state to be available")
	}
	if state.ModelsDir != rt.Config.BrainLocal.ModelsDir {
		t.Fatalf("expected ModelsDir %q, got %q", rt.Config.BrainLocal.ModelsDir, state.ModelsDir)
	}
	if len(state.Models) == 0 {
		t.Fatal("expected at least one model in state")
	}
}

func newBrainLocalServiceTestRuntime(t *testing.T, brainMode string, autoStart bool) (*runtime.Runtime, string) {
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
		BrainMode: brainMode,
		BrainLocal: config.BrainLocalConfig{
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
		WithAudit(&brainLocalTestAuditRecorder{}).
		WithLogger(&brainLocalTestLogger{}).
		Build()
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	rt.BrainLocal = localmodel.NewManagerWithBackend(localmodel.Config{
		ModelID:   "demo",
		ModelsDir: modelsDir,
	}, &brainLocalTestBackend{}, localmodel.BuiltinCatalogPresets())

	return rt, configDir
}
