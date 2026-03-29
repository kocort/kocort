package runtime

import (
	"context"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"

	"net/http"
	"strings"
	"testing"

	"github.com/kocort/kocort/utils"
)

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
	shell := newTestShellHelper(t)
	command, args := shell.Command(shell.JoinedEnvScript("GLOBAL_KEY", "INLINE_KEY", "KOCORT_AGENT_DIR"))
	backend := &backend.CommandBackend{
		Config: core.CommandBackendConfig{
			Command:    command,
			Args:       args,
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


