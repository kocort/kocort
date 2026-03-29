package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RuntimeConfigStore persists config sections to their respective JSON files.
type RuntimeConfigStore struct {
	mu        sync.Mutex
	main      string
	models    string
	chans     string
	configDir string // base directory for resolving relative paths
}

// NewRuntimeConfigStore creates a store using resolved config load options.
func NewRuntimeConfigStore(opts ConfigLoadOptions) *RuntimeConfigStore {
	resolved := ResolveEffectiveConfigLoadOptions(opts)
	return &RuntimeConfigStore{
		main:      strings.TrimSpace(resolved.ConfigPath),
		models:    strings.TrimSpace(resolved.ModelsConfigPath),
		chans:     strings.TrimSpace(resolved.ChannelsConfigPath),
		configDir: strings.TrimSpace(resolved.ConfigDir),
	}
}

// ConfigDir returns the base config directory used for path resolution.
func (s *RuntimeConfigStore) ConfigDir() string {
	if s == nil {
		return ""
	}
	return s.configDir
}

// LoadFresh reloads the full config from disk by merging embedded defaults
// with the on-disk config files (main, models, channels). The returned
// AppConfig reflects the latest persisted state with paths UNRESOLVED
// (relative, as written in JSON). Use LoadFreshResolved to get absolute paths.
func (s *RuntimeConfigStore) LoadFresh() (AppConfig, error) {
	if s == nil {
		return AppConfig{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return LoadRuntimeConfig(DefaultConfigJSON(), ConfigLoadOptions{
		ConfigPath:         s.main,
		ModelsConfigPath:   s.models,
		ChannelsConfigPath: s.chans,
	})
}

// LoadFreshResolved reloads config from disk and resolves all relative paths
// to absolute using the stored configDir. Use this when the config will be
// applied to a running runtime (e.g. after ModifyAndPersist).
func (s *RuntimeConfigStore) LoadFreshResolved() (AppConfig, error) {
	cfg, err := s.LoadFresh()
	if err != nil {
		return cfg, err
	}
	ResolveConfigPaths(&cfg, s.configDir)
	return cfg, nil
}

// SaveSections persists changed config sections to their respective files.
// The config is automatically unreduced to relative paths before writing,
// so callers can pass configs with either absolute or relative paths.
func (s *RuntimeConfigStore) SaveSections(cfg AppConfig, mainChanged bool, modelsChanged bool, channelsChanged bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Convert absolute paths back to relative before persisting.
	toSave := cfg
	UnresolveConfigPaths(&toSave, s.configDir)
	if mainChanged {
		if err := WriteJSONFile(s.main, BuildMainConfigPayload(toSave)); err != nil {
			return err
		}
	}
	if modelsChanged {
		if err := WriteJSONFile(s.models, map[string]any{"models": toSave.Models}); err != nil {
			return err
		}
	}
	if channelsChanged {
		if err := WriteJSONFile(s.chans, map[string]any{"channels": toSave.Channels}); err != nil {
			return err
		}
	}
	return nil
}

// WriteJSONFile writes a JSON payload to the given path, creating directories as needed.
func WriteJSONFile(path string, payload any) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(trimmed), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(trimmed, raw, 0o644)
}

// BuildMainConfigPayload builds the map payload for the main config file,
// omitting zero-valued sections.
func BuildMainConfigPayload(cfg AppConfig) map[string]any {
	payload := map[string]any{}
	if cfg.StateDir != "" {
		payload["stateDir"] = cfg.StateDir
	}
	if !IsZeroJSON(cfg.Tools) {
		payload["tools"] = cfg.Tools
	}
	if !IsZeroJSON(cfg.Plugins) {
		payload["plugins"] = cfg.Plugins
	}
	if !IsZeroJSON(cfg.Skills) {
		payload["skills"] = cfg.Skills
	}
	if !IsZeroJSON(cfg.Agents) {
		payload["agents"] = cfg.Agents
	}
	if !IsZeroJSON(cfg.Session) {
		payload["session"] = cfg.Session
	}
	if !IsZeroJSON(cfg.ACP) {
		payload["acp"] = cfg.ACP
	}
	if !IsZeroJSON(cfg.Gateway) {
		payload["gateway"] = cfg.Gateway
	}
	if !IsZeroJSON(cfg.Memory) {
		payload["memory"] = cfg.Memory
	}
	if !IsZeroJSON(cfg.Data) {
		payload["data"] = cfg.Data
	}
	if !IsZeroJSON(cfg.Env) {
		payload["environment"] = cfg.Env
	}
	if !IsZeroJSON(cfg.Tasks) {
		payload["tasks"] = cfg.Tasks
	}
	if cfg.BrainMode != "" {
		payload["brainMode"] = cfg.BrainMode
	}
	if !IsZeroJSON(cfg.BrainLocal) {
		payload["brainLocal"] = cfg.BrainLocal
	}
	if !IsZeroJSON(cfg.Cerebellum) {
		payload["cerebellum"] = cfg.Cerebellum
	}
	if !IsZeroJSON(cfg.Network) {
		payload["network"] = cfg.Network
	}
	return payload
}

// ConfigHash returns a SHA-256 hex digest of the JSON-serialized configuration.
func ConfigHash(cfg AppConfig) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
