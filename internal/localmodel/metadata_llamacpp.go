//go:build llamacpp

package localmodel

import (
	"strings"

	"github.com/kocort/kocort/internal/llama"
)

func detectModelThinkingDefault(modelPath string) (bool, bool) {
	arch, err := llama.GetModelArch(modelPath)
	if err != nil {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "qwen3", "qwen3moe", "qwen35", "qwen35moe", "gemma4":
		return true, true
	default:
		return false, true
	}
}