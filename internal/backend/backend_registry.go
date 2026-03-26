package backend

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
)

// BackendRegistry caches and resolves backends by provider name.
type BackendRegistry struct {
	cfg    config.AppConfig
	env    *infra.EnvironmentRuntime
	dc     *infra.DynamicHTTPClient
	mu     sync.Mutex
	cached map[string]rtypes.Backend
}

// NewBackendRegistry creates a new BackendRegistry for the given configuration and environment.
func NewBackendRegistry(cfg config.AppConfig, env *infra.EnvironmentRuntime, dc *infra.DynamicHTTPClient) *BackendRegistry {
	return &BackendRegistry{
		cfg:    cfg,
		env:    env,
		dc:     dc,
		cached: map[string]rtypes.Backend{},
	}
}

// Resolve returns a cached or newly created backend for the given provider.
func (r *BackendRegistry) Resolve(provider string) (rtypes.Backend, string, error) {
	if r == nil {
		return nil, "", fmt.Errorf("backend registry is not configured")
	}
	normalized := NormalizeProviderID(provider)
	if normalized == "" {
		return nil, "", fmt.Errorf("provider is required")
	}
	r.mu.Lock()
	if b, ok := r.cached[normalized]; ok {
		r.mu.Unlock()
		return b, BackendKindForProvider(r.cfg, normalized), nil
	}
	r.mu.Unlock()

	providerCfg, _, err := config.ResolveConfiguredProvider(r.cfg, normalized)
	if err != nil {
		return nil, "", err
	}
	api := strings.TrimSpace(providerCfg.API)
	// Build auth profile store when alternate API keys are configured.
	var authProfiles *AuthProfileStore
	if len(providerCfg.AlternateAPIKeys) > 0 && strings.TrimSpace(providerCfg.APIKey) != "" {
		authProfiles = NewAuthProfileStore()
		authProfiles.Register(AuthProfile{
			ID:          normalized + "-primary",
			Provider:    normalized,
			Type:        AuthProfileTypeAPIKey,
			APIKey:      providerCfg.APIKey,
			Label:       "primary",
			ConfigIndex: 0,
		})
		for i, altKey := range providerCfg.AlternateAPIKeys {
			if strings.TrimSpace(altKey) == "" {
				continue
			}
			authProfiles.Register(AuthProfile{
				ID:          fmt.Sprintf("%s-alt-%d", normalized, i+1),
				Provider:    normalized,
				Type:        AuthProfileTypeAPIKey,
				APIKey:      altKey,
				Label:       fmt.Sprintf("alternate-%d", i+1),
				ConfigIndex: i + 1,
			})
		}
	}

	var b rtypes.Backend
	switch api {
	case "", "openai-completions":
		eb := NewEmbeddedBackend(r.cfg, r.env, r.dc)
		if authProfiles != nil {
			eb.OpenAI.AuthProfiles = authProfiles
		}
		b = eb
	case "anthropic-messages":
		ab := NewAnthropicCompatBackend(r.cfg, r.env, r.dc)
		if authProfiles != nil {
			ab.AuthProfiles = authProfiles
		}
		b = ab
	case "command":
		if providerCfg.Command == nil {
			return nil, "", fmt.Errorf("provider %q is missing command backend config", normalized)
		}
		b = &CommandBackend{Config: *providerCfg.Command, Env: r.env}
	case "cli":
		if providerCfg.Command == nil {
			return nil, "", fmt.Errorf("provider %q is missing command backend config", normalized)
		}
		b = &CLIBackend{
			Config:   r.cfg,
			Env:      r.env,
			Provider: normalized,
			Command:  *providerCfg.Command,
		}
	case "acp":
		if providerCfg.Command == nil {
			return nil, "", fmt.Errorf("provider %q is missing ACP backend config", normalized)
		}
		var manager *acp.AcpSessionManager
		if ttl := r.cfg.ACP.Runtime.TTLMinutes; ttl > 0 {
			manager = acp.NewAcpSessionManagerWithTTL(time.Duration(ttl) * time.Minute)
		} else {
			manager = acp.NewAcpSessionManager()
		}
		b = &ACPBackend{
			Config:   r.cfg,
			Env:      r.env,
			Provider: normalized,
			Command:  *providerCfg.Command,
			Mgr:      manager,
		}
	default:
		return nil, "", fmt.Errorf("provider %q uses unsupported api %q", normalized, api)
	}
	r.mu.Lock()
	r.cached[normalized] = b
	r.mu.Unlock()
	return b, BackendKindForProvider(r.cfg, normalized), nil
}

// BackendKindForProvider returns the backend kind string for the given provider.
func BackendKindForProvider(cfg config.AppConfig, provider string) string {
	providerCfg, _, err := config.ResolveConfiguredProvider(cfg, provider)
	if err != nil {
		return ""
	}
	api := strings.TrimSpace(providerCfg.API)
	switch api {
	case "cli":
		return "cli"
	case "acp":
		return "acp"
	case "command":
		return "command"
	default:
		return "embedded"
	}
}
