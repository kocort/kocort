package acpbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/kocort/kocort/api/service"
	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/runtime"
)

const (
	acpMethodSessionList            = "session/list"
	acpMethodSessionSetConfigOption = "session/set_config_option"
	acpDefaultTimeout               = 90 * time.Second
	acpMaxPromptBytes               = 2 * 1024 * 1024
	acpMaxSessions                  = 5000
	acpIdleSessionTTL               = 24 * time.Hour
	acpSessionCreateWindow          = 10 * time.Second
	acpSessionCreateLimit           = 120
)

type ACPBridgeOptions struct {
	DefaultSessionKey   string
	DefaultSessionLabel string
	RequireExisting     bool
	ResetSession        bool
	PrefixCwd           bool
	ProvenanceMode      string
	Logger              *slog.Logger
}

type bridgeSession struct {
	SessionID      string
	SessionKey     string
	Cwd            string
	PrefixCwd      bool
	ThinkingLevel  string
	FastMode       string
	VerboseLevel   string
	ReasoningLevel string
	ResponseUsage  string
	ElevatedLevel  string
	ModelID        string
	ApprovalPolicy string
	TimeoutMs      int
	CurrentRunID   string
	SentToolCalls  map[string]toolCallSnapshot
	LastTouchedAt  time.Time
}

type toolCallSnapshot struct {
	Title string
	Kind  string
}

type ACPBridgeServer struct {
	runtime *runtime.Runtime
	gateway *service.ACPGateway
	opts    ACPBridgeOptions
	conn    *acpsdk.Connection

	mu       sync.Mutex
	sessions map[string]*bridgeSession
	pending  map[string]*pendingPromptState
	created  []time.Time
}

type acpSessionMeta struct {
	SessionKey      string `json:"sessionKey,omitempty"`
	SessionLabel    string `json:"sessionLabel,omitempty"`
	ResetSession    *bool  `json:"resetSession,omitempty"`
	RequireExisting *bool  `json:"requireExisting,omitempty"`
	PrefixCwd       *bool  `json:"prefixCwd,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	TimeoutMs       int    `json:"timeoutMs,omitempty"`
}

type acpSessionListRequest struct {
	Meta any    `json:"_meta,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

type acpSessionListResponse struct {
	Sessions   []acpListedSession `json:"sessions"`
	NextCursor *string            `json:"nextCursor"`
}

type acpListedSession struct {
	SessionID string         `json:"sessionId"`
	Cwd       string         `json:"cwd,omitempty"`
	Title     string         `json:"title,omitempty"`
	UpdatedAt string         `json:"updatedAt,omitempty"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

type acpSetConfigOptionRequest struct {
	Meta      any              `json:"_meta,omitempty"`
	SessionID acpsdk.SessionId `json:"sessionId"`
	ConfigID  string           `json:"configId"`
	Value     any              `json:"value"`
}

type acpSetConfigOptionResponse struct {
	ConfigOptions []acpSessionConfigOption `json:"configOptions,omitempty"`
}

type acpSessionConfigOption struct {
	Type         string                      `json:"type"`
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Category     string                      `json:"category,omitempty"`
	Description  string                      `json:"description,omitempty"`
	CurrentValue string                      `json:"currentValue,omitempty"`
	Options      []acpSessionConfigSelectOpt `json:"options,omitempty"`
}

type acpSessionConfigSelectOpt struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

type acpRawSessionNotification struct {
	SessionID acpsdk.SessionId `json:"sessionId"`
	Update    any              `json:"update"`
}

type acpSessionInfoUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Title         string `json:"title,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type acpUsageUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Used          int    `json:"used"`
	Size          int    `json:"size"`
	Meta          any    `json:"_meta,omitempty"`
}

type acpConfigOptionUpdate struct {
	SessionUpdate string                   `json:"sessionUpdate"`
	ConfigOptions []acpSessionConfigOption `json:"configOptions"`
}

type promptRunResult struct {
	StopReason acpsdk.StopReason
	Err        error
}

type pendingPromptState struct {
	RunID string
	Done  chan acpsdk.StopReason
}

func ServeACPBridge(ctx context.Context, rt *runtime.Runtime, opts ACPBridgeOptions, out io.Writer, in io.Reader) error {
	server := NewACPBridgeServer(rt, opts)
	server.conn = acpsdk.NewConnection(server.handle, out, in)
	if opts.Logger != nil {
		server.conn.SetLogger(opts.Logger)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-server.conn.Done():
		return nil
	}
}

func NewACPBridgeServer(rt *runtime.Runtime, opts ACPBridgeOptions) *ACPBridgeServer {
	if !opts.PrefixCwd {
		opts.PrefixCwd = false
	} else {
		opts.PrefixCwd = true
	}
	return &ACPBridgeServer{
		runtime:  rt,
		gateway:  service.NewACPGateway(rt),
		opts:     opts,
		sessions: map[string]*bridgeSession{},
		pending:  map[string]*pendingPromptState{},
		created:  []time.Time{},
	}
}

func (s *ACPBridgeServer) handle(ctx context.Context, method string, params json.RawMessage) (any, *acpsdk.RequestError) {
	switch method {
	case acpsdk.AgentMethodInitialize:
		var req acpsdk.InitializeRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return s.initialize(req), nil
	case acpsdk.AgentMethodAuthenticate:
		return acpsdk.AuthenticateResponse{}, nil
	case acpsdk.AgentMethodSessionNew:
		var req acpsdk.NewSessionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return s.newSession(ctx, req)
	case acpsdk.AgentMethodSessionLoad:
		var req acpsdk.LoadSessionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return s.loadSession(ctx, req)
	case acpMethodSessionList:
		var req acpSessionListRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return s.listSessions(req), nil
	case acpsdk.AgentMethodSessionPrompt:
		var req acpsdk.PromptRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return s.prompt(ctx, req)
	case acpsdk.AgentMethodSessionCancel:
		var req acpsdk.CancelNotification
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return nil, s.cancel(ctx, req)
	case acpsdk.AgentMethodSessionSetMode:
		var req acpsdk.SetSessionModeRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return s.setSessionMode(ctx, req)
	case acpMethodSessionSetConfigOption:
		var req acpSetConfigOptionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return s.setSessionConfigOption(ctx, req)
	default:
		return nil, acpsdk.NewMethodNotFound(method)
	}
}

func (s *ACPBridgeServer) initialize(_ acpsdk.InitializeRequest) map[string]any {
	title := "Kocort ACP"
	return map[string]any{
		"protocolVersion": acpsdk.ProtocolVersionNumber,
		"agentCapabilities": map[string]any{
			"loadSession": true,
			"promptCapabilities": map[string]any{
				"image":           true,
				"audio":           false,
				"embeddedContext": true,
			},
			"mcpCapabilities": map[string]any{
				"http": false,
				"sse":  false,
			},
			"sessionCapabilities": map[string]any{
				"list": map[string]any{},
			},
		},
		"agentInfo": map[string]any{
			"name":    "kocort-acp",
			"title":   title,
			"version": "0.1.0",
		},
		"authMethods": []any{},
	}
}

func (s *ACPBridgeServer) newSession(ctx context.Context, req acpsdk.NewSessionRequest) (map[string]any, *acpsdk.RequestError) {
	if err := s.assertSessionSetupSupported(req.McpServers); err != nil {
		return nil, err
	}
	if err := s.enforceSessionCreateRateLimit("newSession"); err != nil {
		return nil, err
	}
	meta := parseACPBridgeMeta(req.Meta)
	sessionID := "acp-" + session.NewSessionID()
	sessionKey, err := s.gateway.ResolveSessionKey(service.ACPGatewaySessionMeta{
		SessionKey:      meta.SessionKey,
		SessionLabel:    meta.SessionLabel,
		ResetSession:    s.boolOption(meta.ResetSession, s.opts.ResetSession),
		RequireExisting: s.boolOption(meta.RequireExisting, s.opts.RequireExisting),
	}, "acp:"+sessionID, "")
	if err != nil {
		return nil, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if err := s.gateway.EnsureSession(ctx, sessionKey, strings.TrimSpace(req.Cwd), s.boolOption(meta.ResetSession, s.opts.ResetSession)); err != nil {
		return nil, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	sessionItem := s.upsertBridgeSession(sessionID, sessionKey, req.Cwd, meta)
	sessionSnapshot := s.buildSessionSnapshot(sessionItem)
	_ = s.sendSessionBootstrap(ctx, sessionItem)
	return map[string]any{
		"sessionId":     acpsdk.SessionId(sessionID),
		"modes":         sessionSnapshot["modes"],
		"configOptions": sessionSnapshot["configOptions"],
	}, nil
}

func (s *ACPBridgeServer) loadSession(ctx context.Context, req acpsdk.LoadSessionRequest) (map[string]any, *acpsdk.RequestError) {
	if err := s.assertSessionSetupSupported(req.McpServers); err != nil {
		return nil, err
	}
	if s.getBridgeSession(string(req.SessionId)) == nil {
		if err := s.enforceSessionCreateRateLimit("loadSession"); err != nil {
			return nil, err
		}
	}
	meta := parseACPBridgeMeta(req.Meta)
	sessionID := string(req.SessionId)
	if strings.TrimSpace(sessionID) == "" {
		return nil, acpsdk.NewInvalidParams(map[string]any{"error": "sessionId is required"})
	}
	sessionKey, err := s.gateway.ResolveSessionKey(service.ACPGatewaySessionMeta{
		SessionKey:      meta.SessionKey,
		SessionLabel:    meta.SessionLabel,
		ResetSession:    s.boolOption(meta.ResetSession, s.opts.ResetSession),
		RequireExisting: s.boolOption(meta.RequireExisting, s.opts.RequireExisting),
	}, sessionID, "")
	if err != nil {
		return nil, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	if err := s.gateway.EnsureSession(ctx, sessionKey, strings.TrimSpace(req.Cwd), s.boolOption(meta.ResetSession, s.opts.ResetSession)); err != nil {
		return nil, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	sessionItem := s.upsertBridgeSession(sessionID, sessionKey, req.Cwd, meta)
	if err := s.replayTranscript(ctx, sessionItem); err != nil {
		return nil, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	sessionSnapshot := s.buildSessionSnapshot(sessionItem)
	_ = s.sendSessionBootstrap(ctx, sessionItem)
	return map[string]any{
		"modes":         sessionSnapshot["modes"],
		"configOptions": sessionSnapshot["configOptions"],
	}, nil
}

func (s *ACPBridgeServer) listSessions(req acpSessionListRequest) acpSessionListResponse {
	if s.gateway == nil {
		return acpSessionListResponse{Sessions: []acpListedSession{}}
	}
	limit := 100
	if record, ok := req.Meta.(map[string]any); ok {
		if v, ok := record["limit"]; ok {
			if next := parseAnyInt(v); next > 0 {
				limit = next
			}
		}
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	items := s.gateway.ListSessionRows()
	out := make([]acpListedSession, 0, min(limit, len(items)))
	for _, item := range items {
		title := defaultString(item.Title, item.Label, item.Key)
		out = append(out, acpListedSession{
			SessionID: item.Key,
			Cwd:       cwd,
			Title:     title,
			UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
			Meta: map[string]any{
				"sessionKey": item.Key,
				"kind":       item.Kind,
				"channel":    item.Channel,
			},
		})
		if len(out) >= limit {
			break
		}
	}
	return acpSessionListResponse{Sessions: out}
}

func (s *ACPBridgeServer) prompt(ctx context.Context, req acpsdk.PromptRequest) (acpsdk.PromptResponse, *acpsdk.RequestError) {
	sessionItem := s.getBridgeSession(string(req.SessionId))
	if sessionItem == nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": "session not found"})
	}
	meta := parseACPBridgeMeta(req.Meta)
	text, attachments, err := flattenACPPrompt(req.Prompt)
	if err != nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
	}
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": "prompt content is empty"})
	}
	if sessionItem.CurrentRunID != "" {
		_, _ = s.gateway.ChatCancel(ctx, core.ChatCancelRequest{
			SessionKey: sessionItem.SessionKey,
			RunID:      sessionItem.CurrentRunID,
		})
		s.resolvePendingPrompt(sessionItem.SessionID, sessionItem.CurrentRunID, acpsdk.StopReasonCancelled)
	}
	if sessionItem.PrefixCwd && strings.TrimSpace(sessionItem.Cwd) != "" {
		text = fmt.Sprintf("[Working directory: %s]\n\n%s", shortenHomePath(sessionItem.Cwd), text)
	}
	if len(text) > 0 && len([]byte(text)) > acpMaxPromptBytes {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": fmt.Sprintf("prompt exceeds maximum allowed size of %d bytes", acpMaxPromptBytes)})
	}

	runID := session.NewRunID()
	s.setSessionRun(sessionItem.SessionID, runID)
	defer s.clearSessionRun(sessionItem.SessionID, runID)
	pending := s.registerPendingPrompt(sessionItem.SessionID, runID)
	defer s.clearPendingPrompt(sessionItem.SessionID, runID)

	events, cancelEvents := s.subscribePromptEvents(sessionItem, runID)
	defer cancelEvents()

	timeout := acpDefaultTimeout
	if meta.TimeoutMs > 0 {
		timeout = time.Duration(meta.TimeoutMs) * time.Millisecond
	} else if sessionItem.TimeoutMs > 0 {
		timeout = time.Duration(sessionItem.TimeoutMs) * time.Millisecond
	}
	promptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultCh := make(chan promptRunResult, 1)
	go func() {
		result, err := s.gateway.ChatSend(promptCtx, core.ChatSendRequest{
			RunID:                   runID,
			AgentID:                 resolveACPAgentIDFromSessionKey(sessionItem.SessionKey),
			SessionKey:              sessionItem.SessionKey,
			Message:                 text,
			Channel:                 "webchat",
			To:                      "acp-client",
			Deliver:                 resolvePromptDeliver(req.Meta),
			TimeoutMs:               int(timeout / time.Millisecond),
			ThinkingLevel:           defaultString(backend.NormalizeThinkLevel(meta.ThinkingLevel), sessionItem.ThinkingLevel),
			VerboseLevel:            sessionItem.VerboseLevel,
			SessionModelOverride:    sessionItem.ModelID,
			WorkspaceOverride:       sessionItem.Cwd,
			SystemInputProvenance:   s.buildSystemInputProvenance(sessionItem),
			SystemProvenanceReceipt: s.buildSystemProvenanceReceipt(sessionItem),
			Attachments:             attachments,
		})
		if err != nil {
			if promptCtx.Err() == context.Canceled {
				resultCh <- promptRunResult{StopReason: acpsdk.StopReasonCancelled}
				return
			}
			resultCh <- promptRunResult{Err: err}
			return
		}
		stopReason := acpsdk.StopReasonEndTurn
		if result.Aborted {
			stopReason = acpsdk.StopReasonCancelled
		}
		resultCh <- promptRunResult{StopReason: stopReason}
	}()

	stopReason := acpsdk.StopReasonEndTurn
	for {
		select {
		case <-ctx.Done():
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
		case reason := <-pending.Done:
			if reason == "" {
				reason = acpsdk.StopReasonCancelled
			}
			_ = s.sendSessionSnapshot(ctx, sessionItem, false)
			return acpsdk.PromptResponse{StopReason: reason}, nil
		case evt, ok := <-events:
			if !ok {
				continue
			}
			if mapped := s.forwardPromptEvent(ctx, sessionItem, evt); mapped != "" {
				stopReason = mapped
			}
		case result := <-resultCh:
			if result.Err != nil {
				return acpsdk.PromptResponse{}, acpsdk.NewInternalError(map[string]any{"error": result.Err.Error()})
			}
			if result.StopReason != "" {
				stopReason = result.StopReason
			}
			_ = s.sendSessionSnapshot(ctx, sessionItem, false)
			return acpsdk.PromptResponse{StopReason: stopReason}, nil
		}
	}
}

func (s *ACPBridgeServer) cancel(ctx context.Context, req acpsdk.CancelNotification) *acpsdk.RequestError {
	sessionItem := s.getBridgeSession(string(req.SessionId))
	if sessionItem == nil {
		return nil
	}
	_, err := s.gateway.ChatCancel(ctx, core.ChatCancelRequest{
		SessionKey: sessionItem.SessionKey,
		RunID:      sessionItem.CurrentRunID,
	})
	if err != nil {
		return acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	s.resolvePendingPrompt(sessionItem.SessionID, sessionItem.CurrentRunID, acpsdk.StopReasonCancelled)
	return nil
}

func (s *ACPBridgeServer) setSessionMode(ctx context.Context, req acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, *acpsdk.RequestError) {
	sessionItem := s.getBridgeSession(string(req.SessionId))
	if sessionItem == nil {
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": "session not found"})
	}
	sessionItem.ThinkingLevel = defaultString(backend.NormalizeThinkLevel(string(req.ModeId)), "adaptive")
	if _, err := s.gateway.PatchSession(sessionItem.SessionKey, service.ACPGatewaySessionPatch{
		ThinkingLevel: sessionItem.ThinkingLevel,
		Cwd:           sessionItem.Cwd,
	}); err != nil {
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	_ = s.sendSessionSnapshot(ctx, sessionItem, true)
	return acpsdk.SetSessionModeResponse{}, nil
}

func (s *ACPBridgeServer) setSessionConfigOption(ctx context.Context, req acpSetConfigOptionRequest) (acpSetConfigOptionResponse, *acpsdk.RequestError) {
	sessionItem := s.getBridgeSession(string(req.SessionID))
	if sessionItem == nil {
		return acpSetConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": "session not found"})
	}
	value, ok := req.Value.(string)
	if !ok {
		return acpSetConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": fmt.Sprintf("ACP bridge does not support non-string session config option values for %q", req.ConfigID)})
	}
	value = strings.TrimSpace(strings.ToLower(value))
	switch strings.TrimSpace(req.ConfigID) {
	case "thought_level":
		sessionItem.ThinkingLevel = defaultString(backend.NormalizeThinkLevel(value), "adaptive")
	case "fast_mode":
		sessionItem.FastMode = defaultString(normalizeToggleValue(value), "off")
	case "verbose_level":
		sessionItem.VerboseLevel = defaultString(normalizeVerboseValue(value), "off")
	case "reasoning_level":
		sessionItem.ReasoningLevel = defaultString(normalizeReasoningValue(value), "off")
	case "response_usage":
		sessionItem.ResponseUsage = defaultString(normalizeUsageValue(value), "off")
	case "elevated_level":
		sessionItem.ElevatedLevel = defaultString(normalizeElevatedValue(value), "off")
	default:
		return acpSetConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": fmt.Sprintf("unsupported config option %q", req.ConfigID)})
	}
	if _, err := s.gateway.PatchSession(sessionItem.SessionKey, service.ACPGatewaySessionPatch{
		ThinkingLevel:  sessionItem.ThinkingLevel,
		FastMode:       boolRef(sessionItem.FastMode == "on"),
		VerboseLevel:   sessionItem.VerboseLevel,
		ReasoningLevel: sessionItem.ReasoningLevel,
		ResponseUsage:  sessionItem.ResponseUsage,
		ElevatedLevel:  sessionItem.ElevatedLevel,
		ModelID:        sessionItem.ModelID,
		ApprovalPolicy: sessionItem.ApprovalPolicy,
		Cwd:            sessionItem.Cwd,
	}); err != nil {
		return acpSetConfigOptionResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	_ = s.sendSessionSnapshot(ctx, sessionItem, true)
	return acpSetConfigOptionResponse{ConfigOptions: s.buildConfigOptions(sessionItem)}, nil
}

func (s *ACPBridgeServer) upsertBridgeSession(sessionID, sessionKey, cwd string, meta acpSessionMeta) *bridgeSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reapIdleSessionsLocked(time.Now().UTC())
	item := s.sessions[sessionID]
	if item == nil {
		if len(s.sessions) >= acpMaxSessions {
			s.evictOldestIdleSessionLocked()
		}
		item = &bridgeSession{SessionID: sessionID, SentToolCalls: map[string]toolCallSnapshot{}}
		s.sessions[sessionID] = item
	}
	item.SessionKey = strings.TrimSpace(sessionKey)
	item.Cwd = strings.TrimSpace(cwd)
	item.PrefixCwd = s.boolOption(meta.PrefixCwd, s.opts.PrefixCwd)
	if row := s.gateway.GetSessionRow(sessionKey); row != nil {
		item.ThinkingLevel = defaultString(item.ThinkingLevel, row.ThinkingLevel)
		item.FastMode = defaultString(item.FastMode, toggleString(row.FastMode))
		item.VerboseLevel = defaultString(item.VerboseLevel, row.VerboseLevel)
		item.ReasoningLevel = defaultString(item.ReasoningLevel, row.ReasoningLevel)
		item.ResponseUsage = defaultString(item.ResponseUsage, row.ResponseUsage)
		item.ElevatedLevel = defaultString(item.ElevatedLevel, row.ElevatedLevel)
		item.ModelID = defaultString(item.ModelID, row.Model)
		item.ApprovalPolicy = defaultString(item.ApprovalPolicy, row.ApprovalPolicy)
		item.Cwd = defaultString(item.Cwd, row.Cwd)
	}
	if meta.ThinkingLevel != "" {
		item.ThinkingLevel = defaultString(backend.NormalizeThinkLevel(meta.ThinkingLevel), item.ThinkingLevel)
	}
	if meta.TimeoutMs > 0 {
		item.TimeoutMs = meta.TimeoutMs
	}
	item.ThinkingLevel = defaultString(item.ThinkingLevel, "adaptive")
	item.FastMode = defaultString(item.FastMode, "off")
	item.VerboseLevel = defaultString(item.VerboseLevel, "off")
	item.ReasoningLevel = defaultString(item.ReasoningLevel, "off")
	item.ResponseUsage = defaultString(item.ResponseUsage, "off")
	item.ElevatedLevel = defaultString(item.ElevatedLevel, "off")
	item.LastTouchedAt = time.Now().UTC()
	return item
}

func (s *ACPBridgeServer) getBridgeSession(sessionID string) *bridgeSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.sessions[sessionID]
	if item != nil {
		item.LastTouchedAt = time.Now().UTC()
	}
	return item
}

func (s *ACPBridgeServer) setSessionRun(sessionID, runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item := s.sessions[sessionID]; item != nil {
		item.CurrentRunID = runID
		item.SentToolCalls = map[string]toolCallSnapshot{}
		item.LastTouchedAt = time.Now().UTC()
	}
}

func (s *ACPBridgeServer) clearSessionRun(sessionID, runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item := s.sessions[sessionID]; item != nil && item.CurrentRunID == runID {
		item.CurrentRunID = ""
		item.LastTouchedAt = time.Now().UTC()
	}
}

func (s *ACPBridgeServer) registerPendingPrompt(sessionID, runID string) *pendingPromptState {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := &pendingPromptState{
		RunID: strings.TrimSpace(runID),
		Done:  make(chan acpsdk.StopReason, 1),
	}
	s.pending[sessionID] = pending
	return pending
}

func (s *ACPBridgeServer) clearPendingPrompt(sessionID, runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.pending[sessionID]
	if pending == nil {
		return
	}
	if strings.TrimSpace(runID) != "" && strings.TrimSpace(pending.RunID) != strings.TrimSpace(runID) {
		return
	}
	delete(s.pending, sessionID)
}

func (s *ACPBridgeServer) resolvePendingPrompt(sessionID, runID string, reason acpsdk.StopReason) {
	s.mu.Lock()
	pending := s.pending[sessionID]
	if pending == nil || (strings.TrimSpace(runID) != "" && strings.TrimSpace(pending.RunID) != strings.TrimSpace(runID)) {
		s.mu.Unlock()
		return
	}
	delete(s.pending, sessionID)
	s.mu.Unlock()
	select {
	case pending.Done <- reason:
	default:
	}
}

func (s *ACPBridgeServer) subscribePromptEvents(sessionItem *bridgeSession, runID string) (<-chan gateway.SSEEvent, func()) {
	if s.gateway == nil || s.gateway.Runtime == nil || s.gateway.Runtime.EventHub == nil {
		ch := make(chan gateway.SSEEvent)
		close(ch)
		return ch, func() {}
	}
	source, cancel := s.gateway.Subscribe(sessionItem.SessionKey)
	filtered := make(chan gateway.SSEEvent, 128)
	stop := make(chan struct{})
	go func() {
		defer close(filtered)
		for {
			select {
			case <-stop:
				return
			case evt, ok := <-source:
				if !ok {
					return
				}
				if strings.TrimSpace(evt.RunID) != "" && strings.TrimSpace(evt.RunID) != strings.TrimSpace(runID) {
					continue
				}
				filtered <- evt
			}
		}
	}()
	return filtered, func() {
		close(stop)
		cancel()
	}
}

func (s *ACPBridgeServer) forwardPromptEvent(ctx context.Context, sessionItem *bridgeSession, evt gateway.SSEEvent) acpsdk.StopReason {
	if evt.AgentEvent == nil {
		return ""
	}
	data := evt.AgentEvent.Data
	kind, _ := data["type"].(string)
	switch strings.TrimSpace(evt.AgentEvent.Stream) {
	case "assistant":
		switch kind {
		case "reasoning_delta":
			if text := stringValue(data["text"]); text != "" {
				_ = s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.UpdateAgentThoughtText(text))
			}
		case "text_delta":
			if text := stringValue(data["text"]); text != "" {
				_ = s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.UpdateAgentMessageText(text))
			}
		}
	case "tool":
		switch kind {
		case "tool_call":
			id := defaultString(stringValue(data["toolCallId"]), synthesizeToolCallID(stringValue(data["toolName"]), stringValue(data["arguments"]), stringValue(data["text"])))
			title := buildToolTitle(data)
			kind := mapToolKind(stringValue(data["toolName"]))
			locations := extractToolLocations(data)
			s.trackToolCall(sessionItem, id, toolCallSnapshot{Title: title, Kind: string(kind)})
			_ = s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.StartToolCall(acpsdk.ToolCallId(id), title, acpsdk.WithStartKind(kind), acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress), acpsdk.WithStartLocations(locations), acpsdk.WithStartRawInput(rawToolInput(data))))
		case "tool_execute_started":
			id := defaultString(stringValue(data["toolCallId"]), synthesizeToolCallID(stringValue(data["toolName"]), "", ""))
			_ = s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.UpdateToolCall(acpsdk.ToolCallId(id), acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusInProgress)))
		case "tool_execute_failed":
			id := defaultString(stringValue(data["toolCallId"]), synthesizeToolCallID(stringValue(data["toolName"]), "", ""))
			content := defaultString(stringValue(data["error"]), stringValue(data["text"]))
			_ = s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.UpdateToolCall(acpsdk.ToolCallId(id), acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusFailed), acpsdk.WithUpdateContent(toolCallContent(content)), acpsdk.WithUpdateRawOutput(data), acpsdk.WithUpdateLocations(extractToolLocations(data))))
		case "tool_execute_completed":
			id := defaultString(stringValue(data["toolCallId"]), synthesizeToolCallID(stringValue(data["toolName"]), "", ""))
			content := defaultString(stringValue(data["text"]), stringValue(data["error"]))
			_ = s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.UpdateToolCall(acpsdk.ToolCallId(id), acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted), acpsdk.WithUpdateContent(toolCallContent(content)), acpsdk.WithUpdateRawOutput(data), acpsdk.WithUpdateLocations(extractToolLocations(data))))
		case "tool_result":
			id := defaultString(stringValue(data["toolCallId"]), synthesizeToolCallID(stringValue(data["toolName"]), "", ""))
			content := defaultString(stringValue(data["text"]), stringValue(data["error"]))
			status := acpsdk.ToolCallStatusCompleted
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(content)), "ERROR") {
				status = acpsdk.ToolCallStatusFailed
			}
			_ = s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.UpdateToolCall(acpsdk.ToolCallId(id), acpsdk.WithUpdateStatus(status), acpsdk.WithUpdateContent(toolCallContent(content)), acpsdk.WithUpdateRawOutput(data), acpsdk.WithUpdateLocations(extractToolLocations(data))))
		}
	case "lifecycle":
		switch kind {
		case "run_completed", "done":
			return mapStopReason(stringValue(data["stopReason"]))
		case "run_failed", "error":
			return acpsdk.StopReasonEndTurn
		}
	}
	return ""
}

func (s *ACPBridgeServer) sendSessionBootstrap(ctx context.Context, sessionItem *bridgeSession) error {
	if err := s.sendSessionSnapshot(ctx, sessionItem, false); err != nil {
		return err
	}
	return s.sendAvailableCommands(ctx, sessionItem)
}

func (s *ACPBridgeServer) sendSessionSnapshot(ctx context.Context, sessionItem *bridgeSession, includeControls bool) error {
	if includeControls {
		if err := s.sendCurrentModeUpdate(ctx, sessionItem); err != nil {
			return err
		}
		if err := s.sendConfigOptionsUpdate(ctx, sessionItem); err != nil {
			return err
		}
	}
	if err := s.sendSessionInfoUpdate(ctx, sessionItem); err != nil {
		return err
	}
	return s.sendUsageUpdate(ctx, sessionItem)
}

func (s *ACPBridgeServer) sendSDKUpdate(ctx context.Context, sessionID string, update acpsdk.SessionUpdate) error {
	if s.conn == nil {
		return nil
	}
	return s.conn.SendNotification(ctx, acpsdk.ClientMethodSessionUpdate, acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(sessionID),
		Update:    update,
	})
}

func (s *ACPBridgeServer) sendRawUpdate(ctx context.Context, sessionID string, update any) error {
	if s.conn == nil {
		return nil
	}
	return s.conn.SendNotification(ctx, acpsdk.ClientMethodSessionUpdate, acpRawSessionNotification{
		SessionID: acpsdk.SessionId(sessionID),
		Update:    update,
	})
}

func (s *ACPBridgeServer) sendAvailableCommands(ctx context.Context, sessionItem *bridgeSession) error {
	return s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.SessionUpdate{
		AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{
			SessionUpdate: "available_commands_update",
			AvailableCommands: []acpsdk.AvailableCommand{
				{Name: "help", Description: "Show help and common commands."},
				{Name: "commands", Description: "List available commands."},
				{Name: "status", Description: "Show current status."},
				{Name: "context", Description: "Explain context usage (list|detail|json)."},
				{Name: "whoami", Description: "Show sender id (alias: /id)."},
				{Name: "id", Description: "Alias for /whoami."},
				{Name: "subagents", Description: "List or manage sub-agents."},
				{Name: "config", Description: "Read or write config (owner-only)."},
				{Name: "debug", Description: "Set runtime-only overrides (owner-only)."},
				{Name: "usage", Description: "Toggle usage footer (off|tokens|full)."},
				{Name: "stop", Description: "Stop the current run."},
				{Name: "restart", Description: "Restart the gateway (if enabled)."},
				{Name: "dock-telegram", Description: "Route replies to Telegram."},
				{Name: "dock-discord", Description: "Route replies to Discord."},
				{Name: "dock-slack", Description: "Route replies to Slack."},
				{Name: "activation", Description: "Set group activation (mention|always)."},
				{Name: "send", Description: "Set send mode (on|off|inherit)."},
				{Name: "reset", Description: "Reset the session (/new)."},
				{Name: "new", Description: "Reset the session (/reset)."},
				{Name: "think", Description: "Set thinking level (off|minimal|low|medium|high|xhigh)."},
				{Name: "verbose", Description: "Set verbose mode (on|full|off)."},
				{Name: "reasoning", Description: "Toggle reasoning output (on|off|stream)."},
				{Name: "elevated", Description: "Toggle elevated mode (on|off)."},
				{Name: "model", Description: "Select a model (list|status|<name>)."},
				{Name: "queue", Description: "Adjust queue mode and options."},
				{Name: "bash", Description: "Run a host command (if enabled)."},
				{Name: "compact", Description: "Compact the session history."},
			},
		},
	})
}

func (s *ACPBridgeServer) sendCurrentModeUpdate(ctx context.Context, sessionItem *bridgeSession) error {
	return s.sendSDKUpdate(ctx, sessionItem.SessionID, acpsdk.SessionUpdate{
		CurrentModeUpdate: &acpsdk.SessionCurrentModeUpdate{
			SessionUpdate: "current_mode_update",
			CurrentModeId: acpsdk.SessionModeId(defaultString(sessionItem.ThinkingLevel, "adaptive")),
		},
	})
}

func (s *ACPBridgeServer) sendConfigOptionsUpdate(ctx context.Context, sessionItem *bridgeSession) error {
	return s.sendRawUpdate(ctx, sessionItem.SessionID, acpConfigOptionUpdate{
		SessionUpdate: "config_option_update",
		ConfigOptions: s.buildConfigOptions(sessionItem),
	})
}

func (s *ACPBridgeServer) sendSessionInfoUpdate(ctx context.Context, sessionItem *bridgeSession) error {
	row := s.gateway.GetSessionRow(sessionItem.SessionKey)
	title := sessionItem.SessionKey
	updatedAt := ""
	if row != nil {
		title = defaultString(row.Title, row.Label, sessionItem.SessionKey)
		if !row.UpdatedAt.IsZero() {
			updatedAt = row.UpdatedAt.UTC().Format(time.RFC3339)
		}
	}
	return s.sendRawUpdate(ctx, sessionItem.SessionID, acpSessionInfoUpdate{
		SessionUpdate: "session_info_update",
		Title:         title,
		UpdatedAt:     updatedAt,
	})
}

func (s *ACPBridgeServer) sendUsageUpdate(ctx context.Context, sessionItem *bridgeSession) error {
	row := s.gateway.GetSessionRow(sessionItem.SessionKey)
	if row == nil || row.ContextTokens <= 0 || !row.TotalTokensFresh {
		return nil
	}
	size := max(row.ContextTokens, 0)
	used := max(row.TotalTokens, 0)
	if used > size {
		used = size
	}
	return s.sendRawUpdate(ctx, sessionItem.SessionID, acpUsageUpdate{
		SessionUpdate: "usage_update",
		Used:          used,
		Size:          size,
		Meta:          map[string]any{"source": "gateway-session-store", "approximate": true},
	})
}

func (s *ACPBridgeServer) buildModeState(sessionItem *bridgeSession) *acpsdk.SessionModeState {
	values := s.availableThinkingLevels(sessionItem)
	modes := make([]acpsdk.SessionMode, 0, len(values))
	for _, value := range values {
		name := strings.ToUpper(value[:1]) + value[1:]
		var description *string
		if value == "xhigh" {
			name = "Extra High"
		}
		if value == "adaptive" {
			text := "Use the Gateway session default thought level."
			description = &text
		}
		modes = append(modes, acpsdk.SessionMode{Id: acpsdk.SessionModeId(value), Name: name, Description: description})
	}
	return &acpsdk.SessionModeState{
		CurrentModeId:  acpsdk.SessionModeId(defaultString(sessionItem.ThinkingLevel, "adaptive")),
		AvailableModes: modes,
	}
}

func (s *ACPBridgeServer) buildConfigOptions(sessionItem *bridgeSession) []acpSessionConfigOption {
	return []acpSessionConfigOption{
		{Type: "select", ID: "thought_level", Name: "Thought Level", Category: "thought_level", Description: "Controls how much deliberate reasoning Kocort requests from the Gateway model.", CurrentValue: defaultString(sessionItem.ThinkingLevel, "adaptive"), Options: buildThinkingLevelOptions(s.availableThinkingLevels(sessionItem))},
		{Type: "select", ID: "fast_mode", Name: "Fast Mode", Description: "Controls whether OpenAI sessions use the Gateway fast-mode profile.", CurrentValue: defaultString(sessionItem.FastMode, "off"), Options: []acpSessionConfigSelectOpt{{Value: "off", Name: "Off"}, {Value: "on", Name: "On"}}},
		{Type: "select", ID: "verbose_level", Name: "Tool Verbosity", Description: "Controls how much tool progress and output detail Kocort keeps enabled for the session.", CurrentValue: defaultString(sessionItem.VerboseLevel, "off"), Options: []acpSessionConfigSelectOpt{{Value: "off", Name: "Off"}, {Value: "on", Name: "On"}, {Value: "full", Name: "Full"}}},
		{Type: "select", ID: "reasoning_level", Name: "Reasoning Stream", Description: "Controls whether reasoning-capable models emit reasoning text for the session.", CurrentValue: defaultString(sessionItem.ReasoningLevel, "off"), Options: []acpSessionConfigSelectOpt{{Value: "off", Name: "Off"}, {Value: "on", Name: "On"}, {Value: "stream", Name: "Stream"}}},
		{Type: "select", ID: "response_usage", Name: "Usage Detail", Description: "Controls how much usage information Kocort attaches to responses for the session.", CurrentValue: defaultString(sessionItem.ResponseUsage, "off"), Options: []acpSessionConfigSelectOpt{{Value: "off", Name: "Off"}, {Value: "tokens", Name: "Tokens"}, {Value: "full", Name: "Full"}}},
		{Type: "select", ID: "elevated_level", Name: "Elevated Actions", Description: "Controls how aggressively the session allows elevated execution behavior.", CurrentValue: defaultString(sessionItem.ElevatedLevel, "off"), Options: []acpSessionConfigSelectOpt{{Value: "off", Name: "Off"}, {Value: "on", Name: "On"}, {Value: "ask", Name: "Ask"}, {Value: "full", Name: "Full"}}},
	}
}

func (s *ACPBridgeServer) buildSessionSnapshot(sessionItem *bridgeSession) map[string]any {
	return map[string]any{
		"modes":         s.buildModeState(sessionItem),
		"configOptions": s.buildConfigOptions(sessionItem),
	}
}

func (s *ACPBridgeServer) availableThinkingLevels(sessionItem *bridgeSession) []string {
	values := []string{"off", "minimal", "low", "medium", "high", "adaptive"}
	providerID, modelID := s.resolveCurrentModelRef(sessionItem)
	if backend.SupportsXHighThinking(providerID, modelID) {
		values = []string{"off", "minimal", "low", "medium", "high", "xhigh", "adaptive"}
	}
	current := defaultString(sessionItem.ThinkingLevel, "adaptive")
	if current != "" && !containsString(values, current) {
		values = append(values, current)
	}
	return values
}

func buildThinkingLevelOptions(values []string) []acpSessionConfigSelectOpt {
	options := make([]acpSessionConfigSelectOpt, 0, len(values))
	for _, value := range values {
		name := strings.ToUpper(value[:1]) + value[1:]
		if value == "xhigh" {
			name = "Extra High"
		}
		options = append(options, acpSessionConfigSelectOpt{Value: value, Name: name})
	}
	return options
}

func (s *ACPBridgeServer) resolveCurrentModelRef(sessionItem *bridgeSession) (string, string) {
	raw := strings.TrimSpace(sessionItem.ModelID)
	if raw != "" {
		if parsed, ok := backend.ParseModelRef(raw, ""); ok {
			return parsed.Provider, parsed.Model
		}
	}
	if row := s.gateway.GetSessionRow(sessionItem.SessionKey); row != nil {
		if strings.TrimSpace(row.ModelProvider) != "" && strings.TrimSpace(row.Model) != "" {
			return strings.TrimSpace(row.ModelProvider), strings.TrimSpace(row.Model)
		}
	}
	for providerID, providerCfg := range s.runtime.Config.Models.Providers {
		if len(providerCfg.Models) == 0 {
			continue
		}
		return providerID, providerCfg.Models[0].ID
	}
	return "", ""
}

func (s *ACPBridgeServer) replayTranscript(ctx context.Context, sessionItem *bridgeSession) error {
	history, err := s.gateway.LoadTranscript(sessionItem.SessionKey)
	if err != nil {
		return err
	}
	for _, msg := range history {
		switch strings.TrimSpace(msg.Role) {
		case "user":
			if text := strings.TrimSpace(msg.Text); text != "" {
				if err := s.sendRawUpdate(ctx, sessionItem.SessionID, map[string]any{
					"sessionUpdate": "user_message_chunk",
					"content":       map[string]any{"type": "text", "text": text},
				}); err != nil {
					return err
				}
			}
		case "assistant":
			if text := strings.TrimSpace(msg.Text); text != "" {
				if err := s.sendRawUpdate(ctx, sessionItem.SessionID, map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": text},
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *ACPBridgeServer) assertSessionSetupSupported(mcpServers []acpsdk.McpServer) *acpsdk.RequestError {
	if len(mcpServers) == 0 {
		return nil
	}
	return acpsdk.NewInvalidParams(map[string]any{"error": "ACP bridge mode does not support per-session MCP servers. Configure MCP on the Kocort gateway or agent instead."})
}

func (s *ACPBridgeServer) boolOption(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func boolRef(value bool) *bool {
	return &value
}

func (s *ACPBridgeServer) enforceSessionCreateRateLimit(method string) *acpsdk.RequestError {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	cutoff := now.Add(-acpSessionCreateWindow)
	filtered := s.created[:0]
	for _, createdAt := range s.created {
		if createdAt.After(cutoff) {
			filtered = append(filtered, createdAt)
		}
	}
	s.created = filtered
	if len(s.created) >= acpSessionCreateLimit {
		retryAfter := s.created[0].Add(acpSessionCreateWindow).Sub(now).Round(time.Second)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return acpsdk.NewInvalidParams(map[string]any{"error": fmt.Sprintf("ACP session creation rate limit exceeded for %s; retry after %s", method, retryAfter)})
	}
	s.created = append(s.created, now)
	return nil
}

func (s *ACPBridgeServer) reapIdleSessionsLocked(now time.Time) {
	cutoff := now.Add(-acpIdleSessionTTL)
	for sessionID, item := range s.sessions {
		if item == nil || item.CurrentRunID != "" {
			continue
		}
		if item.LastTouchedAt.IsZero() || item.LastTouchedAt.Before(cutoff) {
			delete(s.sessions, sessionID)
		}
	}
}

func (s *ACPBridgeServer) evictOldestIdleSessionLocked() bool {
	var oldestID string
	var oldestAt time.Time
	for sessionID, item := range s.sessions {
		if item == nil || item.CurrentRunID != "" {
			continue
		}
		if oldestID == "" || item.LastTouchedAt.Before(oldestAt) {
			oldestID = sessionID
			oldestAt = item.LastTouchedAt
		}
	}
	if oldestID == "" {
		return false
	}
	delete(s.sessions, oldestID)
	return true
}

func (s *ACPBridgeServer) trackToolCall(sessionItem *bridgeSession, id string, snapshot toolCallSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item := s.sessions[sessionItem.SessionID]; item != nil {
		item.SentToolCalls[id] = snapshot
	}
}

func (s *ACPBridgeServer) buildSystemInputProvenance(sessionItem *bridgeSession) map[string]any {
	mode := normalizeProvenanceMode(s.opts.ProvenanceMode)
	if mode == "off" {
		return nil
	}
	provenance := map[string]any{
		"kind":            "external_user",
		"bridge":          "kocort-acp",
		"sourceChannel":   "acp",
		"sourceTool":      "kocort-acp",
		"originSessionId": sessionItem.SessionID,
		"targetSession":   sessionItem.SessionKey,
	}
	if cwd := defaultString(shortenHomePath(sessionItem.Cwd)); cwd != "" {
		provenance["originCwd"] = cwd
	}
	return provenance
}

func (s *ACPBridgeServer) buildSystemProvenanceReceipt(sessionItem *bridgeSession) string {
	if normalizeProvenanceMode(s.opts.ProvenanceMode) != "meta+receipt" {
		return ""
	}
	hostName, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostName) == "" {
		hostName = "unknown"
	}
	displayCwd := defaultString(shortenHomePath(strings.TrimSpace(sessionItem.Cwd)), ".")
	return strings.Join([]string{
		"[Source Receipt]",
		"bridge=kocort-acp",
		fmt.Sprintf("originHost=%s", hostName),
		fmt.Sprintf("originCwd=%s", displayCwd),
		fmt.Sprintf("acpSessionId=%s", sessionItem.SessionID),
		fmt.Sprintf("originSessionId=%s", sessionItem.SessionID),
		fmt.Sprintf("targetSession=%s", sessionItem.SessionKey),
		"[/Source Receipt]",
	}, "\n")
}

func parseACPBridgeMeta(value any) acpSessionMeta {
	if value == nil {
		return acpSessionMeta{}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return acpSessionMeta{}
	}
	var meta acpSessionMeta
	_ = json.Unmarshal(data, &meta)
	record, _ := value.(map[string]any)
	meta.SessionKey = defaultString(meta.SessionKey, stringValue(firstValue(record, "session", "key")))
	meta.SessionLabel = defaultString(meta.SessionLabel, stringValue(firstValue(record, "label")))
	if meta.ResetSession == nil {
		meta.ResetSession = boolPointer(firstBool(record, "resetSession", "reset"))
	}
	if meta.RequireExisting == nil {
		meta.RequireExisting = boolPointer(firstBool(record, "requireExistingSession", "requireExisting"))
	}
	if meta.PrefixCwd == nil {
		meta.PrefixCwd = boolPointer(firstBool(record, "prefixCwd"))
	}
	if meta.ThinkingLevel == "" {
		meta.ThinkingLevel = stringValue(firstValue(record, "thinkingLevel", "thinking"))
	}
	if meta.TimeoutMs == 0 {
		meta.TimeoutMs = parseAnyInt(firstValue(record, "timeoutMs"))
	}
	return meta
}

func flattenACPPrompt(blocks []acpsdk.ContentBlock) (string, []core.Attachment, error) {
	parts := make([]string, 0, len(blocks))
	attachments := make([]core.Attachment, 0)
	totalBytes := 0
	for _, block := range blocks {
		switch {
		case block.Text != nil:
			totalBytes += utf8JoinedByteCost(parts, block.Text.Text)
			if totalBytes > acpMaxPromptBytes {
				return "", nil, fmt.Errorf("prompt exceeds maximum allowed size of %d bytes", acpMaxPromptBytes)
			}
			parts = append(parts, block.Text.Text)
		case block.Resource != nil:
			if block.Resource.Resource.TextResourceContents != nil {
				resourceText := block.Resource.Resource.TextResourceContents.Text
				totalBytes += utf8JoinedByteCost(parts, resourceText)
				if totalBytes > acpMaxPromptBytes {
					return "", nil, fmt.Errorf("prompt exceeds maximum allowed size of %d bytes", acpMaxPromptBytes)
				}
				parts = append(parts, resourceText)
			}
		case block.ResourceLink != nil:
			text := formatResourceLink(block.ResourceLink.Name, block.ResourceLink.Uri)
			totalBytes += utf8JoinedByteCost(parts, text)
			if totalBytes > acpMaxPromptBytes {
				return "", nil, fmt.Errorf("prompt exceeds maximum allowed size of %d bytes", acpMaxPromptBytes)
			}
			parts = append(parts, text)
		case block.Image != nil:
			raw, err := base64.StdEncoding.DecodeString(block.Image.Data)
			if err != nil {
				return "", nil, fmt.Errorf("decode image attachment: %w", err)
			}
			attachments = append(attachments, core.Attachment{
				Type:     "image",
				Name:     "image",
				MIMEType: block.Image.MimeType,
				Content:  raw,
			})
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), attachments, nil
}

func buildToolTitle(data map[string]any) string {
	name := defaultString(stringValue(data["toolName"]), stringValue(data["name"]), "tool")
	rawInput := rawToolInput(data)
	record, _ := rawInput.(map[string]any)
	if len(record) == 0 {
		arguments := defaultString(stringValue(data["arguments"]), stringValue(data["text"]))
		if arguments == "" {
			return name
		}
		if len(arguments) > 120 {
			arguments = arguments[:120] + "..."
		}
		return name + ": " + arguments
	}
	parts := make([]string, 0, len(record))
	for key, value := range record {
		raw := fmt.Sprintf("%v", value)
		if marshaled, err := json.Marshal(value); err == nil && string(marshaled) != `""` {
			raw = string(marshaled)
		}
		if len(raw) > 100 {
			raw = raw[:100] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s: %s", key, raw))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return name
	}
	return name + ": " + strings.Join(parts, ", ")
}

func rawToolInput(data map[string]any) any {
	if value, ok := data["args"]; ok {
		return value
	}
	if raw := stringValue(data["arguments"]); raw != "" {
		var decoded any
		if json.Unmarshal([]byte(raw), &decoded) == nil {
			return decoded
		}
	}
	return nil
}

func extractToolLocations(data map[string]any) []acpsdk.ToolCallLocation {
	locations := map[string]acpsdk.ToolCallLocation{}
	for _, value := range []any{rawToolInput(data), data["result"], data["partialResult"], data["text"], data["error"]} {
		collectToolLocations(value, locations, 0, 0)
	}
	if len(locations) == 0 {
		return nil
	}
	out := make([]acpsdk.ToolCallLocation, 0, len(locations))
	for _, location := range locations {
		out = append(out, location)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return toolLocationLineValue(out[i]) < toolLocationLineValue(out[j])
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func mapToolKind(name string) acpsdk.ToolKind {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(name, "read"), strings.Contains(name, "grep"), strings.Contains(name, "find"), strings.Contains(name, "ls"):
		return acpsdk.ToolKindRead
	case strings.Contains(name, "write"), strings.Contains(name, "edit"), strings.Contains(name, "patch"):
		return acpsdk.ToolKindEdit
	case strings.Contains(name, "delete"), strings.Contains(name, "remove"):
		return acpsdk.ToolKindDelete
	case strings.Contains(name, "move"), strings.Contains(name, "rename"):
		return acpsdk.ToolKindMove
	case strings.Contains(name, "search"):
		return acpsdk.ToolKindSearch
	case strings.Contains(name, "exec"), strings.Contains(name, "run"), strings.Contains(name, "shell"):
		return acpsdk.ToolKindExecute
	case strings.Contains(name, "fetch"), strings.Contains(name, "http"), strings.Contains(name, "web"):
		return acpsdk.ToolKindFetch
	default:
		return acpsdk.ToolKindOther
	}
}

func toolCallContent(text string) []acpsdk.ToolCallContent {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock(text))}
}

func mapStopReason(reason string) acpsdk.StopReason {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "cancel", "cancelled", "canceled", "aborted":
		return acpsdk.StopReasonCancelled
	case "max_tokens":
		return acpsdk.StopReasonMaxTokens
	default:
		return acpsdk.StopReasonEndTurn
	}
}

func resolveACPAgentIDFromSessionKey(sessionKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if strings.HasPrefix(sessionKey, "agent:") {
		parts := strings.Split(sessionKey, ":")
		if len(parts) >= 2 {
			return session.NormalizeAgentID(parts[1])
		}
	}
	return session.NormalizeAgentID(session.ResolveAgentIDFromSessionKey(sessionKey))
}

func synthesizeToolCallID(parts ...string) string {
	value := strings.TrimSpace(strings.Join(parts, ":"))
	if value == "" {
		return "tool-" + session.NewRunID()
	}
	value = strings.ReplaceAll(value, " ", "-")
	return "tool-" + value
}

func parseAnyInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func defaultString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(target) {
			return true
		}
	}
	return false
}

func normalizeToggleValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes", "1", "enable", "enabled", "fast":
		return "on"
	case "off", "false", "no", "0", "disable", "disabled", "normal":
		return "off"
	default:
		return ""
	}
}

func normalizeVerboseValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "full", "all", "everything":
		return "full"
	case "on", "true", "yes", "1", "minimal":
		return "on"
	case "off", "false", "no", "0":
		return "off"
	default:
		return ""
	}
}

func normalizeReasoningValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "stream", "streaming", "draft", "live":
		return "stream"
	case "on", "true", "yes", "1", "show", "visible", "enable", "enabled":
		return "on"
	case "off", "false", "no", "0", "hide", "hidden", "disable", "disabled":
		return "off"
	default:
		return ""
	}
}

func normalizeUsageValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "full", "session":
		return "full"
	case "on", "true", "yes", "1", "tokens", "token", "tok", "minimal", "min", "enable", "enabled":
		return "tokens"
	case "off", "false", "no", "0", "disable", "disabled":
		return "off"
	default:
		return ""
	}
}

func normalizeElevatedValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "full", "auto", "auto-approve", "autoapprove":
		return "full"
	case "ask", "prompt", "approval", "approve":
		return "ask"
	case "on", "true", "yes", "1":
		return "on"
	case "off", "false", "no", "0":
		return "off"
	default:
		return ""
	}
}

func firstValue(record map[string]any, keys ...string) any {
	for _, key := range keys {
		if record == nil {
			return nil
		}
		if value, ok := record[key]; ok {
			return value
		}
	}
	return nil
}

func firstBool(record map[string]any, keys ...string) *bool {
	for _, key := range keys {
		if record == nil {
			return nil
		}
		value, ok := record[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return &typed
		case string:
			trimmed := strings.TrimSpace(strings.ToLower(typed))
			if trimmed == "true" {
				value := true
				return &value
			}
			if trimmed == "false" {
				value := false
				return &value
			}
		}
	}
	return nil
}

func boolPointer(value *bool) *bool {
	return value
}

func boolValuePointer(value bool) *bool {
	return &value
}

func resolvePromptDeliver(meta any) *bool {
	record, _ := meta.(map[string]any)
	if value := firstBool(record, "deliver"); value != nil {
		return value
	}
	return boolValuePointer(true)
}

func toggleString(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func utf8JoinedByteCost(parts []string, next string) int {
	if len(parts) == 0 {
		return len([]byte(next))
	}
	return 1 + len([]byte(next))
}

func formatResourceLink(name, uri string) string {
	title := escapeResourceLinkTitle(strings.TrimSpace(name))
	escapedURI := escapeInlineControlChars(strings.TrimSpace(uri))
	switch {
	case title != "" && escapedURI != "":
		return fmt.Sprintf("[Resource link (%s)] %s", title, escapedURI)
	case escapedURI != "":
		return fmt.Sprintf("[Resource link] %s", escapedURI)
	default:
		return "[Resource link]"
	}
}

func escapeResourceLinkTitle(value string) string {
	return strings.NewReplacer("[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)").Replace(escapeInlineControlChars(value))
}

func escapeInlineControlChars(value string) string {
	replacer := strings.NewReplacer(
		"\x00", "\\0",
		"\r", "\\r",
		"\n", "\\n",
		"\t", "\\t",
		"\v", "\\v",
		"\f", "\\f",
		"\u2028", "\\u2028",
		"\u2029", "\\u2029",
	)
	return replacer.Replace(value)
}

func collectToolLocations(value any, locations map[string]acpsdk.ToolCallLocation, depth int, visited int) int {
	if visited >= 100 || depth > 4 {
		return visited
	}
	visited++
	switch typed := value.(type) {
	case string:
		for _, line := range strings.Split(typed, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "FILE:") || strings.HasPrefix(line, "MEDIA:") {
				addToolLocation(locations, strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), 0)
			}
		}
		return visited
	case []any:
		for _, item := range typed {
			visited = collectToolLocations(item, locations, depth+1, visited)
			if visited >= 100 {
				return visited
			}
		}
		return visited
	case map[string]any:
		line := extractLocationLine(typed)
		for _, key := range []string{"path", "file", "filePath", "file_path", "targetPath", "target_path", "sourcePath", "source_path", "destinationPath", "destination_path", "oldPath", "newPath", "outputPath", "inputPath"} {
			if path := stringValue(typed[key]); path != "" {
				addToolLocation(locations, path, line)
			}
		}
		if content, ok := typed["content"].([]any); ok {
			for _, block := range content {
				if entry, ok := block.(map[string]any); ok {
					if text := stringValue(entry["text"]); text != "" {
						visited = collectToolLocations(text, locations, depth+1, visited)
					}
				}
			}
		}
		for key, nested := range typed {
			if key == "content" {
				continue
			}
			visited = collectToolLocations(nested, locations, depth+1, visited)
			if visited >= 100 {
				return visited
			}
		}
		return visited
	default:
		return visited
	}
}

func extractLocationLine(record map[string]any) int {
	for _, key := range []string{"line", "lineNumber", "line_number", "startLine", "start_line"} {
		if line := parseAnyInt(record[key]); line > 0 {
			return line
		}
	}
	return 0
}

func addToolLocation(locations map[string]acpsdk.ToolCallLocation, rawPath string, line int) {
	path := normalizeLocationPath(rawPath)
	if path == "" {
		return
	}
	key := path
	location := acpsdk.ToolCallLocation{Path: path}
	if line > 0 {
		key = fmt.Sprintf("%s:%d", path, line)
		location.Line = intPointer(line)
	}
	locations[key] = location
}

func normalizeLocationPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "\x00") || strings.Contains(value, "\r") || strings.Contains(value, "\n") {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://") {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "file://") {
		value = strings.TrimPrefix(strings.TrimPrefix(value, "file://"), "file://")
	}
	return value
}

func normalizeProvenanceMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off":
		return "off"
	case "meta":
		return "meta"
	case "meta+receipt":
		return "meta+receipt"
	default:
		return "off"
	}
}

func shortenHomePath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Clean(trimmed)
	}
	normalizedValue := filepath.Clean(trimmed)
	normalizedHome := filepath.Clean(strings.TrimSpace(home))
	if normalizedHome == "" {
		return normalizedValue
	}
	lowerValue := strings.ToLower(normalizedValue)
	lowerHome := strings.ToLower(normalizedHome)
	if !strings.EqualFold(normalizedValue, normalizedHome) && !strings.HasPrefix(lowerValue, lowerHome+strings.ToLower(string(os.PathSeparator))) {
		return normalizedValue
	}
	if strings.EqualFold(normalizedValue, normalizedHome) {
		return "~"
	}
	suffix := strings.TrimPrefix(normalizedValue[len(normalizedHome):], string(os.PathSeparator))
	if suffix == "" {
		return "~"
	}
	return "~" + string(os.PathSeparator) + suffix
}

func toolLocationLineValue(location acpsdk.ToolCallLocation) int {
	if location.Line == nil {
		return 0
	}
	return *location.Line
}

func intPointer(value int) *int {
	return &value
}

func defaultSessionLabel(label, sessionKey string) string {
	if strings.TrimSpace(label) != "" {
		return strings.TrimSpace(label)
	}
	if strings.TrimSpace(sessionKey) == "" {
		return "ACP Session"
	}
	return filepath.Base(strings.TrimSpace(sessionKey))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
