package runtime

import (
	"context"
	"encoding/json"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"

	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kocort/kocort/utils"
)

func TestEnvironmentRuntimeResolvesTemplatesAndMasks(t *testing.T) {
	t.Setenv("FROM_PROCESS", "process-secret")
	rt := infra.NewEnvironmentRuntime(config.EnvironmentConfig{
		Strict: utils.BoolPtr(true),
		Entries: map[string]config.EnvironmentEntryConfig{
			"API_KEY":    {Value: "local-secret", Masked: utils.BoolPtr(true)},
			"COMBINED":   {Value: "token=${API_KEY}"},
			"FROM_ENV":   {FromEnv: "FROM_PROCESS", Masked: utils.BoolPtr(true)},
			"UNMASKED":   {Value: "hello"},
			"REQUIRED_X": {FromEnv: "MISSING_REQUIRED", Required: utils.BoolPtr(true)},
		},
	})

	if got, ok := rt.Resolve("COMBINED"); !ok || got != "token=local-secret" {
		t.Fatalf("expected resolved template, got %q ok=%v", got, ok)
	}
	if got, ok := rt.Resolve("FROM_ENV"); !ok || got != "process-secret" {
		t.Fatalf("expected process env resolution, got %q ok=%v", got, ok)
	}
	masked := rt.MaskString("API local-secret process-secret hello")
	if strings.Contains(masked, "local-secret") || strings.Contains(masked, "process-secret") {
		t.Fatalf("expected masked output, got %q", masked)
	}
	if !strings.Contains(masked, "hello") {
		t.Fatalf("expected unmasked value to remain, got %q", masked)
	}
}

func TestOpenAICompatBackendResolvesProviderAPIKeyFromEnvironmentConfig(t *testing.T) {
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer resolved-secret" {
			t.Fatalf("expected resolved API key header, got %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"resp_1\",\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\",\"index\":0}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer cleanup()

	backend := backend.NewOpenAICompatBackend(config.AppConfig{
		Env: config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"MODEL_API_KEY": {Value: "resolved-secret", Masked: utils.BoolPtr(true)},
			},
		},
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: serverURL + "/v1",
					APIKey:  "${MODEL_API_KEY}",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-test", MaxTokens: 16}},
				},
			},
		},
	}, infra.NewEnvironmentRuntime(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"MODEL_API_KEY": {Value: "resolved-secret", Masked: utils.BoolPtr(true)},
		},
	}), nil)
	backend.BlockReplyCoalescing = nil
	runCtx := rtypes.AgentRunContext{
		Request:        core.AgentRunRequest{RunID: "run_env_openai", Message: "hi"},
		Session:        core.SessionResolution{SessionKey: "agent:main:main", SessionID: "sess_env"},
		ModelSelection: core.ModelSelection{Provider: "openai", Model: "gpt-test"},
		ReplyDispatcher: delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{
			SessionKey: "agent:main:main",
		}),
		Runtime: &Runtime{},
	}
	result, err := backend.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("run backend: %v", err)
	}
	if len(result.Payloads) == 0 || strings.TrimSpace(result.Payloads[len(result.Payloads)-1].Text) != "OK" {
		t.Fatalf("expected final text OK, got %+v", result.Payloads)
	}
}

func TestCommandBackendInjectsEnvironmentRuntimeAndOverrides(t *testing.T) {
	backend := &backend.CommandBackend{
		Config: core.CommandBackendConfig{
			Command:    "/bin/sh",
			Args:       []string{"-lc", "echo \"$GLOBAL_KEY|$INLINE_KEY|$KOCORT_AGENT_DIR\""},
			OutputMode: core.CommandBackendOutputText,
			Env: map[string]string{
				"INLINE_KEY": "${INLINE_SOURCE}",
			},
		},
		Env: infra.NewEnvironmentRuntime(config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"GLOBAL_KEY":    {Value: "global-value"},
				"INLINE_SOURCE": {Value: "inline-value"},
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{RunID: "run_env_cmd", Message: "hello"},
		Session: core.SessionResolution{SessionKey: "agent:main:main", SessionID: "sess_env_cmd"},
		Identity: core.AgentIdentity{
			ID:       "main",
			AgentDir: "/tmp/agent-dir",
		},
		ReplyDispatcher: delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"}),
	}
	result, err := backend.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("run command backend: %v", err)
	}
	if len(result.Payloads) == 0 || strings.TrimSpace(result.Payloads[len(result.Payloads)-1].Text) != "global-value|inline-value|/tmp/agent-dir" {
		t.Fatalf("unexpected command output %+v", result.Payloads)
	}
}

func TestRuntimeReloadEnvironmentUpdatesResolvedValue(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"test": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "test",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "test/model", Reasoning: true}},
				},
			},
		},
		Env: config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"TOKEN": {Value: "one"},
			},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{StateDir: t.TempDir(), Deliverer: &delivery.MemoryDeliverer{}})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	if got, _ := rt.Environment.Resolve("TOKEN"); got != "one" {
		t.Fatalf("expected initial token, got %q", got)
	}
	rt.Config.Env.Entries["TOKEN"] = config.EnvironmentEntryConfig{Value: "two"}
	rt.ReloadEnvironment()
	if got, _ := rt.Environment.Resolve("TOKEN"); got != "two" {
		t.Fatalf("expected reloaded token, got %q", got)
	}
}

func TestEnvironmentConfigRoundTrips(t *testing.T) {
	cfg := config.AppConfig{
		Env: config.EnvironmentConfig{
			Strict: utils.BoolPtr(true),
			Entries: map[string]config.EnvironmentEntryConfig{
				"API_KEY": {Value: "x", Masked: utils.BoolPtr(true), Required: utils.BoolPtr(true)},
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded config.AppConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Env.Strict == nil || !*decoded.Env.Strict {
		t.Fatalf("expected strict to survive roundtrip")
	}
	if entry := decoded.Env.Entries["API_KEY"]; entry.Masked == nil || !*entry.Masked || entry.Required == nil || !*entry.Required {
		t.Fatalf("expected entry flags to survive roundtrip")
	}
}

func TestEnvironmentRuntimeUsesProcessEnvFallbackWithoutEntry(t *testing.T) {
	t.Setenv("DIRECT_FALLBACK", "fallback-value")
	rt := infra.NewEnvironmentRuntime(config.EnvironmentConfig{})
	if got, ok := rt.Resolve("DIRECT_FALLBACK"); !ok || got != "fallback-value" {
		t.Fatalf("expected process env fallback, got %q ok=%v", got, ok)
	}
}

func TestEnvironmentRuntimeResolveMapStrictError(t *testing.T) {
	rt := infra.NewEnvironmentRuntime(config.EnvironmentConfig{Strict: utils.BoolPtr(true)})
	_, err := rt.ResolveMap(map[string]string{"A": "${MISSING_VALUE}"})
	if err == nil || !strings.Contains(err.Error(), "MISSING_VALUE") {
		t.Fatalf("expected missing value error, got %v", err)
	}
}

func TestEnvironmentRuntimeAppendToEnvAddsConfiguredEntries(t *testing.T) {
	rt := infra.NewEnvironmentRuntime(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"GLOBAL_A": {Value: "A"},
		},
	})
	env, err := rt.AppendToEnv(os.Environ(), map[string]string{"INLINE_B": "B"})
	if err != nil {
		t.Fatalf("append env: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GLOBAL_A=A") || !strings.Contains(joined, "INLINE_B=B") {
		t.Fatalf("expected appended env values, got %q", joined)
	}
}
