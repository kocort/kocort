package infra

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/utils"
)

func TestEnvironmentRuntimeResolvesTemplatesAndMasks(t *testing.T) {
	t.Setenv("FROM_PROCESS", "process-secret")
	rt := NewEnvironmentRuntime(config.EnvironmentConfig{
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
	rt := NewEnvironmentRuntime(config.EnvironmentConfig{})
	if got, ok := rt.Resolve("DIRECT_FALLBACK"); !ok || got != "fallback-value" {
		t.Fatalf("expected process env fallback, got %q ok=%v", got, ok)
	}
}

func TestEnvironmentRuntimeResolveMapStrictError(t *testing.T) {
	rt := NewEnvironmentRuntime(config.EnvironmentConfig{Strict: utils.BoolPtr(true)})
	_, err := rt.ResolveMap(map[string]string{"A": "${MISSING_VALUE}"})
	if err == nil || !strings.Contains(err.Error(), "MISSING_VALUE") {
		t.Fatalf("expected missing value error, got %v", err)
	}
}

func TestEnvironmentRuntimeAppendToEnvAddsConfiguredEntries(t *testing.T) {
	rt := NewEnvironmentRuntime(config.EnvironmentConfig{
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
