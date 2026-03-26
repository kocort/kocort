package runtime

import (
	"context"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
)

func TestRuntimeBuilderBuild_MinimalConfig(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "key",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
	}
	params := config.RuntimeConfigParams{
		StateDir: t.TempDir(),
		AgentID:  "main",
	}
	rt, err := NewRuntimeBuilder(cfg, params).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
	if rt.Sessions == nil {
		t.Error("expected Sessions to be set")
	}
	if rt.Logger == nil {
		t.Error("expected Logger to be set")
	}
	if rt.Audit == nil {
		t.Error("expected Audit to be set")
	}
	if rt.Environment == nil {
		t.Error("expected Environment to be set")
	}
	if rt.Backends == nil {
		t.Error("expected Backends to be set")
	}
	if rt.Tasks == nil {
		t.Error("expected Tasks to be set")
	}
	if rt.Tools == nil {
		t.Error("expected Tools to be set")
	}
}

func TestRuntimeBuilderBuild_WithOverrides(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "key",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
	}
	params := config.RuntimeConfigParams{
		StateDir: t.TempDir(),
		AgentID:  "main",
	}

	customAudit := &mockAuditRecorder{}
	customLogger := &mockLogger{}

	rt, err := NewRuntimeBuilder(cfg, params).
		WithAudit(customAudit).
		WithLogger(customLogger).
		WithMemory(infra.NullMemoryProvider{}).
		WithDeliverer(&delivery.MemoryDeliverer{}).
		Build()

	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Verify custom overrides are preserved (compare via AuditRecorder interface).
	if _, ok := rt.Audit.(*mockAuditRecorder); !ok {
		t.Error("expected custom audit recorder")
	}
	if _, ok := rt.Logger.(*mockLogger); !ok {
		t.Error("expected custom logger")
	}
}

func TestRuntimeBuilderBuild_WithApprovals(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "key",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
	}
	params := config.RuntimeConfigParams{
		StateDir: t.TempDir(),
		AgentID:  "main",
	}
	approvals := stubToolApprovalRunner{}
	customAudit := &mockAuditRecorder{}
	customLogger := &mockLogger{}

	rt, err := NewRuntimeBuilder(cfg, params).
		WithAudit(customAudit).
		WithLogger(customLogger).
		WithApprovals(approvals).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rt.Approvals == nil {
		t.Fatal("expected approvals runner to be wired")
	}
}

func TestRuntimeBuilderBuild_DelegatesToNewRuntimeFromConfig(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "key",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
	}
	params := config.RuntimeConfigParams{
		StateDir: t.TempDir(),
		AgentID:  "main",
	}
	rt, err := NewRuntimeFromConfig(cfg, params)
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
}

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

type mockAuditRecorder struct{}

func (m *mockAuditRecorder) Record(_ context.Context, _ core.AuditEvent) error {
	return nil
}
func (m *mockAuditRecorder) List(_ context.Context, _ core.AuditQuery) ([]core.AuditEvent, error) {
	return nil, nil
}

type mockLogger struct{}

func (m *mockLogger) LogAuditEvent(_ core.AuditEvent) {}
func (m *mockLogger) Reload(_ config.LoggingConfig, _ string) error {
	return nil
}
