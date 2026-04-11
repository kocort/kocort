package catalog

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Capabilities describes model features surfaced in the UI.
type Capabilities struct {
	Vision    bool `json:"vision,omitempty"`
	Audio     bool `json:"audio,omitempty"`
	Video     bool `json:"video,omitempty"`
	Tools     bool `json:"tools,omitempty"`
	Reasoning bool `json:"reasoning,omitempty"`
	Coding    bool `json:"coding,omitempty"`
}

var splitGGUFPattern = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})$`)

func splitModelID(id string) (base string, ok bool) {
	matches := splitGGUFPattern.FindStringSubmatch(strings.TrimSpace(id))
	if len(matches) != 4 {
		return "", false
	}
	return matches[1], true
}

// IsMMProjFilename reports whether the filename looks like a companion mmproj file.
func IsMMProjFilename(filename string) bool {
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	return strings.Contains(stem, "mmproj")
}

// CompanionModelIDFromFilename derives the main model ID that an mmproj file belongs to.
func CompanionModelIDFromFilename(filename string) string {
	stem := strings.TrimSpace(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	if stem == "" {
		return ""
	}
	lower := strings.ToLower(stem)
	if !strings.Contains(lower, "mmproj") {
		return ModelIDFromFilename(filename)
	}

	idx := strings.Index(lower, "mmproj")
	switch {
	case idx <= 1:
		stem = stem[idx+len("mmproj"):]
	default:
		stem = stem[:idx]
	}
	stem = strings.Trim(stem, "-_. ")
	if stem == "" {
		return ""
	}
	if base, ok := splitModelID(stem); ok {
		return base
	}
	return stem
}

// ModelIDFromFilename resolves the grouped model ID for a GGUF filename.
func ModelIDFromFilename(filename string) string {
	if IsMMProjFilename(filename) {
		return CompanionModelIDFromFilename(filename)
	}
	stem := strings.TrimSpace(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	if stem == "" {
		return ""
	}
	if base, ok := splitModelID(stem); ok {
		return base
	}
	return stem
}

// InferCapabilities returns best-effort capability badges for UI display.
func InferCapabilities(modelID, name, description string, hasVision bool) Capabilities {
	text := strings.ToLower(strings.Join([]string{modelID, name, description}, " "))
	caps := Capabilities{Vision: hasVision}

	switch {
	case hasAny(text, "qwen3-coder-next", "qwen3 coder next"):
		caps.Tools = true
		caps.Coding = true
	case hasAny(text, "qwen3.5", "qwen 3.5", "reasoning-distilled", "reasoning distilled"):
		caps.Reasoning = true
	case hasAny(text, "qwen3", "qwen 3"):
		caps.Tools = true
		caps.Reasoning = true
	case hasAny(text, "gemma 4", "gemma-4"):
		caps.Tools = true
		caps.Reasoning = true
		caps.Coding = true
		if hasAny(text, "e2b", "e4b") {
			caps.Audio = true
			caps.Video = true
		}
	case hasAny(text, "gemma 3", "gemma-3"):
		caps.Tools = true
		caps.Reasoning = true
	case hasAny(text, "glm-5", "glm 5", "glm-4.7-flash", "glm 4.7 flash"):
		caps.Tools = true
		caps.Reasoning = true
		caps.Coding = true
	case hasAny(text, "minimax-m2.5", "minimax m2.5"):
		caps.Tools = true
		caps.Reasoning = true
		caps.Coding = true
	case hasAny(text, "gpt-oss"):
		caps.Tools = true
		caps.Reasoning = true
		caps.Coding = true
	case hasAny(text, "phi-4", "phi4"):
		caps.Tools = true
		caps.Reasoning = true
		caps.Coding = true
	case hasAny(text, "mistral-small", "mistral small"):
		caps.Tools = true
		caps.Coding = true
	case hasAny(text, "deepseek-r1", "deepseek r1"):
		caps.Reasoning = true
	}

	return caps
}

func hasAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// runtimeCaps holds the dynamically configured runtime capabilities.
// Updated via SetRuntimeCapabilities when the llama.cpp library is loaded.
var (
	runtimeCapsMu sync.RWMutex
	runtimeCaps   = Capabilities{
		Vision:    true,
		Tools:     true,
		Reasoning: true,
		Coding:    true,
	}
)

// SetRuntimeCapabilities updates the runtime capability set based on the
// actual features available in the loaded llama.cpp library.
// Call this after the library is initialised (e.g. after checking IsMtmdAvailable).
func SetRuntimeCapabilities(caps Capabilities) {
	runtimeCapsMu.Lock()
	runtimeCaps = caps
	runtimeCapsMu.Unlock()
}

// RuntimeSupportedCapabilities returns the subset of capability badges that
// the current local runtime can actually serve end-to-end today.
func RuntimeSupportedCapabilities() Capabilities {
	runtimeCapsMu.RLock()
	c := runtimeCaps
	runtimeCapsMu.RUnlock()
	return c
}

// IntersectCapabilities keeps only capabilities supported by both sides.
func IntersectCapabilities(a, b Capabilities) Capabilities {
	return Capabilities{
		Vision:    a.Vision && b.Vision,
		Audio:     a.Audio && b.Audio,
		Video:     a.Video && b.Video,
		Tools:     a.Tools && b.Tools,
		Reasoning: a.Reasoning && b.Reasoning,
		Coding:    a.Coding && b.Coding,
	}
}

// ModelID returns the grouped model ID for the preset's primary model file.
func (p Preset) ModelID() string {
	files := p.DownloadFiles()
	for _, file := range files {
		if !IsMMProjFilename(file.Filename) {
			return ModelIDFromFilename(file.Filename)
		}
	}
	if p.Filename != "" {
		return ModelIDFromFilename(p.Filename)
	}
	return strings.TrimSpace(p.ID)
}

// CapabilitiesResolved returns capability badges for the preset.
// If the preset has explicit capabilities defined in JSON, those are used;
// otherwise it falls back to name-based inference. The result is then
// intersected with the runtime's supported capabilities.
func (p Preset) CapabilitiesResolved() Capabilities {
	var declared Capabilities
	if p.Capabilities != nil {
		declared = *p.Capabilities
	} else {
		description := ""
		if p.Description != nil {
			description = strings.TrimSpace(strings.Join([]string{p.Description.Zh, p.Description.En}, " "))
		}
		hasVision := false
		for _, file := range p.DownloadFiles() {
			if IsMMProjFilename(file.Filename) {
				hasVision = true
				break
			}
		}
		declared = InferCapabilities(p.ModelID(), p.Name, description, hasVision)
	}
	return IntersectCapabilities(declared, RuntimeSupportedCapabilities())
}

// LookupCatalogCapabilities returns the resolved capabilities from the builtin
// catalog for the given model ID, if a matching preset exists. The second
// return value is false when no catalog entry matches.
func LookupCatalogCapabilities(modelID string) (Capabilities, bool) {
	for _, entry := range BuiltinCatalog {
		if entry.Preset.ModelID() == modelID {
			return entry.Preset.CapabilitiesResolved(), true
		}
	}
	return Capabilities{}, false
}
