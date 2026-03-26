package adapter

import (
	"strings"
	"sync"
)

// =========================================================================
// Global Driver Registry — adapters register factories via Register
// =========================================================================

// AdapterFactory creates a new zero-value ChannelAdapter instance.
type AdapterFactory func() ChannelAdapter

var (
	registryMu sync.RWMutex
	registry   = map[string]AdapterFactory{}
	schemas    = []ChannelDriverSchema{}
)

// Register adds an adapter factory for the given driver ID.
// Channel drivers should call this from their init() function.
func Register(driverID string, factory AdapterFactory) {
	driverID = strings.ToLower(strings.TrimSpace(driverID))
	if driverID == "" || factory == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[driverID] = factory

}

// AllDriverSchemas returns all registered channel driver schemas.
func AllDriverSchemas() []ChannelDriverSchema {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]ChannelDriverSchema, 0, len(registry))
	for _, s := range registry {
		out = append(out, s().Schema())
	}
	return out
}

// CreateAdapter looks up the registered factory for driverID and returns
// a new adapter instance, or nil if no factory is registered.
func CreateAdapter(driverID string) ChannelAdapter {
	driverID = strings.ToLower(strings.TrimSpace(driverID))
	registryMu.RLock()
	f := registry[driverID]
	registryMu.RUnlock()
	if f != nil {
		return f()
	}
	return nil
}

// ResolveDriverID determines which driver should handle channelID by
// inspecting the config map for explicit "driver" or "type" keys.
// Falls back to the normalized channelID itself.
func ResolveDriverID(channelID string, cfg ChannelConfig) string {
	if len(cfg.Config) > 0 {
		if raw, ok := cfg.Config["driver"].(string); ok {
			if d := strings.TrimSpace(raw); d != "" {
				return strings.ToLower(d)
			}
		}
		if raw, ok := cfg.Config["type"].(string); ok {
			if d := strings.TrimSpace(raw); d != "" {
				return strings.ToLower(d)
			}
		}
	}
	return NormalizeID(channelID)
}

// =========================================================================
// Integration — bundles an adapter for registration with ChannelManager
// =========================================================================

// Integration bundles an adapter for registration with the channel manager.
// For adapters implementing ChannelAdapter, set Transport and Outbound
// to the same instance.
type Integration struct {
	ID           string
	Transport    any // primary adapter; used for HTTP ingress + background lifecycle
	Outbound     any // outbound delivery adapter (usually same as Transport)
	Capabilities any // deprecated: ignored — capabilities are read via duck-typing
}

// ResolveAdapter returns the first non-nil field among Transport, Outbound,
// Capabilities. This is the value stored by ChannelManager.RegisterIntegration.
func (ci Integration) ResolveAdapter() any {
	if ci.Transport != nil {
		return ci.Transport
	}
	if ci.Outbound != nil {
		return ci.Outbound
	}
	return ci.Capabilities
}

// BuildIntegration creates an Integration by resolving the driver from
// cfg and looking up the registered factory.
// Falls back to "generic" when no matching driver is registered.
func BuildChannel(channelID string, cfg ChannelConfig) ChannelAdapter {
	driver := ResolveDriverID(channelID, cfg)

	if a := CreateAdapter(driver); a != nil {
		return a
	}
	if a := CreateAdapter("generic"); a != nil {
		return a
	}
	return CreateAdapter(driver)
}

// NormalizeID lowercases and trims a channel/driver identifier.
func NormalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}
