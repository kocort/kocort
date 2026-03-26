package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) downloadsDir() string {
	return filepath.Join(m.artifactDir, "downloads")
}

func (m *Manager) traceDir() string {
	return filepath.Join(m.artifactDir, "traces")
}

func (m *Manager) resolveOutputPath(rootDir, requestedPath, defaultName string) (string, error) {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	requestedPath = strings.TrimSpace(requestedPath)
	var candidate string
	if requestedPath == "" {
		candidate = filepath.Join(rootAbs, defaultName)
	} else if filepath.IsAbs(requestedPath) {
		candidate = filepath.Clean(requestedPath)
	} else {
		candidate = filepath.Join(rootAbs, requestedPath)
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path outside allowed directory")
	}
	parent := filepath.Dir(candidateAbs)
	if info, err := os.Lstat(parent); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("invalid symlinked output directory")
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return candidateAbs, nil
}
