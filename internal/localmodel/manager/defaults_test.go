package manager

import "testing"

func TestResolveEnableThinkingDefaultUsesConfiguredValue(t *testing.T) {
	configured := false
	got := ResolveEnableThinkingDefault(&configured, "demo", t.TempDir(), []ModelPreset{{
		ID:       "demo",
		Filename: "demo.gguf",
		Defaults: &ModelPresetDefaults{EnableThinking: boolPtr(true)},
	}})
	if got {
		t.Fatal("expected explicit config to override catalog default")
	}
}

func TestResolveEnableThinkingDefaultUsesCatalogDefault(t *testing.T) {
	got := ResolveEnableThinkingDefault(nil, "demo", t.TempDir(), []ModelPreset{{
		ID:       "demo",
		Filename: "demo.gguf",
		Defaults: &ModelPresetDefaults{EnableThinking: boolPtr(false)},
	}})
	if got {
		t.Fatal("expected catalog default to disable thinking")
	}
}

func TestResolveEnableThinkingDefaultFallsBackToLegacyDefault(t *testing.T) {
	if !ResolveEnableThinkingDefault(nil, "missing", t.TempDir(), nil) {
		t.Fatal("expected fallback thinking default to remain enabled")
	}
}

func boolPtr(v bool) *bool { return &v }