package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/utils"
)

var toolUserHomeDir = os.UserHomeDir

func resolveWorkspaceToolPath(toolCtx ToolContext, value string) (workspaceDir string, relative string, absPath string, err error) {
	workspaceDir, err = resolveToolDefaultWorkingDir(toolCtx)
	if err != nil {
		return "", "", "", err
	}
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", "", "", ToolInputError{Message: "path is required"}
	}
	absPath, err = normalizeToolInputPath(workspaceDir, raw)
	if err != nil {
		return "", "", "", err
	}
	if err := ensurePathWithinToolSandbox(toolCtx, absPath); err != nil {
		return "", "", "", err
	}
	return workspaceDir, displayToolPath(workspaceDir, absPath), absPath, nil
}

func resolveToolDefaultWorkingDir(toolCtx ToolContext) (string, error) {
	workspaceDir := strings.TrimSpace(toolCtx.Run.WorkspaceDir)
	if workspaceDir == "" {
		return "", fmt.Errorf("working directory is not configured")
	}
	return normalizeConfiguredToolPath(workspaceDir)
}

// resolveToolAccessBoundaryDirs returns every directory that should be treated
// as inside the sandbox boundary for the current tool invocation.
//
// This always includes the tool's default working directory. When sandboxing is
// enabled, it also includes the sandbox-owned workspace plus any additional
// configured sandbox directories.
func resolveToolAccessBoundaryDirs(toolCtx ToolContext) ([]string, error) {
	if toolCtx.Sandbox == nil || !toolCtx.Sandbox.Enabled {
		return nil, nil
	}
	rawDirs := make([]string, 0, len(toolCtx.Sandbox.SandboxDirs)+2)
	if workdir := strings.TrimSpace(toolCtx.Run.WorkspaceDir); workdir != "" {
		rawDirs = append(rawDirs, workdir)
	}
	if sandboxDir := strings.TrimSpace(toolCtx.Sandbox.WorkspaceDir); sandboxDir != "" {
		rawDirs = append(rawDirs, sandboxDir)
	}
	rawDirs = append(rawDirs, toolCtx.Sandbox.SandboxDirs...)
	dirs := make([]string, 0, len(rawDirs))
	seen := map[string]struct{}{}
	for _, raw := range rawDirs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		absDir, err := normalizeConfiguredToolPath(trimmed)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[absDir]; ok {
			continue
		}
		seen[absDir] = struct{}{}
		dirs = append(dirs, absDir)
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	return dirs, nil
}

func ensurePathWithinToolSandbox(toolCtx ToolContext, absPath string) error {
	workingDir, err := resolveToolDefaultWorkingDir(toolCtx)
	if err != nil {
		return err
	}
	if pathWithinBase(absPath, workingDir) {
		return nil
	}
	sandboxDirs, err := resolveToolAccessBoundaryDirs(toolCtx)
	if err != nil {
		return err
	}
	if len(sandboxDirs) == 0 {
		return nil
	}
	for _, sandboxDir := range sandboxDirs {
		if pathWithinBase(absPath, sandboxDir) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside sandbox directories", absPath)
}

func displayToolPath(workspaceDir string, absPath string) string {
	rel, err := filepath.Rel(workspaceDir, absPath)
	if err != nil {
		return absPath
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return "."
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return filepath.ToSlash(rel)
	}
	return absPath
}

func pathWithinBase(path string, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func readWorkspaceFileLines(absPath string) ([]string, string, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, "", err
	}
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := []string{}
	if normalized != "" {
		lines = strings.Split(normalized, "\n")
	}
	return lines, normalized, nil
}

// fileURI converts an absolute filesystem path to a proper file:// URI.
// Delegates to utils.FileURI which uses net/url for cross-platform correctness.
func fileURI(absPath string) string {
	return utils.FileURI(absPath)
}

func normalizeConfiguredToolPath(value string) (string, error) {
	expanded, err := expandToolUserPath(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

func normalizeToolInputPath(baseDir string, value string) (string, error) {
	expanded, err := expandToolUserPath(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	target := expanded
	if !filepath.IsAbs(target) {
		target = filepath.Join(baseDir, target)
	}
	return filepath.Abs(target)
}

func expandToolUserPath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if value != "~" {
		if !strings.HasPrefix(value, "~") || len(value) < 2 || !isToolPathSeparator(value[1]) {
			return value, nil
		}
	}
	if value == "~" {
		homeDir, err := toolUserHomeDir()
		if err != nil {
			return "", err
		}
		homeDir = strings.TrimSpace(homeDir)
		if homeDir == "" {
			return "", fmt.Errorf("user home directory is not available")
		}
		return homeDir, nil
	}
	homeDir, err := toolUserHomeDir()
	if err != nil {
		return "", err
	}
	homeDir = strings.TrimSpace(homeDir)
	if homeDir == "" {
		return "", fmt.Errorf("user home directory is not available")
	}
	remainder := strings.TrimLeftFunc(value[1:], func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if remainder == "" {
		return homeDir, nil
	}
	parts := strings.FieldsFunc(remainder, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) == 0 {
		return homeDir, nil
	}
	return filepath.Join(append([]string{homeDir}, parts...)...), nil
}

func isToolPathSeparator(ch byte) bool {
	return ch == '/' || ch == '\\'
}

func sliceLines(lines []string, from int, count int) (int, int, []string) {
	if from <= 0 {
		from = 1
	}
	if count <= 0 {
		count = len(lines)
	}
	start := min(len(lines), max(0, from-1))
	end := min(len(lines), start+count)
	if start >= len(lines) {
		return from, from - 1, nil
	}
	return start + 1, end, lines[start:end]
}
