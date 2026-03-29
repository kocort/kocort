package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/runtime"
)

// ACPGateway provides a gateway-shaped facade over the local API/runtime layer
type ACPGateway struct {
	Runtime *runtime.Runtime
}

type ACPGatewaySessionMeta struct {
	SessionKey      string
	SessionLabel    string
	ResetSession    bool
	RequireExisting bool
}

type ACPGatewaySessionPatch struct {
	ThinkingLevel  string
	FastMode       *bool
	VerboseLevel   string
	ReasoningLevel string
	ResponseUsage  string
	ElevatedLevel  string
	ModelID        string
	ApprovalPolicy string
	Cwd            string
}

type ACPGatewaySessionRow struct {
	Key              string
	Title            string
	Label            string
	Kind             string
	Channel          string
	UpdatedAt        time.Time
	ThinkingLevel    string
	FastMode         bool
	ModelProvider    string
	Model            string
	VerboseLevel     string
	ReasoningLevel   string
	ResponseUsage    string
	ElevatedLevel    string
	ApprovalPolicy   string
	ContextTokens    int
	TotalTokens      int
	TotalTokensFresh bool
	Cwd              string
}

func NewACPGateway(rt *runtime.Runtime) *ACPGateway {
	return &ACPGateway{Runtime: rt}
}

func (g *ACPGateway) ChatSend(ctx context.Context, req core.ChatSendRequest) (core.ChatSendResponse, error) {
	if g == nil || g.Runtime == nil {
		return core.ChatSendResponse{}, fmt.Errorf("runtime is not configured")
	}
	req.ExtraSystemPrompt = joinSystemPrompts(req.ExtraSystemPrompt, buildACPSystemPrompt(req))
	return g.Runtime.ChatSend(ctx, req)
}

func (g *ACPGateway) ChatCancel(ctx context.Context, req core.ChatCancelRequest) (core.ChatCancelResponse, error) {
	if g == nil || g.Runtime == nil {
		return core.ChatCancelResponse{}, fmt.Errorf("runtime is not configured")
	}
	return g.Runtime.ChatCancel(ctx, req)
}

func (g *ACPGateway) Subscribe(sessionKey string) (<-chan gateway.SSEEvent, func()) {
	if g == nil || g.Runtime == nil || g.Runtime.EventHub == nil {
		ch := make(chan gateway.SSEEvent)
		close(ch)
		return ch, func() {}
	}
	return g.Runtime.EventHub.Subscribe(sessionKey)
}

func (g *ACPGateway) LoadTranscript(sessionKey string) ([]core.TranscriptMessage, error) {
	if g == nil || g.Runtime == nil || g.Runtime.Sessions == nil {
		return nil, nil
	}
	return g.Runtime.Sessions.LoadTranscript(sessionKey)
}

func (g *ACPGateway) ListSessionRows() []ACPGatewaySessionRow {
	if g == nil || g.Runtime == nil || g.Runtime.Sessions == nil {
		return nil
	}
	items := g.Runtime.Sessions.ListSessions()
	rows := make([]ACPGatewaySessionRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, g.sessionRowFromListItem(item))
	}
	return rows
}

func (g *ACPGateway) GetSessionRow(sessionKey string) *ACPGatewaySessionRow {
	if g == nil || g.Runtime == nil || g.Runtime.Sessions == nil {
		return nil
	}
	entry := g.Runtime.Sessions.Entry(sessionKey)
	if entry == nil {
		return nil
	}
	row := g.rowFromEntry(sessionKey, entry)
	return &row
}

func (g *ACPGateway) ResolveSessionKey(meta ACPGatewaySessionMeta, fallbackKey string, agentID string) (string, error) {
	if g == nil || g.Runtime == nil || g.Runtime.Sessions == nil {
		return "", fmt.Errorf("session store is not configured")
	}
	requireExisting := meta.RequireExisting
	tryKey := func(candidate string) (string, error) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return "", nil
		}
		if !requireExisting {
			return candidate, nil
		}
		if key, ok := g.Runtime.Sessions.ResolveSessionKeyReference(candidate); ok {
			return key, nil
		}
		return "", fmt.Errorf("session key not found: %s", candidate)
	}
	if label := strings.TrimSpace(meta.SessionLabel); label != "" {
		if key, ok := g.Runtime.Sessions.ResolveSessionLabel(agentID, label, ""); ok {
			return key, nil
		}
		return "", fmt.Errorf("unable to resolve session label: %s", label)
	}
	if key, err := tryKey(meta.SessionKey); key != "" || err != nil {
		return key, err
	}
	return strings.TrimSpace(fallbackKey), nil
}

func (g *ACPGateway) EnsureSession(ctx context.Context, sessionKey, cwd string, reset bool) error {
	if g == nil || g.Runtime == nil || g.Runtime.Sessions == nil {
		return fmt.Errorf("runtime is not configured")
	}
	if reset {
		if _, err := g.Runtime.Sessions.Reset(sessionKey, "acp-reset"); err != nil {
			return err
		}
	}
	agentID := session.ResolveAgentIDFromSessionKey(sessionKey)
	if agentID == "" {
		agentID = config.ResolveDefaultConfiguredAgentID(g.Runtime.Config)
	}
	_, err := g.Runtime.Sessions.ResolveForRequest(ctx, session.SessionResolveOptions{
		AgentID:             agentID,
		SessionKey:          sessionKey,
		Channel:             "webchat",
		To:                  "acp-client",
		ChatType:            core.ChatTypeDirect,
		MainKey:             config.ResolveSessionMainKey(g.Runtime.Config),
		DMScope:             config.ResolveSessionDMScope(g.Runtime.Config),
		ParentForkMaxTokens: config.ResolveSessionParentForkMaxTokens(g.Runtime.Config),
		Now:                 time.Now().UTC(),
		ResetPolicy:         session.ResolveFreshnessPolicyForSession(g.Runtime.Config, sessionKey, core.ChatTypeDirect, "webchat", ""),
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	return g.Runtime.Sessions.Mutate(sessionKey, func(entry *core.SessionEntry) error {
		entry.Label = defaultGatewayString(entry.Label, defaultGatewaySessionLabel(sessionKey))
		if entry.ACP == nil {
			entry.ACP = &core.AcpSessionMeta{}
		}
		entry.ACP.Cwd = strings.TrimSpace(cwd)
		return nil
	})
}

func (g *ACPGateway) PatchSession(sessionKey string, patch ACPGatewaySessionPatch) (*ACPGatewaySessionRow, error) {
	if g == nil || g.Runtime == nil || g.Runtime.Sessions == nil {
		return nil, fmt.Errorf("runtime is not configured")
	}
	if err := g.Runtime.Sessions.Mutate(sessionKey, func(entry *core.SessionEntry) error {
		if strings.TrimSpace(patch.ThinkingLevel) != "" {
			entry.ThinkingLevel = strings.TrimSpace(patch.ThinkingLevel)
		}
		if patch.FastMode != nil {
			entry.FastMode = *patch.FastMode
		}
		if strings.TrimSpace(patch.VerboseLevel) != "" {
			entry.VerboseLevel = strings.TrimSpace(patch.VerboseLevel)
		}
		if strings.TrimSpace(patch.ReasoningLevel) != "" {
			entry.ReasoningLevel = strings.TrimSpace(patch.ReasoningLevel)
		}
		if strings.TrimSpace(patch.ResponseUsage) != "" {
			entry.ResponseUsage = strings.TrimSpace(patch.ResponseUsage)
		}
		if strings.TrimSpace(patch.ElevatedLevel) != "" {
			entry.ElevatedLevel = strings.TrimSpace(patch.ElevatedLevel)
		}
		if strings.TrimSpace(patch.ModelID) != "" {
			entry.ModelOverride = strings.TrimSpace(patch.ModelID)
		}
		if strings.TrimSpace(patch.ApprovalPolicy) != "" {
			entry.AuthProfileOverride = strings.TrimSpace(patch.ApprovalPolicy)
		}
		if entry.ACP == nil {
			entry.ACP = &core.AcpSessionMeta{}
		}
		if strings.TrimSpace(patch.Cwd) != "" {
			entry.ACP.Cwd = strings.TrimSpace(patch.Cwd)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return g.GetSessionRow(sessionKey), nil
}

func (g *ACPGateway) sessionRowFromListItem(item session.SessionListItem) ACPGatewaySessionRow {
	row := ACPGatewaySessionRow{
		Key:            item.Key,
		Title:          defaultGatewayString(item.Label, item.Key),
		Label:          strings.TrimSpace(item.Label),
		Kind:           strings.TrimSpace(item.Kind),
		Channel:        strings.TrimSpace(item.Channel),
		UpdatedAt:      item.UpdatedAt.UTC(),
		ThinkingLevel:  strings.TrimSpace(item.ThinkingLevel),
		FastMode:       item.FastMode,
		VerboseLevel:   strings.TrimSpace(item.VerboseLevel),
		ReasoningLevel: strings.TrimSpace(item.ReasoningLevel),
		ResponseUsage:  strings.TrimSpace(item.ResponseUsage),
		ElevatedLevel:  strings.TrimSpace(item.ElevatedLevel),
		ModelProvider:  strings.TrimSpace(item.ProviderOverride),
		Model:          strings.TrimSpace(item.ModelOverride),
	}
	if entry := g.Runtime.Sessions.Entry(item.Key); entry != nil {
		row = g.rowFromEntry(item.Key, entry)
		row.Kind = defaultGatewayString(row.Kind, item.Kind)
		row.Channel = defaultGatewayString(row.Channel, item.Channel)
	}
	return row
}

func (g *ACPGateway) rowFromEntry(sessionKey string, entry *core.SessionEntry) ACPGatewaySessionRow {
	if entry == nil {
		return ACPGatewaySessionRow{Key: sessionKey, Title: defaultGatewaySessionLabel(sessionKey)}
	}
	provider := strings.TrimSpace(entry.ProviderOverride)
	model := strings.TrimSpace(entry.ModelOverride)
	if provider == "" {
		provider = strings.TrimSpace(entry.ActiveProvider)
	}
	if model == "" {
		model = strings.TrimSpace(entry.ActiveModel)
	}
	if provider == "" || model == "" {
		for providerID, providerCfg := range g.Runtime.Config.Models.Providers {
			if provider == "" {
				provider = providerID
			}
			if model == "" && len(providerCfg.Models) > 0 {
				model = strings.TrimSpace(providerCfg.Models[0].ID)
			}
			if provider != "" && model != "" {
				break
			}
		}
	}
	row := ACPGatewaySessionRow{
		Key:            sessionKey,
		Title:          defaultGatewayString(strings.TrimSpace(entry.Label), defaultGatewaySessionLabel(sessionKey)),
		Label:          strings.TrimSpace(entry.Label),
		UpdatedAt:      entry.UpdatedAt.UTC(),
		ThinkingLevel:  strings.TrimSpace(entry.ThinkingLevel),
		FastMode:       entry.FastMode,
		VerboseLevel:   strings.TrimSpace(entry.VerboseLevel),
		ReasoningLevel: strings.TrimSpace(entry.ReasoningLevel),
		ResponseUsage:  strings.TrimSpace(entry.ResponseUsage),
		ElevatedLevel:  strings.TrimSpace(entry.ElevatedLevel),
		ModelProvider:  provider,
		Model:          model,
		ApprovalPolicy: strings.TrimSpace(entry.AuthProfileOverride),
		ContextTokens:  entry.ContextTokens,
		Cwd:            "",
	}
	if entry.ACP != nil {
		row.Cwd = strings.TrimSpace(entry.ACP.Cwd)
	}
	if totalTokens, ok := usageInt(entry.Usage["total_tokens"]); ok {
		row.TotalTokens = totalTokens
		row.TotalTokensFresh = true
	}
	return row
}

func usageInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func buildACPSystemPrompt(req core.ChatSendRequest) string {
	if len(req.SystemInputProvenance) == 0 && strings.TrimSpace(req.SystemProvenanceReceipt) == "" {
		return ""
	}
	lines := make([]string, 0, 10)
	if len(req.SystemInputProvenance) > 0 {
		lines = append(lines, "[ACP Source Metadata]")
		if kind := strings.TrimSpace(defaultGatewayString(anyString(req.SystemInputProvenance["kind"]), "external_user")); kind != "" {
			lines = append(lines, "kind="+kind)
		}
		for _, key := range []string{"bridge", "sourceChannel", "sourceTool", "originSessionId", "targetSession", "originCwd"} {
			if value := strings.TrimSpace(anyString(req.SystemInputProvenance[key])); value != "" {
				lines = append(lines, key+"="+value)
			}
		}
		lines = append(lines, "[/ACP Source Metadata]")
	}
	if receipt := strings.TrimSpace(req.SystemProvenanceReceipt); receipt != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, receipt)
	}
	return strings.Join(lines, "\n")
}

func joinSystemPrompts(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n\n")
}

func anyString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func defaultGatewayString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func defaultGatewaySessionLabel(sessionKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "ACP Session"
	}
	return sessionKey
}
