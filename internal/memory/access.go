package memory

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

func ResolveReadablePath(workspaceDir string, identity core.AgentIdentity, value string) (string, string, bool) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	clean := filepath.Clean(strings.TrimSpace(value))
	if clean == "." || clean == "" {
		return "", "", false
	}
	candidateAbs := clean
	if !filepath.IsAbs(candidateAbs) {
		candidateAbs = filepath.Join(workspaceDir, candidateAbs)
	}
	candidateAbs = filepath.Clean(candidateAbs)

	if display, ok := resolveWorkspaceMemoryPath(workspaceDir, clean, candidateAbs); ok {
		return display, candidateAbs, true
	}
	for _, rawPath := range identity.MemoryExtraPaths {
		display, ok := resolveExtraMemoryPath(workspaceDir, strings.TrimSpace(rawPath), candidateAbs)
		if ok {
			return display, candidateAbs, true
		}
	}
	return "", "", false
}

func ReadAllowedTextFile(absPath string) (string, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(content), nil
}

func resolveWorkspaceMemoryPath(workspaceDir, clean, candidateAbs string) (string, bool) {
	switch clean {
	case DefaultMemoryFilename, DefaultMemoryAltFile:
		return clean, true
	}
	if pathHasDirPrefix(clean, "memory") {
		rel, err := filepath.Rel(workspaceDir, candidateAbs)
		if err != nil {
			return "", false
		}
		rel = filepath.Clean(rel)
		if pathEscapesBase(rel) {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	if workspaceDir == "" {
		return "", false
	}
	rel, err := filepath.Rel(workspaceDir, candidateAbs)
	if err != nil {
		return "", false
	}
	rel = filepath.Clean(rel)
	switch rel {
	case DefaultMemoryFilename, DefaultMemoryAltFile:
		return filepath.ToSlash(rel), true
	}
	if !pathEscapesBase(rel) && pathHasDirPrefix(rel, "memory") {
		return filepath.ToSlash(rel), true
	}
	return "", false
}

func resolveExtraMemoryPath(workspaceDir, rawPath, candidateAbs string) (string, bool) {
	if rawPath == "" {
		return "", false
	}
	allowedAbs := rawPath
	if !filepath.IsAbs(allowedAbs) {
		allowedAbs = filepath.Join(workspaceDir, allowedAbs)
	}
	allowedAbs = filepath.Clean(allowedAbs)

	info, err := os.Stat(allowedAbs)
	if err == nil && info.IsDir() {
		rel, relErr := filepath.Rel(allowedAbs, candidateAbs)
		if relErr != nil {
			return "", false
		}
		rel = filepath.Clean(rel)
		if rel == "." || pathEscapesBase(rel) {
			return "", false
		}
		if workspaceDir != "" {
			if workspaceRel, err := filepath.Rel(workspaceDir, candidateAbs); err == nil {
				workspaceRel = filepath.Clean(workspaceRel)
				if !pathEscapesBase(workspaceRel) {
					return filepath.ToSlash(workspaceRel), true
				}
			}
		}
		return filepath.ToSlash(candidateAbs), true
	}
	if err == nil && !info.IsDir() && filepath.Clean(candidateAbs) == allowedAbs {
		if workspaceDir != "" {
			if rel, relErr := filepath.Rel(workspaceDir, allowedAbs); relErr == nil {
				rel = filepath.Clean(rel)
				if !pathEscapesBase(rel) {
					return filepath.ToSlash(rel), true
				}
			}
		}
		return filepath.ToSlash(allowedAbs), true
	}
	if os.IsNotExist(err) && filepath.Clean(candidateAbs) == allowedAbs {
		return filepath.ToSlash(allowedAbs), true
	}
	return "", false
}

func pathEscapesBase(rel string) bool {
	rel = filepath.Clean(strings.TrimSpace(rel))
	if rel == "" || rel == "." {
		return false
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func pathHasDirPrefix(pathValue, prefix string) bool {
	clean := filepath.Clean(strings.TrimSpace(pathValue))
	if clean == prefix {
		return true
	}
	rel, err := filepath.Rel(prefix, clean)
	if err != nil {
		return false
	}
	return !pathEscapesBase(rel)
}
