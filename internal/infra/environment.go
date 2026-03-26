package infra

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/config"
)

var environmentTemplatePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type EnvironmentRuntime struct {
	cfg config.EnvironmentConfig
	mu  sync.RWMutex
}

func NewEnvironmentRuntime(cfg config.EnvironmentConfig) *EnvironmentRuntime {
	return &EnvironmentRuntime{cfg: cfg}
}

func (r *EnvironmentRuntime) Reload(cfg config.EnvironmentConfig) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
}

func (r *EnvironmentRuntime) Resolve(key string) (string, bool) {
	return r.resolve(key, map[string]bool{})
}

func (r *EnvironmentRuntime) resolve(key string, stack map[string]bool) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	if stack[key] {
		return "", false
	}
	stack[key] = true
	defer delete(stack, key)
	r.mu.RLock()
	cfg := r.cfg
	r.mu.RUnlock()
	if cfg.Entries != nil {
		if entry, ok := cfg.Entries[key]; ok {
			if raw := strings.TrimSpace(entry.Value); raw != "" {
				resolved, err := r.resolveTemplates(raw, stack)
				if err == nil {
					return resolved, true
				}
			}
			if source := strings.TrimSpace(entry.FromEnv); source != "" {
				if value := strings.TrimSpace(os.Getenv(source)); value != "" {
					return value, true
				}
			}
		}
	}
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value, true
	}
	return "", false
}

func (r *EnvironmentRuntime) ResolveString(raw string) (string, error) {
	return r.resolveTemplates(raw, map[string]bool{})
}

func (r *EnvironmentRuntime) resolveTemplates(raw string, stack map[string]bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	matches := environmentTemplatePattern.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return raw, nil
	}
	var unresolved []string
	resolved := environmentTemplatePattern.ReplaceAllStringFunc(raw, func(token string) string {
		match := environmentTemplatePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		value, ok := r.resolve(match[1], stack)
		if !ok {
			unresolved = append(unresolved, match[1])
			return token
		}
		return value
	})
	if len(unresolved) > 0 && r.Strict() {
		return "", fmt.Errorf("missing environment values: %s", strings.Join(UniqueStrings(unresolved), ", "))
	}
	return resolved, nil
}

func (r *EnvironmentRuntime) Strict() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.Strict != nil && *r.cfg.Strict
}

func (r *EnvironmentRuntime) ResolveMap(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		resolved, err := r.ResolveString(value)
		if err != nil {
			return nil, err
		}
		out[key] = resolved
	}
	return out, nil
}

func (r *EnvironmentRuntime) Snapshot(masked bool) map[string]string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	cfg := r.cfg
	r.mu.RUnlock()
	if len(cfg.Entries) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cfg.Entries))
	for key := range cfg.Entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		value, _ := r.Resolve(key) // zero value fallback is intentional
		if masked && r.IsMasked(key) && strings.TrimSpace(value) != "" {
			out[key] = "********"
			continue
		}
		out[key] = value
	}
	return out
}

func (r *EnvironmentRuntime) IsMasked(key string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.cfg.Entries[strings.TrimSpace(key)]
	if !ok {
		return false
	}
	if entry.Masked != nil {
		return *entry.Masked
	}
	return entry.Required != nil && *entry.Required
}

func (r *EnvironmentRuntime) MaskString(text string) string {
	if r == nil || strings.TrimSpace(text) == "" {
		return text
	}
	snapshot := r.Snapshot(false)
	if len(snapshot) == 0 {
		return text
	}
	for key, value := range snapshot {
		if !r.IsMasked(key) || strings.TrimSpace(value) == "" {
			continue
		}
		text = strings.ReplaceAll(text, value, "********")
	}
	return text
}

func (r *EnvironmentRuntime) AppendToEnv(env []string, overrides map[string]string) ([]string, error) {
	merged := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		merged[key] = value
	}
	resolvedOverrides, err := r.ResolveMap(overrides)
	if err != nil {
		return nil, err
	}
	for key, value := range resolvedOverrides {
		merged[key] = value
	}
	if r != nil {
		for key, value := range r.Snapshot(false) {
			if _, exists := merged[key]; !exists && strings.TrimSpace(value) != "" {
				merged[key] = value
			}
		}
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+merged[key])
	}
	return out, nil
}

func UniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
