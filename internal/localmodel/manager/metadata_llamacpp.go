package manager

import (
	"strings"

	"github.com/kocort/kocort/internal/localmodel/ffi"
)

func detectModelThinkingDefault(modelPath string) (bool, bool) {
	ffi.BackendInit()
	lib := ffi.DefaultLibrary()
	if lib == nil {
		return false, false
	}
	arch, err := ffi.GetModelArch(lib, modelPath)
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
