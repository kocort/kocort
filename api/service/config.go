package service

// Config helper functions.

import (
	"encoding/json"
	"fmt"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/runtime"
)

// CloneConfig creates a deep copy of the config.
func CloneConfig(cfg config.AppConfig) config.AppConfig {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg
	}
	var cloned config.AppConfig
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return cfg
	}
	return cloned
}

// ConfigSections indicates which config files were changed by a mutator.
type ConfigSections struct {
	Main     bool
	Models   bool
	Channels bool
}

// ConfigMutator modifies a freshly-loaded config in place and returns which
// sections were changed.
type ConfigMutator func(cfg *config.AppConfig) (ConfigSections, error)

// ModifyAndPersist implements a safe read-modify-write cycle:
//  1. Load the latest config from disk (via ConfigStore.LoadFresh)
//  2. Call the mutator to apply targeted changes
//  3. Persist only the changed sections back to disk (paths are automatically
//     unresolved to relative by the store)
//  4. Resolve relative paths to absolute and apply the full config to the runtime
//
// This prevents stale in-memory config from overwriting concurrent changes.
func ModifyAndPersist(rt *runtime.Runtime, mutator ConfigMutator) error {
	if rt == nil {
		return fmt.Errorf("runtime is not configured")
	}
	cfg, err := loadFreshOrFallback(rt)
	if err != nil {
		return err
	}
	sections, err := mutator(&cfg)
	if err != nil {
		return err
	}
	if rt.ConfigStore != nil {
		if err := rt.ConfigStore.SaveSections(cfg, sections.Main, sections.Models, sections.Channels); err != nil {
			return err
		}
		// Resolve relative paths to absolute before applying to runtime.
		config.ResolveConfigPaths(&cfg, rt.ConfigStore.ConfigDir())
	}
	return rt.ApplyConfig(cfg)
}

// loadFreshOrFallback attempts to load fresh config from disk; falls back to
// cloning the runtime's in-memory config when no ConfigStore is available or
// when the config files do not yet exist on disk (e.g. first run, tests).
func loadFreshOrFallback(rt *runtime.Runtime) (config.AppConfig, error) {
	if rt.ConfigStore != nil {
		fresh, err := rt.ConfigStore.LoadFresh()
		if err == nil {
			return fresh, nil
		}
		// Files may not exist yet (first run / tests); fall back to in-memory clone.
	}
	return CloneConfig(rt.Config), nil
}

// ApplyAndPersistConfig applies config changes and persists them.
// Deprecated: prefer ModifyAndPersist for new code.
func ApplyAndPersistConfig(rt *runtime.Runtime, cfg config.AppConfig, mainChanged bool, modelsChanged bool, channelsChanged bool) error {
	if rt == nil {
		return fmt.Errorf("runtime is not configured")
	}
	if rt.ConfigStore != nil {
		if err := rt.ConfigStore.SaveSections(cfg, mainChanged, modelsChanged, channelsChanged); err != nil {
			return err
		}
	}
	return rt.ApplyConfig(cfg)
}
