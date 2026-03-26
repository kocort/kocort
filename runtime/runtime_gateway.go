package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/kocort/kocort/internal/config"
)

func (r *Runtime) GatewayConfigSnapshot() (config.AppConfig, string, error) {
	hash, err := gatewayConfigHash(r.Config)
	return r.Config, hash, err
}

func (r *Runtime) GatewayApplyConfig(_ context.Context, cfg config.AppConfig) error {
	return r.ApplyConfig(cfg)
}

func (r *Runtime) GatewayPersistConfig(_ context.Context, mainChanged bool, modelsChanged bool, channelsChanged bool) error {
	return r.PersistConfig(mainChanged, modelsChanged, channelsChanged)
}

func gatewayConfigHash(cfg config.AppConfig) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
