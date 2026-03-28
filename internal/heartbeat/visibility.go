package heartbeat

import (
	"strings"

	"github.com/kocort/kocort/internal/config"
)

type Visibility struct {
	ShowOK       bool
	ShowAlerts   bool
	UseIndicator bool
}

var defaultVisibility = Visibility{
	ShowOK:       false,
	ShowAlerts:   true,
	UseIndicator: true,
}

func ResolveVisibility(cfg config.AppConfig, channelID, accountID string) Visibility {
	channelID = strings.TrimSpace(strings.ToLower(channelID))
	if channelID == "" {
		return defaultVisibility
	}

	channelDefaults := cfg.Channels.Defaults
	if channelID == "webchat" {
		out := defaultVisibility
		if channelDefaults != nil && channelDefaults.Heartbeat != nil {
			out = mergeVisibility(out, channelDefaults.Heartbeat)
		}
		return out
	}

	var channelCfg *config.ChannelConfig
	if entry, ok := cfg.Channels.Entries[channelID]; ok {
		copied := entry
		channelCfg = &copied
	}

	out := defaultVisibility
	if channelDefaults != nil && channelDefaults.Heartbeat != nil {
		out = mergeVisibility(out, channelDefaults.Heartbeat)
	}
	if channelCfg != nil && channelCfg.Heartbeat != nil {
		out = mergeVisibility(out, channelCfg.Heartbeat)
	}
	if channelCfg != nil && strings.TrimSpace(accountID) != "" {
		if accountVis := resolveAccountHeartbeatVisibility(channelCfg.Accounts, accountID); accountVis != nil {
			out = mergeVisibility(out, accountVis)
		}
	}
	return out
}

func mergeVisibility(current Visibility, override *config.ChannelHeartbeatVisibilityConfig) Visibility {
	if override == nil {
		return current
	}
	if override.ShowOK != nil {
		current.ShowOK = *override.ShowOK
	}
	if override.ShowAlerts != nil {
		current.ShowAlerts = *override.ShowAlerts
	}
	if override.UseIndicator != nil {
		current.UseIndicator = *override.UseIndicator
	}
	return current
}

func resolveAccountHeartbeatVisibility(accounts map[string]any, accountID string) *config.ChannelHeartbeatVisibilityConfig {
	raw, ok := accounts[strings.TrimSpace(accountID)]
	if !ok {
		return nil
	}
	entry, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	rawHeartbeat, ok := entry["heartbeat"].(map[string]any)
	if !ok {
		return nil
	}
	result := &config.ChannelHeartbeatVisibilityConfig{}
	if value, ok := readBool(rawHeartbeat["showOk"]); ok {
		result.ShowOK = &value
	}
	if value, ok := readBool(rawHeartbeat["showAlerts"]); ok {
		result.ShowAlerts = &value
	}
	if value, ok := readBool(rawHeartbeat["useIndicator"]); ok {
		result.UseIndicator = &value
	}
	if result.ShowOK == nil && result.ShowAlerts == nil && result.UseIndicator == nil {
		return nil
	}
	return result
}

func readBool(raw any) (bool, bool) {
	value, ok := raw.(bool)
	return value, ok
}
