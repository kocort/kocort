package config

import "testing"

func TestMergeSubagentConfigCopiesAttachmentsEnabled(t *testing.T) {
	disabled := false
	base := AgentSubagentConfig{}
	override := AgentSubagentConfig{AttachmentsEnabled: &disabled}
	got := MergeSubagentConfig(base, override)
	if got.AttachmentsEnabled == nil || *got.AttachmentsEnabled {
		t.Fatalf("expected attachmentsEnabled=false, got %+v", got.AttachmentsEnabled)
	}
}
