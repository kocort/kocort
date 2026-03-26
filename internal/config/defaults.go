package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ResolveDefaultConfigPath resolves the default path for the main kocort.json config.
func ResolveDefaultConfigPath() string {
	if override := strings.TrimSpace(os.Getenv("KOCORT_CONFIG_PATH")); override != "" {
		return override
	}
	return filepath.Join(ResolveDefaultConfigDir(), "kocort.json")
}

// ResolveDefaultModelsConfigPath resolves the default path for models.json.
func ResolveDefaultModelsConfigPath() string {
	if override := strings.TrimSpace(os.Getenv("KOCORT_MODELS_CONFIG_PATH")); override != "" {
		return override
	}
	return filepath.Join(ResolveDefaultConfigDir(), "models.json")
}

// ResolveDefaultChannelsConfigPath resolves the default path for channels.json.
func ResolveDefaultChannelsConfigPath() string {
	if override := strings.TrimSpace(os.Getenv("KOCORT_CHANNELS_CONFIG_PATH")); override != "" {
		return override
	}
	return filepath.Join(ResolveDefaultConfigDir(), "channels.json")
}

// LoadDefaultAppConfig parses the given embedded default JSON into an AppConfig.
func LoadDefaultAppConfig(embeddedJSON []byte) (AppConfig, error) {
	var cfg AppConfig
	if err := json.Unmarshal(embeddedJSON, &cfg); err != nil {
		return AppConfig{}, err
	}
	return cfg, nil
}

// LoadRuntimeConfig loads config by merging embedded defaults with optional overlay files.
func LoadRuntimeConfig(embeddedJSON []byte, opts ConfigLoadOptions) (AppConfig, error) {
	merged, err := ParseJSONConfigMap(embeddedJSON)
	if err != nil {
		return AppConfig{}, err
	}
	configPath := strings.TrimSpace(opts.ConfigPath)
	modelsPath := strings.TrimSpace(opts.ModelsConfigPath)
	channelsPath := strings.TrimSpace(opts.ChannelsConfigPath)
	configDir := strings.TrimSpace(opts.ConfigDir)

	configRequired := configPath != ""
	modelsRequired := modelsPath != ""
	channelsRequired := channelsPath != ""

	if configDir != "" {
		if configPath == "" {
			configPath = filepath.Join(configDir, "kocort.json")
		}
		if modelsPath == "" {
			modelsPath = filepath.Join(configDir, "models.json")
		}
		if channelsPath == "" {
			channelsPath = filepath.Join(configDir, "channels.json")
		}
	}

	if configPath == "" {
		configPath = ResolveDefaultConfigPath()
	}
	if modelsPath == "" {
		modelsPath = ResolveDefaultModelsConfigPath()
	}
	if channelsPath == "" {
		channelsPath = ResolveDefaultChannelsConfigPath()
	}

	for _, source := range []struct {
		path     string
		required bool
	}{
		{path: configPath, required: configRequired},
		{path: modelsPath, required: modelsRequired},
		{path: channelsPath, required: channelsRequired},
	} {
		next, ok, err := LoadOptionalConfigMap(source.path, source.required)
		if err != nil {
			return AppConfig{}, err
		}
		if !ok {
			continue
		}
		merged = DeepMergeJSONMaps(merged, next)
	}

	payload, err := json.Marshal(merged)
	if err != nil {
		return AppConfig{}, err
	}
	var cfg AppConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		slog.Warn("config unmarshal had errors, using partial/default config", "error", err)
		// Try to load just the embedded defaults so the runtime can still start.
		var fallback AppConfig
		_ = json.Unmarshal(embeddedJSON, &fallback)
		return fallback, nil
	}
	return cfg, nil
}
