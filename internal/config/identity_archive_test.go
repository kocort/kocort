package config

import (
	"testing"
)

func TestConfiguredAgentAllowsArchiveAfterMinutesZeroOverride(t *testing.T) {
	archiveDefault := 60
	archiveDisabled := 0
	cfg := AppConfig{
		Models: ModelsConfig{
			Providers: map[string]ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models:  []ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
		Agents: AgentsConfig{
			Defaults: &AgentDefaultsConfig{
				Model: AgentModelConfig{Primary: "openai/gpt-4.1"},
				Subagents: AgentSubagentConfig{
					ArchiveAfterMinutes: &archiveDefault,
				},
			},
			List: []AgentConfig{
				{
					ID:      "main",
					Default: true,
				},
				{
					ID: "worker",
					Subagents: AgentSubagentConfig{
						ArchiveAfterMinutes: &archiveDisabled,
					},
				},
			},
		},
	}
	identity, err := BuildConfiguredAgentIdentity(cfg, t.TempDir(), "worker", "", "", "")
	if err != nil {
		t.Fatalf("build identity: %v", err)
	}
	if identity.SubagentArchiveAfterMinutes != 0 {
		t.Fatalf("expected explicit zero archive override, got %d", identity.SubagentArchiveAfterMinutes)
	}
}
