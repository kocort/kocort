package runtime

import (
	"context"

	"github.com/kocort/kocort/internal/config"
)

func (r *Runtime) GatewayConfigSnapshot() (config.AppConfig, string, error) {
	hash, err := config.ConfigHash(r.Config)
	return r.Config, hash, err
}

func (r *Runtime) GatewayApplyConfig(_ context.Context, cfg config.AppConfig) error {
	return r.ApplyConfig(cfg)
}

func (r *Runtime) GatewayPersistConfig(_ context.Context, mainChanged bool, modelsChanged bool, channelsChanged bool) error {
	return r.PersistConfig(mainChanged, modelsChanged, channelsChanged)
}
