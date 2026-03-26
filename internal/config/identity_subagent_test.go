package config

import "testing"

func TestBuildConfiguredAgentIdentityCarriesSubagentAttachmentsEnabled(t *testing.T) {
	disabled := false
	cfg := AppConfig{
		Models: ModelsConfig{
			Providers: map[string]ProviderConfig{
				"openai": {
					Models: []ProviderModelConfig{{ID: "gpt-4.1-mini"}},
				},
			},
		},
		Agents: AgentsConfig{
			Defaults: &AgentDefaultsConfig{
				Model: AgentModelConfig{Primary: "openai/gpt-4.1-mini"},
				Subagents: AgentSubagentConfig{
					AttachmentsEnabled: &disabled,
				},
			},
		},
	}
	identity, err := BuildConfiguredAgentIdentity(cfg, t.TempDir(), "main", "", "", "")
	if err != nil {
		t.Fatalf("build configured identity: %v", err)
	}
	if identity.SubagentAttachmentsEnabled {
		t.Fatalf("expected attachments disabled on identity, got %+v", identity)
	}
}
