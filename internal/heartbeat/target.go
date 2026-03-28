package heartbeat

import (
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

type DeliveryTargetPlan struct {
	Enabled   bool
	Channel   string
	To        string
	AccountID string
	ThreadID  string
	Reason    string
}

func ResolveDeliveryTarget(cfg config.AppConfig, identity core.AgentIdentity, entry *core.SessionEntry) DeliveryTargetPlan {
	target := strings.TrimSpace(strings.ToLower(identity.HeartbeatTarget))
	switch target {
	case "", "none":
		return DeliveryTargetPlan{Enabled: false, Reason: "target-none"}
	case "last":
		resolved := resolveSessionDeliveryTarget(entry)
		if !resolved.Enabled {
			return resolved
		}
		if strings.EqualFold(strings.TrimSpace(identity.HeartbeatDirectPolicy), "block") && isDirectHeartbeatTarget(entry, resolved) {
			return DeliveryTargetPlan{Enabled: false, Reason: "dm-blocked"}
		}
		return resolved
	default:
		plan := DeliveryTargetPlan{
			Enabled:   true,
			Channel:   target,
			To:        strings.TrimSpace(identity.HeartbeatTo),
			AccountID: strings.TrimSpace(identity.HeartbeatAccountID),
		}
		if strings.EqualFold(plan.Channel, "last") {
			return resolveSessionDeliveryTarget(entry)
		}
		if plan.To == "" {
			if entry != nil && entry.DeliveryContext != nil && strings.EqualFold(entry.DeliveryContext.Channel, plan.Channel) {
				plan.To = strings.TrimSpace(entry.DeliveryContext.To)
				if plan.AccountID == "" {
					plan.AccountID = strings.TrimSpace(entry.DeliveryContext.AccountID)
				}
				plan.ThreadID = strings.TrimSpace(entry.DeliveryContext.ThreadID)
			} else if entry != nil && strings.EqualFold(entry.LastChannel, plan.Channel) {
				plan.To = strings.TrimSpace(entry.LastTo)
				if plan.AccountID == "" {
					plan.AccountID = strings.TrimSpace(entry.LastAccountID)
				}
				plan.ThreadID = strings.TrimSpace(entry.LastThreadID)
			}
		}
		if plan.AccountID == "" {
			if channelCfg, ok := cfg.Channels.Entries[plan.Channel]; ok {
				plan.AccountID = strings.TrimSpace(channelCfg.DefaultAccount)
			}
		} else if channelCfg, ok := cfg.Channels.Entries[plan.Channel]; ok && len(channelCfg.Accounts) > 0 {
			if _, exists := channelCfg.Accounts[plan.AccountID]; !exists {
				return DeliveryTargetPlan{Enabled: false, Reason: "unknown-account"}
			}
		}
		if plan.To == "" {
			if channelCfg, ok := cfg.Channels.Entries[plan.Channel]; ok {
				plan.To = strings.TrimSpace(channelCfg.DefaultTo)
			}
		}
		if strings.EqualFold(plan.Channel, "telegram") && strings.Contains(plan.To, ":topic:") {
			before, after, ok := strings.Cut(plan.To, ":topic:")
			if ok {
				plan.To = strings.TrimSpace(before)
				plan.ThreadID = strings.TrimSpace(after)
			}
		}
		if strings.EqualFold(strings.TrimSpace(identity.HeartbeatDirectPolicy), "block") && isDirectRoute(plan, entry) {
			return DeliveryTargetPlan{Enabled: false, Reason: "dm-blocked"}
		}
		if strings.TrimSpace(plan.Channel) == "" || strings.TrimSpace(plan.To) == "" {
			return DeliveryTargetPlan{Enabled: false, Reason: "no-target"}
		}
		return plan
	}
}

func resolveSessionDeliveryTarget(entry *core.SessionEntry) DeliveryTargetPlan {
	if entry == nil {
		return DeliveryTargetPlan{Enabled: false, Reason: "no-target"}
	}
	if entry.DeliveryContext != nil && strings.TrimSpace(entry.DeliveryContext.Channel) != "" && strings.TrimSpace(entry.DeliveryContext.To) != "" {
		return DeliveryTargetPlan{
			Enabled:   true,
			Channel:   strings.TrimSpace(entry.DeliveryContext.Channel),
			To:        strings.TrimSpace(entry.DeliveryContext.To),
			AccountID: strings.TrimSpace(entry.DeliveryContext.AccountID),
			ThreadID:  strings.TrimSpace(entry.DeliveryContext.ThreadID),
		}
	}
	if strings.TrimSpace(entry.LastChannel) == "" || strings.TrimSpace(entry.LastTo) == "" {
		return DeliveryTargetPlan{Enabled: false, Reason: "no-target"}
	}
	return DeliveryTargetPlan{
		Enabled:   true,
		Channel:   strings.TrimSpace(entry.LastChannel),
		To:        strings.TrimSpace(entry.LastTo),
		AccountID: strings.TrimSpace(entry.LastAccountID),
		ThreadID:  strings.TrimSpace(entry.LastThreadID),
	}
}

func isDirectHeartbeatTarget(entry *core.SessionEntry, plan DeliveryTargetPlan) bool {
	if entry != nil && entry.LastChatType != "" {
		return entry.LastChatType == core.ChatTypeDirect
	}
	return isDirectRoute(plan, entry)
}

func isDirectRoute(plan DeliveryTargetPlan, entry *core.SessionEntry) bool {
	to := strings.ToLower(strings.TrimSpace(plan.To))
	switch {
	case strings.Contains(to, "group:"), strings.Contains(to, "channel:"), strings.Contains(to, "chat:"), strings.Contains(to, "room:"), strings.Contains(to, "topic:"):
		return false
	case entry != nil && strings.Contains(strings.ToLower(strings.TrimSpace(entry.LastChannel)), "group"):
		return false
	default:
		return true
	}
}
