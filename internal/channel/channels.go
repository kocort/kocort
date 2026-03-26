package channel

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	evtpkg "github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

// =========================================================================
// Delivery mode classification
// =========================================================================

// ChannelDeliveryMode classifies how a channel delivers messages.
type ChannelDeliveryMode string

const (
	ChannelDeliveryDirect  ChannelDeliveryMode = "direct"
	ChannelDeliveryGateway ChannelDeliveryMode = "gateway"
	ChannelDeliveryHybrid  ChannelDeliveryMode = "hybrid"
)

// =========================================================================
// Interfaces for runtime type-assertions on adapters
//
// These are the only interface types defined in this package. All other
// capability discovery uses duck-typed assertions on the adapter value
// (e.g. SendText, SendMedia, SendPayload, StopBackground).
// =========================================================================

// =========================================================================
// ChannelIntegration — type alias for adapter.Integration
// =========================================================================

// =========================================================================
// ChannelRegistry — manages registered channel adapters
// =========================================================================

// ChannelManager stores channel adapters and their configuration.
// channels are stored in a single map keyed by normalized channel ID.
// Runtime type-assertions are used to discover adapter capabilities
// (HTTP ingress, outbound delivery, background lifecycle, etc.).
type ChannelManager struct {
	mu       sync.Mutex
	channels map[string]adapter.ChannelAdapter
	config   config.ChannelsConfig
	dc       *infra.DynamicHTTPClient
}

// NewChannelManager creates a new ChannelManager with the given config
// and optional pre-registered integrations.
func NewChannelManager(cfg config.ChannelsConfig) *ChannelManager {
	r := &ChannelManager{
		channels: map[string]adapter.ChannelAdapter{},
		config:   cfg,
	}
	return r
}

// RegisterChannel stores an adapter under the given normalized channel ID.
func (r *ChannelManager) RegisterChannel(id string, a adapter.ChannelAdapter) {
	if r == nil || a == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.channels[id] = a
}

// RegisterChannelByConfig
func (r *ChannelManager) RegisterChannelByConfig(channelID string, cfg config.ChannelConfig) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.channels[channelID] = adapter.BuildChannel(channelID, cfg)
}

// GetChannel returns the registered adapter for the given channel ID, or nil.
func (r *ChannelManager) GetChannel(channelID string) adapter.ChannelAdapter {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.channels[adapter.NormalizeID(channelID)]
}

// Outbound returns the adapter for the channel (alias for GetChannel).
// Satisfies the delivery.ChannelOutboundResolver interface.
func (r *ChannelManager) Outbound(channelID string) any {
	return r.GetChannel(channelID)
}

// =========================================================================
// Config application and background lifecycle
// =========================================================================

// ApplyConfig replaces the channel configuration. Old adapters are stopped
// before new ones are created from the updated config entries.
func (r *ChannelManager) ApplyConfig(cfg config.ChannelsConfig) {
	if r == nil {
		return
	}

	// Snapshot old adapters before replacing.
	r.mu.Lock()
	old := make(map[string]adapter.ChannelAdapter, len(r.channels))
	for k, v := range r.channels {
		old[k] = v
	}
	r.config = cfg
	r.mu.Unlock()

	// Stop old background runners.
	for _, a := range old {
		if stopper, ok := a.(interface{ StopBackground() }); ok {
			stopper.StopBackground()
		}
	}

	// Build and register new adapters from config.
	for channelID, entry := range cfg.Entries {
		r.RegisterChannel(channelID, adapter.BuildChannel(channelID, entry))
	}
}

// RestartChannelBackgrounds starts (or restarts) background goroutines for
// every configured channel whose adapter implements adapter.ChannelAdapter.
func (r *ChannelManager) RestartChannelBackgrounds(ctx context.Context, rt rtypes.RuntimeServices) {
	if r == nil {
		return
	}
	r.mu.Lock()
	entries := make(map[string]struct{}, len(r.config.Entries))
	for id := range r.config.Entries {
		entries[id] = struct{}{}
	}
	dc := r.dc
	r.mu.Unlock()

	for channelID := range entries {
		ca := r.GetChannel(channelID)
		if ca == nil {
			continue
		}
		ch := r.ResolveConfig(channelID)
		cb := buildAdapterCallbacks(rt)
		ca.StopBackground()
		if err := ca.StartBackground(ctx, channelID, ch, dc, cb); err != nil {
			slog.Warn("channel background restart failed",
				"channel", channelID,
				"error", err,
			)
		}
	}
}

// SetDynamicHTTPClient sets the DynamicHTTPClient for all channel adapters.
// This is called once during runtime initialization.
func (r *ChannelManager) SetDynamicHTTPClient(dc *infra.DynamicHTTPClient) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.dc = dc
	r.mu.Unlock()
}

// =========================================================================
// Config resolution
// =========================================================================

func (r *ChannelManager) ResolveConfig(channelID string) config.ChannelConfig {
	if r == nil {
		return config.ChannelConfig{}
	}
	channelID = adapter.NormalizeID(channelID)
	entry := config.ChannelConfig{}
	if r.config.Defaults != nil {
		entry.Agent = strings.TrimSpace(r.config.Defaults.DefaultAgent)
		entry.DefaultAccount = strings.TrimSpace(r.config.Defaults.DefaultAccount)
		entry.AllowFrom = append([]string{}, r.config.Defaults.AllowFrom...)
		entry.TextChunkLimit = r.config.Defaults.TextChunkLimit
		entry.ChunkMode = strings.TrimSpace(r.config.Defaults.ChunkMode)
	}
	if override, ok := r.config.Entries[channelID]; channelID != "" && ok {
		if override.Enabled != nil {
			entry.Enabled = override.Enabled
		}
		setNonEmpty(&entry.DefaultTo, override.DefaultTo)
		setNonEmpty(&entry.DefaultAccount, override.DefaultAccount)
		setNonEmpty(&entry.Agent, override.Agent)
		setNonEmpty(&entry.InboundToken, override.InboundToken)
		if len(override.AllowFrom) > 0 {
			entry.AllowFrom = append([]string{}, override.AllowFrom...)
		}
		if override.TextChunkLimit > 0 {
			entry.TextChunkLimit = override.TextChunkLimit
		}
		setNonEmpty(&entry.ChunkMode, override.ChunkMode)
		if len(override.Accounts) > 0 {
			entry.Accounts = override.Accounts
		}
		if len(override.Config) > 0 {
			entry.Config = override.Config
		}
	}
	return entry
}

func (r *ChannelManager) IsEnabled(channelID string) bool {
	cfg := r.ResolveConfig(channelID)
	return cfg.Enabled == nil || *cfg.Enabled
}

// =========================================================================
// Snapshot for API / dashboard
// =========================================================================

func (r *ChannelManager) Snapshot() []adapter.ChannelIntegrationSummary {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.config.Entries))
	for key := range r.config.Entries {
		keys = append(keys, adapter.NormalizeID(key))
	}
	if len(keys) == 0 {
		for key := range r.channels {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]adapter.ChannelIntegrationSummary, 0, len(keys))
	for _, key := range keys {
		cfg := r.ResolveConfig(key)
		summary := adapter.ChannelIntegrationSummary{
			ID:             key,
			Enabled:        cfg.Enabled == nil || *cfg.Enabled,
			Agent:          cfg.Agent,
			DefaultTo:      cfg.DefaultTo,
			DefaultAccount: cfg.DefaultAccount,
			AllowFrom:      append([]string{}, cfg.AllowFrom...),
			TextChunkLimit: cfg.TextChunkLimit,
			ChunkMode:      cfg.ChunkMode,
		}

		out = append(out, summary)
	}
	return out
}

// =========================================================================
// Outbound resolution
// =========================================================================

func (r *ChannelManager) ResolveAgentID(channelID string) string {
	cfg := r.ResolveConfig(channelID)
	return session.NormalizeAgentID(cfg.Agent)
}

func (r *ChannelManager) ResolveOutboundMessage(ctx context.Context, target core.DeliveryTarget, payload core.ReplyPayload) (core.ChannelOutboundMessage, config.ChannelConfig, error) {
	if r == nil {
		return core.ChannelOutboundMessage{}, config.ChannelConfig{}, core.ErrChannelRegistryNotConfigured
	}
	channelID := adapter.NormalizeID(target.Channel)
	if channelID == "" {
		return core.ChannelOutboundMessage{}, config.ChannelConfig{}, core.ErrChannelRequired
	}
	cfg := r.ResolveConfig(channelID)

	accountID := strings.TrimSpace(target.AccountID)
	if accountID == "" {
		accountID = cfg.DefaultAccount
	}
	to := strings.TrimSpace(target.To)
	if to == "" {
		to = cfg.DefaultTo
	}
	mode := "implicit"
	if to != "" {
		mode = "explicit"
	}
	message := core.ChannelOutboundMessage{
		Channel:   channelID,
		AccountID: accountID,
		To:        to,
		AllowFrom: append([]string{}, cfg.AllowFrom...),
		Mode:      mode,
		ThreadID:  strings.TrimSpace(target.ThreadID),
		Payload:   payload,
	}
	if strings.TrimSpace(message.ReplyToID) == "" {
		message.ReplyToID = strings.TrimSpace(payload.ReplyToID)
	}

	outbound := r.GetChannel(channelID)
	if outbound == nil {
		return core.ChannelOutboundMessage{}, cfg, fmt.Errorf("channel outbound %q is not registered", channelID)
	}

	// Post-resolver safety fallbacks — resolver may have cleared fields.
	if strings.TrimSpace(message.Channel) == "" {
		message.Channel = channelID
	}
	if strings.TrimSpace(message.AccountID) == "" {
		message.AccountID = cfg.DefaultAccount
	}
	if len(message.AllowFrom) == 0 && len(cfg.AllowFrom) > 0 {
		message.AllowFrom = append([]string{}, cfg.AllowFrom...)
	}
	if strings.TrimSpace(message.Mode) == "" {
		message.Mode = "implicit"
	}
	if strings.TrimSpace(message.To) == "" {
		message.To = cfg.DefaultTo
	}
	if strings.TrimSpace(message.To) == "" {
		return core.ChannelOutboundMessage{}, cfg, fmt.Errorf("channel %q is missing outbound target", channelID)
	}
	return message, cfg, nil
}

// =========================================================================
// Inbound normalization
// =========================================================================

func (r *ChannelManager) NormalizeInbound(channelID string, msg *core.ChannelInboundMessage) (*core.ChannelInboundMessage, error) {
	if msg == nil {
		return nil, fmt.Errorf("inbound message is required")
	}
	normalizedChannel := adapter.NormalizeID(utils.NonEmpty(msg.Channel, channelID))
	if normalizedChannel == "" {
		return nil, fmt.Errorf("inbound channel is required")
	}
	cfg := r.ResolveConfig(normalizedChannel)
	out := *msg
	out.Channel = normalizedChannel
	out.AccountID = strings.TrimSpace(out.AccountID)
	out.From = strings.TrimSpace(out.From)
	out.To = strings.TrimSpace(out.To)
	out.ThreadID = strings.TrimSpace(out.ThreadID)
	out.Text = strings.TrimSpace(out.Text)
	if len(out.Attachments) > 0 {
		out.Attachments = append([]core.Attachment{}, out.Attachments...)
	}
	if out.ChatType == "" {
		if out.ThreadID != "" {
			out.ChatType = core.ChatTypeThread
		} else {
			out.ChatType = core.ChatTypeDirect
		}
	}
	out.AgentID = session.NormalizeAgentID(utils.NonEmpty(out.AgentID, cfg.Agent))
	out.MessageID = strings.TrimSpace(out.MessageID)
	return &out, nil
}

// =========================================================================
// Adapter callback factory — creates adapter.Callbacks from RuntimeServices
// =========================================================================

// buildAdapterCallbacks creates adapter.Callbacks that forward inbound
// messages and audit events to the runtime. Since adapter types are now
// aliases for core types, no type conversion is needed.
func buildAdapterCallbacks(rt rtypes.RuntimeServices) adapter.Callbacks {
	return adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg core.ChannelInboundMessage) error {
			_, err := rt.PushInbound(ctx, msg)
			return err
		},
		OnAudit: func(ctx context.Context, entry adapter.AuditEntry) {
			evtpkg.RecordAudit(ctx, rt.GetAudit(), nil, core.AuditEvent{
				Category: core.AuditCategory(entry.Category),
				Type:     entry.Type,
				Level:    entry.Level,
				Channel:  entry.Channel,
				Message:  entry.Message,
				Data:     entry.Data,
			})
		},
	}
}

// setNonEmpty sets *dst to the trimmed value of src if non-empty.
func setNonEmpty(dst *string, src string) {
	if v := strings.TrimSpace(src); v != "" {
		*dst = v
	}
}
