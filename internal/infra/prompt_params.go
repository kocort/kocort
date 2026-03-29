package infra

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// ResolveReasoningHint returns reasoning-format instructions when the model
// needs explicit <think> tag guidance. Returns "" when reasoning is off or
// the model handles reasoning natively through its API.
func ResolveReasoningHint(thinkLevel string) string {
	level := strings.TrimSpace(strings.ToLower(thinkLevel))
	if level == "" || level == "off" {
		return ""
	}
	return "ALL internal reasoning MUST be inside <think>...</think> tags.\n" +
		"Content outside these tags is visible to the user."
}

// BuildPromptSandboxInfo constructs sandbox metadata for prompt building from
// the agent identity and workspace directory.
func BuildPromptSandboxInfo(identity core.AgentIdentity, defaultWorkdir string) PromptSandboxInfo {
	mode := strings.TrimSpace(strings.ToLower(identity.SandboxMode))
	if mode == "" || mode == "off" {
		return PromptSandboxInfo{}
	}
	return PromptSandboxInfo{
		Enabled:         true,
		Mode:            mode,
		WorkspaceAccess: strings.TrimSpace(identity.SandboxWorkspaceAccess),
		Scope:           strings.TrimSpace(identity.SandboxScope),
		WorkspaceRoot:   strings.TrimSpace(identity.SandboxWorkspaceRoot),
		DefaultWorkdir:  strings.TrimSpace(defaultWorkdir),
		AgentWorkspace:  strings.TrimSpace(identity.WorkspaceDir),
	}
}

// ResolvePromptDocsPath checks for a docs/ directory relative to the current
// working directory and returns the relative path when present.
func ResolvePromptDocsPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(cwd, "docs")
	info, statErr := os.Stat(candidate)
	if statErr != nil || !info.IsDir() {
		return ""
	}
	return "docs"
}

// ToPromptTools converts a slice of PromptTool-compatible objects to
// []PromptTool. This is a type-assertion helper for bridging runtime tool
// slices to prompt building.
func ToPromptTools[T PromptTool](tools []T) []PromptTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]PromptTool, len(tools))
	for i, t := range tools {
		out[i] = t
	}
	return out
}
