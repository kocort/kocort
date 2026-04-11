package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/localmodel/catalog"
)

// ── File scanning and model path resolution ─────────────────────────────────

var splitGGUFPattern = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})$`)

func splitModelID(id string) (base string, shardIndex int, shardCount int, ok bool) {
	matches := splitGGUFPattern.FindStringSubmatch(strings.TrimSpace(id))
	if len(matches) != 4 {
		return "", 0, 0, false
	}
	var idx, total int
	if _, err := fmt.Sscanf(matches[2], "%d", &idx); err != nil {
		return "", 0, 0, false
	}
	if _, err := fmt.Sscanf(matches[3], "%d", &total); err != nil {
		return "", 0, 0, false
	}
	return matches[1], idx, total, true
}

func installedModelFiles(modelsDir, modelID string) []string {
	if strings.TrimSpace(modelsDir) == "" || strings.TrimSpace(modelID) == "" {
		return nil
	}

	directPath := filepath.Join(modelsDir, modelID+".gguf")
	files := make([]string, 0, 1)
	if _, err := os.Stat(directPath); err == nil {
		files = append(files, directPath)
	}

	pattern := filepath.Join(modelsDir, modelID+"-*.gguf")
	matches, _ := filepath.Glob(pattern)
	for _, match := range matches {
		stem := strings.TrimSuffix(filepath.Base(match), filepath.Ext(match))
		base, _, _, ok := splitModelID(stem)
		if ok && base == modelID {
			files = append(files, match)
		}
	}

	sort.Strings(files)
	return files
}

func installedMMProjFiles(modelsDir, modelID string) []string {
	if strings.TrimSpace(modelsDir) == "" || strings.TrimSpace(modelID) == "" {
		return nil
	}

	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		return nil
	}

	files := make([]string, 0, 1)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".gguf") || !catalog.IsMMProjFilename(name) {
			continue
		}
		if catalog.CompanionModelIDFromFilename(name) != modelID {
			continue
		}
		files = append(files, filepath.Join(modelsDir, name))
	}
	sort.Strings(files)
	return files
}

func resolveInstalledModelPath(modelsDir, modelID string) string {
	if strings.TrimSpace(modelsDir) == "" || strings.TrimSpace(modelID) == "" {
		return modelID
	}
	files := installedModelFiles(modelsDir, modelID)
	if len(files) == 0 {
		return filepath.Join(modelsDir, modelID+".gguf")
	}
	for _, file := range files {
		stem := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if base, idx, _, ok := splitModelID(stem); ok && base == modelID && idx == 1 {
			return file
		}
	}
	return files[0]
}

// resolveModelPath returns the filesystem path for the currently selected model.
func (m *Manager) resolveModelPath() string {
	if m.modelsDir == "" || m.modelID == "" {
		return m.modelID
	}
	return resolveInstalledModelPath(m.modelsDir, m.modelID)
}

// scanModels scans the models directory for available GGUF model files.
func scanModels(modelsDir string) []ModelInfo {
	if modelsDir == "" {
		return nil
	}

	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		return nil
	}

	type aggregate struct {
		size         int64
		hasFirst     bool
		hasPrimary   bool
		hasVision    bool
		capabilities catalog.Capabilities
	}
	aggregates := make(map[string]*aggregate, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".gguf") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		id := catalog.ModelIDFromFilename(name)
		if id == "" {
			continue
		}
		isMMProj := catalog.IsMMProjFilename(name)
		hasFirst := true
		if !isMMProj {
			if base, idx, _, ok := splitModelID(stem); ok {
				id = base
				hasFirst = idx == 1
			}
		}

		agg := aggregates[id]
		if agg == nil {
			agg = &aggregate{}
			aggregates[id] = agg
		}
		if info, err := entry.Info(); err == nil {
			agg.size += info.Size()
		}
		if isMMProj {
			agg.hasVision = true
			continue
		}
		agg.hasPrimary = true
		agg.hasFirst = agg.hasFirst || hasFirst
	}

	ids := make([]string, 0, len(aggregates))
	for id, agg := range aggregates {
		if !agg.hasPrimary || !agg.hasFirst {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	models := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		agg := aggregates[id]
		sizeStr := ""
		if agg != nil && agg.size > 0 {
			sizeStr = FormatBytes(agg.size)
		}
		caps := catalog.IntersectCapabilities(
			catalog.InferCapabilities(id, HumanModelName(id), "", agg != nil && agg.hasVision),
			catalog.RuntimeSupportedCapabilities(),
		)
		models = append(models, ModelInfo{
			ID:           id,
			Name:         HumanModelName(id),
			Size:         sizeStr,
			Capabilities: caps,
		})
	}

	return models
}

// findPresetDefaults returns the Defaults block for the preset whose ID
// matches modelID, or nil if no match is found.
func findPresetDefaults(catalog []ModelPreset, modelID string) *ModelPresetDefaults {
	for _, p := range catalog {
		if p.ID == modelID {
			return p.Defaults
		}
	}
	return nil
}

// ResolveEnableThinkingDefault determines the enableThinking setting using
// the following priority:
//  1. Explicit user configuration (*configured != nil).
//  2. Catalog preset default for the given modelID.
//  3. Fallback: true (thinking enabled by default).
func ResolveEnableThinkingDefault(configured *bool, modelID, modelsDir string, catalog []ModelPreset) bool {
	if configured != nil {
		return *configured
	}
	if defaults := findPresetDefaults(catalog, modelID); defaults != nil && defaults.EnableThinking != nil {
		return *defaults.EnableThinking
	}
	return true // default: thinking enabled
}
