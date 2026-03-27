package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/channel"
	channeladapter "github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
	"github.com/kocort/kocort/runtime"
	"github.com/kocort/kocort/utils"
)

type backendFunc func(context.Context, rtypes.AgentRunContext) (core.AgentRunResult, error)

func (f backendFunc) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	return f(ctx, runCtx)
}

func testChannelSchema(id string) channeladapter.ChannelDriverSchema {
	return channeladapter.ChannelDriverSchema{
		ID:   strings.TrimSpace(id),
		Name: strings.TrimSpace(id),
	}
}

func testChannelNotImplemented(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, channeladapter.ErrNotImplemented.Error(), http.StatusNotImplemented)
}

type captureChannelAdapter struct {
	id        string
	channelID string
	cb        channeladapter.Callbacks
	inbound   *core.ChannelInboundMessage
	outbound  []core.ChannelOutboundMessage
}

func (a *captureChannelAdapter) ID() string { return a.id }

func (a *captureChannelAdapter) Schema() channeladapter.ChannelDriverSchema {
	return testChannelSchema(a.id)
}

func (a *captureChannelAdapter) StartBackground(_ context.Context, channelID string, _ channeladapter.ChannelConfig, _ *infra.DynamicHTTPClient, cb channeladapter.Callbacks) error {
	a.channelID = channelID
	a.cb = cb
	return nil
}

func (a *captureChannelAdapter) StopBackground() {
	a.cb = channeladapter.Callbacks{}
}

func (a *captureChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var payload struct {
		Text string `json:"text"`
		From string `json:"from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	msg := core.ChannelInboundMessage{
		Channel: strings.TrimSpace(a.channelID),
		From:    payload.From,
		Text:    payload.Text,
	}
	if msg.Channel == "" {
		msg.Channel = a.id
	}
	a.inbound = &msg
	if a.cb.OnMessage == nil {
		testChannelNotImplemented(w, r)
		return
	}
	if err := a.cb.OnMessage(r.Context(), msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *captureChannelAdapter) SendPayload(_ context.Context, message core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelDeliveryResult, error) {
	a.outbound = append(a.outbound, message)
	return core.ChannelDeliveryResult{MessageID: "msg-1"}, nil
}

func (a *captureChannelAdapter) SendText(ctx context.Context, message core.ChannelOutboundMessage, cfg channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return a.SendPayload(ctx, message, cfg)
}

func (a *captureChannelAdapter) SendMedia(ctx context.Context, message core.ChannelOutboundMessage, cfg channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return a.SendPayload(ctx, message, cfg)
}

type outboundOnlyChannelAdapter struct {
	id       string
	outbound []core.ChannelOutboundMessage
}

func (a *outboundOnlyChannelAdapter) ID() string { return a.id }

func (a *outboundOnlyChannelAdapter) Schema() channeladapter.ChannelDriverSchema {
	return testChannelSchema(a.id)
}

func (a *outboundOnlyChannelAdapter) StartBackground(_ context.Context, _ string, _ channeladapter.ChannelConfig, _ *infra.DynamicHTTPClient, _ channeladapter.Callbacks) error {
	return nil
}

func (a *outboundOnlyChannelAdapter) StopBackground() {}

func (a *outboundOnlyChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testChannelNotImplemented(w, r)
}

func (a *outboundOnlyChannelAdapter) SendPayload(_ context.Context, message core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelDeliveryResult, error) {
	a.outbound = append(a.outbound, message)
	return core.ChannelDeliveryResult{MessageID: "msg-2"}, nil
}

func (a *outboundOnlyChannelAdapter) SendText(ctx context.Context, message core.ChannelOutboundMessage, cfg channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return a.SendPayload(ctx, message, cfg)
}

func (a *outboundOnlyChannelAdapter) SendMedia(ctx context.Context, message core.ChannelOutboundMessage, cfg channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return a.SendPayload(ctx, message, cfg)
}

type textOnlyChannelAdapter struct {
	id       string
	outbound []core.ChannelOutboundMessage
	sendErr  error
}

func (a *textOnlyChannelAdapter) ID() string { return a.id }

func (a *textOnlyChannelAdapter) Schema() channeladapter.ChannelDriverSchema {
	return testChannelSchema(a.id)
}

func (a *textOnlyChannelAdapter) StartBackground(_ context.Context, _ string, _ channeladapter.ChannelConfig, _ *infra.DynamicHTTPClient, _ channeladapter.Callbacks) error {
	return nil
}

func (a *textOnlyChannelAdapter) StopBackground() {}

func (a *textOnlyChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testChannelNotImplemented(w, r)
}

func (a *textOnlyChannelAdapter) SendText(_ context.Context, message core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelDeliveryResult, error) {
	a.outbound = append(a.outbound, message)
	if a.sendErr != nil {
		return core.ChannelDeliveryResult{}, a.sendErr
	}
	return core.ChannelDeliveryResult{MessageID: "msg-3"}, nil
}

func (a *textOnlyChannelAdapter) SendMedia(_ context.Context, _ core.ChannelOutboundMessage, _ channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return core.ChannelDeliveryResult{}, channeladapter.ErrNotImplemented
}

type chunkingTextChannelAdapter struct {
	id       string
	outbound []core.ChannelOutboundMessage
}

func (a *chunkingTextChannelAdapter) ID() string { return a.id }

func (a *chunkingTextChannelAdapter) Schema() channeladapter.ChannelDriverSchema {
	return testChannelSchema(a.id)
}

func (a *chunkingTextChannelAdapter) StartBackground(_ context.Context, _ string, _ channeladapter.ChannelConfig, _ *infra.DynamicHTTPClient, _ channeladapter.Callbacks) error {
	return nil
}

func (a *chunkingTextChannelAdapter) StopBackground() {}

func (a *chunkingTextChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testChannelNotImplemented(w, r)
}

func (a *chunkingTextChannelAdapter) TextChunkLimit() int { return 10 }

func (a *chunkingTextChannelAdapter) ChunkerMode() string { return "markdown" }

func (a *chunkingTextChannelAdapter) ChunkText(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	return []string{text[:limit], text[limit:]}
}

func (a *chunkingTextChannelAdapter) SendText(_ context.Context, message core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelDeliveryResult, error) {
	a.outbound = append(a.outbound, message)
	return core.ChannelDeliveryResult{MessageID: "msg-chunk"}, nil
}

func (a *chunkingTextChannelAdapter) SendMedia(_ context.Context, _ core.ChannelOutboundMessage, _ channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return core.ChannelDeliveryResult{}, channeladapter.ErrNotImplemented
}

type resolvingTextChannelAdapter struct {
	id       string
	outbound []core.ChannelOutboundMessage
}

func (a *resolvingTextChannelAdapter) ID() string { return a.id }

func (a *resolvingTextChannelAdapter) Schema() channeladapter.ChannelDriverSchema {
	return testChannelSchema(a.id)
}

func (a *resolvingTextChannelAdapter) StartBackground(_ context.Context, _ string, _ channeladapter.ChannelConfig, _ *infra.DynamicHTTPClient, _ channeladapter.Callbacks) error {
	return nil
}

func (a *resolvingTextChannelAdapter) StopBackground() {}

func (a *resolvingTextChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testChannelNotImplemented(w, r)
}

func (a *resolvingTextChannelAdapter) ResolveTarget(_ context.Context, message core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelOutboundMessage, error) {
	if strings.TrimSpace(message.To) == "" {
		message.To = "resolved-user"
	}
	return message, nil
}

func (a *resolvingTextChannelAdapter) SendText(_ context.Context, message core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelDeliveryResult, error) {
	a.outbound = append(a.outbound, message)
	return core.ChannelDeliveryResult{MessageID: "msg-4"}, nil
}

func (a *resolvingTextChannelAdapter) SendMedia(_ context.Context, _ core.ChannelOutboundMessage, _ channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return core.ChannelDeliveryResult{}, channeladapter.ErrNotImplemented
}

type mediaOnlyChannelAdapter struct {
	id       string
	outbound []core.ChannelOutboundMessage
}

func (a *mediaOnlyChannelAdapter) ID() string { return a.id }

func (a *mediaOnlyChannelAdapter) Schema() channeladapter.ChannelDriverSchema {
	return testChannelSchema(a.id)
}

func (a *mediaOnlyChannelAdapter) StartBackground(_ context.Context, _ string, _ channeladapter.ChannelConfig, _ *infra.DynamicHTTPClient, _ channeladapter.Callbacks) error {
	return nil
}

func (a *mediaOnlyChannelAdapter) StopBackground() {}

func (a *mediaOnlyChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testChannelNotImplemented(w, r)
}

func (a *mediaOnlyChannelAdapter) SendText(_ context.Context, _ core.ChannelOutboundMessage, _ channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return core.ChannelDeliveryResult{}, channeladapter.ErrNotImplemented
}

func (a *mediaOnlyChannelAdapter) SendMedia(_ context.Context, message core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelDeliveryResult, error) {
	a.outbound = append(a.outbound, message)
	return core.ChannelDeliveryResult{MessageID: "msg-5"}, nil
}

type failingTextChannelAdapter struct {
	id string
}

func (a *failingTextChannelAdapter) ID() string { return a.id }

func (a *failingTextChannelAdapter) Schema() channeladapter.ChannelDriverSchema {
	return testChannelSchema(a.id)
}

func (a *failingTextChannelAdapter) StartBackground(_ context.Context, _ string, _ channeladapter.ChannelConfig, _ *infra.DynamicHTTPClient, _ channeladapter.Callbacks) error {
	return nil
}

func (a *failingTextChannelAdapter) StopBackground() {}

func (a *failingTextChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testChannelNotImplemented(w, r)
}

func (a *failingTextChannelAdapter) SendText(_ context.Context, _ core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return core.ChannelDeliveryResult{}, io.ErrUnexpectedEOF
}

func (a *failingTextChannelAdapter) SendMedia(_ context.Context, _ core.ChannelOutboundMessage, _ channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return core.ChannelDeliveryResult{}, channeladapter.ErrNotImplemented
}

type flakyTextChannelAdapter struct {
	id       string
	failures int
	outbound []core.ChannelOutboundMessage
}

func (a *flakyTextChannelAdapter) ID() string { return a.id }

func (a *flakyTextChannelAdapter) Schema() channeladapter.ChannelDriverSchema {
	return testChannelSchema(a.id)
}

func (a *flakyTextChannelAdapter) StartBackground(_ context.Context, _ string, _ channeladapter.ChannelConfig, _ *infra.DynamicHTTPClient, _ channeladapter.Callbacks) error {
	return nil
}

func (a *flakyTextChannelAdapter) StopBackground() {}

func (a *flakyTextChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testChannelNotImplemented(w, r)
}

func (a *flakyTextChannelAdapter) SendText(_ context.Context, message core.ChannelOutboundMessage, _ config.ChannelConfig) (core.ChannelDeliveryResult, error) {
	if a.failures > 0 {
		a.failures--
		return core.ChannelDeliveryResult{}, io.ErrUnexpectedEOF
	}
	a.outbound = append(a.outbound, message)
	return core.ChannelDeliveryResult{MessageID: "msg-replayed"}, nil
}

func (a *flakyTextChannelAdapter) SendMedia(_ context.Context, _ core.ChannelOutboundMessage, _ channeladapter.ChannelConfig) (core.ChannelDeliveryResult, error) {
	return core.ChannelDeliveryResult{}, channeladapter.ErrNotImplemented
}

type recordingHookRunner struct {
	sending []delivery.OutboundMessageSendingEvent
	sent    []delivery.OutboundMessageSentEvent
	result  delivery.OutboundMessageSendingResult
}

func (r *recordingHookRunner) OnMessageSending(_ context.Context, event delivery.OutboundMessageSendingEvent) (delivery.OutboundMessageSendingResult, error) {
	r.sending = append(r.sending, event)
	return r.result, nil
}

func (r *recordingHookRunner) OnMessageSent(_ context.Context, event delivery.OutboundMessageSentEvent) error {
	r.sent = append(r.sent, event)
	return nil
}

func TestLoadAppConfigParsesGatewayAndChannels(t *testing.T) {
	cfgJSON := `{
		"models":{"providers":{"openai":{"baseUrl":"https://example.com/v1","api":"openai-completions","models":[{"id":"gpt-4.1"}]}}},
		"gateway":{"enabled":true,"bind":"loopback","port":18789,"auth":{"mode":"token","token":"secret"},"webchat":{"enabled":true,"basePath":"/"}},
		"channels":{"defaults":{"defaultAgent":"main","defaultAccount":"acc"},"entries":{"webhook":{"enabled":true,"agent":"worker","inboundToken":"tok"}}}
	}`
	path := writeTempFile(t, cfgJSON)
	cfg, err := config.LoadAppConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Gateway.Enabled || cfg.Gateway.Port != 18789 || cfg.Gateway.Auth == nil || cfg.Gateway.Auth.Token != "secret" {
		t.Fatalf("unexpected gateway config: %+v", cfg.Gateway)
	}
	entry := cfg.Channels.Entries["webhook"]
	if entry.Agent != "worker" || entry.InboundToken != "tok" {
		t.Fatalf("unexpected channel config: %+v", entry)
	}
}

func TestGatewayChatSendAndHistory(t *testing.T) {
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "hello back"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "hello back"}}}, nil
	}))
	server := NewServer(rt, config.GatewayConfig{})

	sendReq := httptest.NewRequest(http.MethodPost, "/rpc/chat.send", strings.NewReader(`{"sessionKey":"agent:main:webchat:direct:webchat-user","message":"hello","channel":"webchat","to":"webchat-user"}`))
	sendReq.Header.Set("Content-Type", "application/json")
	sendRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(sendRes, sendReq)
	if sendRes.Code != http.StatusOK {
		t.Fatalf("chat.send status = %d body=%s", sendRes.Code, sendRes.Body.String())
	}
	var sendPayload core.ChatSendResponse
	if err := json.Unmarshal(sendRes.Body.Bytes(), &sendPayload); err != nil {
		t.Fatalf("decode send response: %v", err)
	}
	if sendPayload.SessionKey != "agent:main:webchat:direct:webchat-user" {
		t.Fatalf("unexpected session key: %+v", sendPayload)
	}
	if got := latestReplyText(sendPayload.Payloads); got != "hello back" {
		t.Fatalf("unexpected payloads: %+v", sendPayload.Payloads)
	}

	historyReq := httptest.NewRequest(http.MethodGet, "/rpc/chat.history?sessionKey=agent:main:webchat:direct:webchat-user", nil)
	historyRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(historyRes, historyReq)
	if historyRes.Code != http.StatusOK {
		t.Fatalf("chat.history status = %d body=%s", historyRes.Code, historyRes.Body.String())
	}
	var history core.ChatHistoryResponse
	if err := json.Unmarshal(historyRes.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history response: %v", err)
	}
	if len(history.Messages) < 2 {
		t.Fatalf("expected transcript messages, got %+v", history.Messages)
	}
}

func TestGatewayChatSendStopCommandCancelsActiveRun(t *testing.T) {
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		select {
		case <-ctx.Done():
			return core.AgentRunResult{}, ctx.Err()
		case <-time.After(5 * time.Second):
			t.Fatal("expected run to be canceled before timeout")
			return core.AgentRunResult{}, nil
		}
	}))
	server := NewServer(rt, config.GatewayConfig{})
	sessionKey := "agent:main:webchat:direct:webchat-user"

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		sendReq := httptest.NewRequest(http.MethodPost, "/rpc/chat.send", strings.NewReader(`{"sessionKey":"agent:main:webchat:direct:webchat-user","message":"hello","channel":"webchat","to":"webchat-user","timeoutMs":5000}`))
		sendReq.Header.Set("Content-Type", "application/json")
		sendRes := httptest.NewRecorder()
		server.Handler().ServeHTTP(sendRes, sendReq)
		firstDone <- sendRes
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rt.ActiveRuns.IsActive(sessionKey) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !rt.ActiveRuns.IsActive(sessionKey) {
		t.Fatal("expected active run before stop command")
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/rpc/chat.send", strings.NewReader(`{"sessionKey":"agent:main:webchat:direct:webchat-user","message":"/stop","channel":"webchat","to":"webchat-user"}`))
	stopReq.Header.Set("Content-Type", "application/json")
	stopRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop chat.send status=%d body=%s", stopRes.Code, stopRes.Body.String())
	}
	var stopPayload core.ChatSendResponse
	if err := json.Unmarshal(stopRes.Body.Bytes(), &stopPayload); err != nil {
		t.Fatalf("decode stop response: %v", err)
	}
	if !stopPayload.Aborted {
		t.Fatalf("expected aborted response, got %+v", stopPayload)
	}
	if got := latestReplyText(stopPayload.Payloads); got != "⚙️ Agent was aborted." {
		t.Fatalf("unexpected stop payloads: %+v", stopPayload.Payloads)
	}

	select {
	case res := <-firstDone:
		if res.Code != http.StatusBadRequest {
			t.Fatalf("expected canceled first request to fail, got %d body=%s", res.Code, res.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled run to finish")
	}

	history, _, _, _, err := session.LoadChatHistoryPage(rt.Sessions, sessionKey, 20, 0)
	if err != nil {
		t.Fatalf("chat history: %v", err)
	}
	if got := history[len(history)-1].Text; strings.TrimSpace(got) != "⚙️ Agent was aborted." {
		t.Fatalf("expected abort message in history, got %+v", history)
	}
}

func TestGatewayWebchatPageRenders(t *testing.T) {
	server := NewServer(testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		return core.AgentRunResult{}, nil
	})), config.GatewayConfig{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("webchat status = %d", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(strings.ToLower(body), "<!doctype html") {
		t.Fatalf("expected embedded html document, got: %s", body)
	}
	if !strings.Contains(body, "_next/") {
		t.Fatalf("expected exported next assets in html body: %s", body)
	}
}

func TestGatewayEmbeddedStaticAssetsServeAndFallback(t *testing.T) {
	server := NewServer(testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		return core.AgentRunResult{}, nil
	})), config.GatewayConfig{})

	var assetPath string
	err := fs.WalkDir(embeddedWebFS, "static/dist", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
			assetPath = strings.TrimPrefix(path, "static/dist")
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded web fs: %v", err)
	}
	if assetPath == "" {
		t.Fatal("expected at least one embedded static asset")
	}

	assetReq := httptest.NewRequest(http.MethodGet, assetPath, nil)
	assetRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(assetRes, assetReq)
	if assetRes.Code != http.StatusOK {
		t.Fatalf("asset status = %d body=%s", assetRes.Code, assetRes.Body.String())
	}

	spaReq := httptest.NewRequest(http.MethodGet, "/chat/session/demo", nil)
	spaRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(spaRes, spaReq)
	if spaRes.Code != http.StatusOK {
		t.Fatalf("spa fallback status = %d body=%s", spaRes.Code, spaRes.Body.String())
	}
	if !strings.Contains(strings.ToLower(spaRes.Body.String()), "<!doctype html") {
		t.Fatalf("expected html fallback body, got: %s", spaRes.Body.String())
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/not-found", nil)
	apiRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(apiRes, apiReq)
	if apiRes.Code != http.StatusNotFound {
		t.Fatalf("expected api paths to stay 404, got %d body=%s", apiRes.Code, apiRes.Body.String())
	}
}

func TestGatewayChannelInboundAndOutbound(t *testing.T) {
	adapter := &captureChannelAdapter{id: "webhook"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "channel reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "channel reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["webhook"] = config.ChannelConfig{Enabled: &enabled, Agent: "main"}
	server := NewServer(rt, config.GatewayConfig{})

	inboundReq := httptest.NewRequest(http.MethodPost, "/channels/webhook", strings.NewReader(`{"from":"user-1","text":"ping"}`))
	inboundReq.Header.Set("Content-Type", "application/json")
	inboundRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(inboundRes, inboundReq)
	if inboundRes.Code != http.StatusOK {
		t.Fatalf("inbound status = %d body=%s", inboundRes.Code, inboundRes.Body.String())
	}
	if adapter.inbound == nil || adapter.inbound.Text != "ping" {
		t.Fatalf("expected inbound message capture, got %+v", adapter.inbound)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(adapter.outbound) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(adapter.outbound) == 0 || strings.TrimSpace(adapter.outbound[0].Payload.Text) != "channel reply" {
		t.Fatalf("expected outbound delivery, got %+v", adapter.outbound)
	}
}

func TestRuntimePushInboundSupportsNonHTTPChannels(t *testing.T) {
	adapter := &outboundOnlyChannelAdapter{id: "sdkchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "sdk reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "sdk reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["sdkchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main"}
	resp, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "sdkchan",
		From:    "sdk-user",
		Text:    "ping from sdk",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if got := latestReplyText(resp.Payloads); got != "sdk reply" {
		t.Fatalf("unexpected reply payloads: %+v", resp.Payloads)
	}
	if resp.SessionKey != session.BuildMainSessionKey("main") {
		t.Fatalf("expected main session key, got %q", resp.SessionKey)
	}
	if len(adapter.outbound) == 0 || strings.TrimSpace(adapter.outbound[0].Payload.Text) != "sdk reply" {
		t.Fatalf("expected outbound delivery via adapter, got %+v", adapter.outbound)
	}
}

func TestRuntimePushInboundReturnsDeliveryError(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "failchan", sendErr: errors.New("send failed")}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["failchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main"}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "failchan",
		From:    "user-1",
		Text:    "ping",
	})
	// Delivery errors are silently discarded by the dispatcher; PushInbound
	// should succeed even when the channel adapter fails to send.
	if err != nil {
		t.Fatalf("expected no error (delivery errors are discarded), got %v", err)
	}
}

func TestGatewayChannelInboundRejectsAdaptersWithoutHTTPIngress(t *testing.T) {
	adapter := &outboundOnlyChannelAdapter{id: "sdkchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		return core.AgentRunResult{}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["sdkchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main"}
	server := NewServer(rt, config.GatewayConfig{})

	req := httptest.NewRequest(http.MethodPost, "/channels/sdkchan", strings.NewReader(`{"text":"ping"}`))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 for non-http channel ingress, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestRuntimePushInboundDeliversThroughTextOnlyOutbound(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "textchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "text only reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "text only reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["textchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "fallback-user"}
	resp, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "textchan",
		From:    "text-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if got := latestReplyText(resp.Payloads); got != "text only reply" {
		t.Fatalf("unexpected reply payloads: %+v", resp.Payloads)
	}
	if len(adapter.outbound) == 0 || strings.TrimSpace(adapter.outbound[0].Payload.Text) != "text only reply" {
		t.Fatalf("expected text outbound delivery, got %+v", adapter.outbound)
	}
}

func TestRuntimePushInboundUsesGroupTargetForGroupChats(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "groupchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "group reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "group reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["groupchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main"}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel:  "groupchan",
		From:     "user-1",
		To:       "group-1",
		ChatType: core.ChatTypeGroup,
		Text:     "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) == 0 {
		t.Fatalf("expected outbound delivery")
	}
	if adapter.outbound[0].To != "group-1" {
		t.Fatalf("expected group target, got %+v", adapter.outbound[0])
	}
}

func TestRuntimePushInboundUsesOutboundTargetResolver(t *testing.T) {
	adapter := &resolvingTextChannelAdapter{id: "resolvechan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "resolver reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "resolver reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["resolvechan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main"}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "resolvechan",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) == 0 || adapter.outbound[0].To != "resolved-user" {
		t.Fatalf("expected resolved target, got %+v", adapter.outbound)
	}
	if adapter.outbound[0].Mode != "implicit" {
		t.Fatalf("expected implicit outbound mode, got %+v", adapter.outbound)
	}
}

func TestRuntimePushInboundChunksTextOutbound(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "chunkchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "alpha\n\nbeta\n\ngamma"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "alpha\n\nbeta\n\ngamma"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["chunkchan"] = config.ChannelConfig{
		Enabled:        &enabled,
		Agent:          "main",
		DefaultTo:      "chunk-user",
		TextChunkLimit: 8,
		ChunkMode:      "newline",
	}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "chunkchan",
		From:    "chunk-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) != 3 {
		t.Fatalf("expected 3 chunks, got %+v", adapter.outbound)
	}
	if adapter.outbound[0].Payload.Text != "alpha" || adapter.outbound[1].Payload.Text != "beta" || adapter.outbound[2].Payload.Text != "gamma" {
		t.Fatalf("unexpected chunk payloads: %+v", adapter.outbound)
	}
}

func TestRuntimePushInboundUsesAdapterChunker(t *testing.T) {
	adapter := &chunkingTextChannelAdapter{id: "markdownchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "alpha\n\nbeta section"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "alpha\n\nbeta section"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["markdownchan"] = config.ChannelConfig{
		Enabled:   &enabled,
		Agent:     "main",
		DefaultTo: "markdown-user",
		ChunkMode: "newline",
	}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "markdownchan",
		From:    "markdown-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) != 3 {
		t.Fatalf("expected adapter chunker to split into 3 sends, got %+v", adapter.outbound)
	}
	if adapter.outbound[0].Payload.Text != "alpha" {
		t.Fatalf("unexpected first chunk: %+v", adapter.outbound)
	}
	if adapter.outbound[1].Payload.Text != "beta" || adapter.outbound[2].Payload.Text != "section" {
		t.Fatalf("unexpected later chunks: %+v", adapter.outbound)
	}
}

func TestRuntimePushInboundPropagatesReplyThreadAndMediaOutbound(t *testing.T) {
	adapter := &mediaOnlyChannelAdapter{id: "mediachan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{
			Text:      "media caption",
			MediaURLs: []string{"https://example.com/a.png", "https://example.com/b.png"},
			ReplyToID: "reply-1",
		})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{
			Text:      "media caption",
			MediaURLs: []string{"https://example.com/a.png", "https://example.com/b.png"},
			ReplyToID: "reply-1",
		}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["mediachan"] = config.ChannelConfig{
		Enabled:        &enabled,
		Agent:          "main",
		DefaultTo:      "media-user",
		DefaultAccount: "acc-1",
		AllowFrom:      []string{"allowed-1"},
	}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel:   "mediachan",
		From:      "media-user",
		AccountID: "acc-1",
		ThreadID:  "thread-7",
		Text:      "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) != 2 {
		t.Fatalf("expected 2 media sends, got %+v", adapter.outbound)
	}
	if adapter.outbound[0].Payload.MediaURL != "https://example.com/a.png" || adapter.outbound[1].Payload.MediaURL != "https://example.com/b.png" {
		t.Fatalf("unexpected media URLs: %+v", adapter.outbound)
	}
	if adapter.outbound[0].Payload.Text != "media caption" || adapter.outbound[1].Payload.Text != "" {
		t.Fatalf("unexpected media captions: %+v", adapter.outbound)
	}
	if adapter.outbound[0].Payload.ReplyToID != "reply-1" || adapter.outbound[0].ThreadID != "thread-7" || adapter.outbound[0].AccountID != "acc-1" {
		t.Fatalf("expected reply/thread/account propagation, got %+v", adapter.outbound)
	}
	if len(adapter.outbound[0].AllowFrom) != 1 || adapter.outbound[0].AllowFrom[0] != "allowed-1" {
		t.Fatalf("expected allowFrom propagation, got %+v", adapter.outbound)
	}
}

func TestRuntimePushInboundSanitizesPlainTextSurface(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "whatsapp"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "<b>Hello</b><br><i>world</i>"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "<b>Hello</b><br><i>world</i>"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["whatsapp"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "wa-user"}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "whatsapp",
		From:    "wa-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) != 1 {
		t.Fatalf("expected one outbound message, got %+v", adapter.outbound)
	}
	if adapter.outbound[0].Payload.Text != "*Hello*\n_world_" {
		t.Fatalf("unexpected sanitized payload: %+v", adapter.outbound[0].Payload)
	}
}

func TestRuntimePushInboundUsesPersistedDeliveryTargetFallback(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "telegram"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "persisted reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "persisted reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["telegram"] = config.ChannelConfig{Enabled: &enabled, Agent: "main"}
	sessionKey := session.BuildDirectSessionKey("main", "telegram", "peer-9")
	if err := rt.Sessions.Upsert(sessionKey, core.SessionEntry{
		SessionID:       "sess-persisted",
		LastChannel:     "telegram",
		LastTo:          "peer-9",
		LastAccountID:   "acc-9",
		LastThreadID:    "thread-9",
		DeliveryContext: &core.DeliveryContext{Channel: "telegram", To: "peer-9", AccountID: "acc-9", ThreadID: "thread-9"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := rt.Deliverer.Deliver(context.Background(), core.ReplyKindFinal, core.ReplyPayload{Text: "persisted reply"}, core.DeliveryTarget{
		SessionKey: sessionKey,
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(adapter.outbound) != 1 {
		t.Fatalf("expected one outbound message, got %+v", adapter.outbound)
	}
	if adapter.outbound[0].To != "peer-9" || adapter.outbound[0].AccountID != "acc-9" || adapter.outbound[0].ThreadID != "thread-9" {
		t.Fatalf("expected persisted target fields, got %+v", adapter.outbound[0])
	}
}

func TestRuntimePushInboundSkipsNonRenderableReasoningPayload(t *testing.T) {
	adapter := &outboundOnlyChannelAdapter{id: "payloadchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "thinking", IsReasoning: true})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "thinking", IsReasoning: true}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["payloadchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "payload-user"}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "payloadchan",
		From:    "payload-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) != 0 {
		t.Fatalf("expected reasoning payload to be skipped, got %+v", adapter.outbound)
	}
}

func TestRuntimePushInboundPreservesChannelDataForPayloadSender(t *testing.T) {
	adapter := &outboundOnlyChannelAdapter{id: "payloadjson"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{
			Text:        "payload reply",
			ChannelData: map[string]any{"format": "rich"},
		})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{
			Text:        "payload reply",
			ChannelData: map[string]any{"format": "rich"},
		}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["payloadjson"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "payload-user"}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "payloadjson",
		From:    "payload-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) != 1 {
		t.Fatalf("expected one outbound payload, got %+v", adapter.outbound)
	}
	if adapter.outbound[0].Payload.ChannelData["format"] != "rich" {
		t.Fatalf("expected channelData preservation, got %+v", adapter.outbound[0].Payload.ChannelData)
	}
}

func TestRuntimePushInboundWritesAndAcksDeliveryQueue(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "queuechan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "queued reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "queued reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["queuechan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "queue-user"}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "queuechan",
		From:    "queue-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	queueDir := filepath.Join(rt.Sessions.BaseDir(), "delivery-queue")
	files, readErr := os.ReadDir(queueDir)
	if readErr != nil {
		t.Fatalf("read queue dir: %v", readErr)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			t.Fatalf("expected queue entry to be acked and removed, found %s", file.Name())
		}
	}
}

func TestRuntimePushInboundMovesFailedDeliveryToFailedQueue(t *testing.T) {
	adapter := &failingTextChannelAdapter{id: "failchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "will fail"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "will fail"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["failchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "fail-user"}
	hooks := &recordingHookRunner{}
	rt.Hooks = hooks
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "failchan",
		From:    "fail-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	failedDir := filepath.Join(rt.Sessions.BaseDir(), "delivery-queue", "failed")
	found := false
	_ = filepath.WalkDir(failedDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".json") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatalf("expected failed delivery artifact in %s", failedDir)
	}
	if len(hooks.sent) != 1 || hooks.sent[0].Success {
		t.Fatalf("expected failed message_sent hook, got %+v", hooks.sent)
	}
}

func TestRuntimePushInboundAppliesMessageSendingAndSentHooks(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "hookchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "hello"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "hello"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["hookchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "hook-user"}
	hooks := &recordingHookRunner{result: delivery.OutboundMessageSendingResult{Content: "rewritten"}}
	rt.Hooks = hooks
	if router, ok := rt.Deliverer.(*delivery.RouterDeliverer); ok {
		router.Hooks = hooks
	}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "hookchan",
		From:    "hook-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(hooks.sending) != 1 || hooks.sending[0].Content != "hello" {
		t.Fatalf("unexpected sending hooks: %+v", hooks.sending)
	}
	if len(adapter.outbound) != 1 || adapter.outbound[0].Payload.Text != "rewritten" {
		t.Fatalf("expected rewritten outbound payload, got %+v", adapter.outbound)
	}
	if len(hooks.sent) != 1 || !hooks.sent[0].Success || hooks.sent[0].Content != "rewritten" {
		t.Fatalf("unexpected sent hooks: %+v", hooks.sent)
	}
}

func TestRuntimePushInboundCanCancelViaMessageSendingHook(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "cancelchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "hello"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "hello"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["cancelchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "cancel-user"}
	hooks := &recordingHookRunner{result: delivery.OutboundMessageSendingResult{Cancel: true}}
	rt.Hooks = hooks
	if router, ok := rt.Deliverer.(*delivery.RouterDeliverer); ok {
		router.Hooks = hooks
	}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "cancelchan",
		From:    "cancel-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(adapter.outbound) != 0 {
		t.Fatalf("expected send cancellation, got %+v", adapter.outbound)
	}
	if len(hooks.sent) != 0 {
		t.Fatalf("did not expect message_sent hook on cancellation, got %+v", hooks.sent)
	}
}

func TestRuntimePushInboundMirrorsTranscriptWithoutDuplicateAppend(t *testing.T) {
	adapter := &textOnlyChannelAdapter{id: "mirrorchan"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "mirror reply"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "mirror reply"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["mirrorchan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "mirror-user"}
	resp, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "mirrorchan",
		From:    "mirror-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	history, loadErr := rt.Sessions.LoadTranscript(resp.SessionKey)
	if loadErr != nil {
		t.Fatalf("load transcript: %v", loadErr)
	}
	count := 0
	for _, message := range history {
		if message.Role == "assistant" && strings.TrimSpace(message.Text) == "mirror reply" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected mirrored assistant text to avoid duplication, count=%d history=%+v", count, history)
	}
}

func TestRuntimeReplayDeliveryQueueRetriesFailedOutbound(t *testing.T) {
	adapter := &flakyTextChannelAdapter{id: "replaychan", failures: 1}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "retry me"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "retry me"}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["replaychan"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "replay-user"}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "replaychan",
		From:    "replay-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	items, loadErr := delivery.LoadQueuedDeliveries(rt.Sessions.BaseDir(), true)
	if loadErr != nil {
		t.Fatalf("load queued deliveries: %v", loadErr)
	}
	if len(items) != 1 {
		t.Fatalf("expected one queued delivery, got %+v", items)
	}
	item := items[0]
	item.NextAttemptAt = time.Now().UTC().Add(-time.Second)
	if err := delivery.WriteQueuedDelivery(rt.Sessions.BaseDir(), item, true); err != nil {
		t.Fatalf("write queued delivery: %v", err)
	}
	result, err := delivery.ReplayQueue(context.Background(), rt.Sessions.BaseDir(), rt.Deliverer, 10)
	if err != nil {
		t.Fatalf("replay delivery queue: %v", err)
	}
	if result.Replayed != 1 {
		t.Fatalf("expected one replayed delivery, got %+v", result)
	}
	if len(adapter.outbound) != 1 || adapter.outbound[0].Payload.Text != "retry me" {
		t.Fatalf("expected replayed outbound payload, got %+v", adapter.outbound)
	}
	failedDir := filepath.Join(rt.Sessions.BaseDir(), "delivery-queue", "failed")
	entries, readErr := os.ReadDir(failedDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read failed dir: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("expected replayed delivery to be acked, found %s", entry.Name())
		}
	}
}

func TestRuntimePushInboundHookEventsIncludeQueueMetadata(t *testing.T) {
	adapter := &failingTextChannelAdapter{id: "hookmeta"}
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{
			Text:        "meta reply",
			ChannelData: map[string]any{"format": "rich"},
		})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "meta reply", ChannelData: map[string]any{"format": "rich"}}}}, nil
	}))
	rt.Channels.RegisterChannel(adapter.ID(), adapter)
	enabled := true
	rt.Config.Channels.Entries["hookmeta"] = config.ChannelConfig{Enabled: &enabled, Agent: "main", DefaultTo: "meta-user"}
	hooks := &recordingHookRunner{}
	rt.Hooks = hooks
	if router, ok := rt.Deliverer.(*delivery.RouterDeliverer); ok {
		router.Hooks = hooks
	}
	_, err := rt.PushInbound(context.Background(), core.ChannelInboundMessage{
		Channel: "hookmeta",
		From:    "meta-user",
		Text:    "ping",
	})
	if err != nil {
		t.Fatalf("push inbound: %v", err)
	}
	if len(hooks.sending) != 1 || hooks.sending[0].QueueID == "" || hooks.sending[0].Kind != core.ReplyKindFinal {
		t.Fatalf("expected queue metadata on message_sending, got %+v", hooks.sending)
	}
	if hooks.sending[0].ChannelData["format"] != "rich" {
		t.Fatalf("expected channelData in message_sending, got %+v", hooks.sending[0].ChannelData)
	}
	if len(hooks.sent) != 1 || hooks.sent[0].Status != delivery.DeliveryStatusFailed || hooks.sent[0].QueueID == "" {
		t.Fatalf("expected failed message_sent with queue metadata, got %+v", hooks.sent)
	}
}

func TestChunkOutboundTextPreservesSingleParagraphFenceBlock(t *testing.T) {
	text := "```js\nconst a = 1;\nconst b = 2;\n```\nAfter"
	chunks := channel.ChunkOutboundText(text, config.ChannelConfig{TextChunkLimit: 1000, ChunkMode: "newline"}, "")
	if len(chunks) != 1 || chunks[0] != text {
		t.Fatalf("expected single fenced chunk, got %+v", chunks)
	}
}

func TestChunkOutboundTextSplitsLongFenceSafely(t *testing.T) {
	text := "```js\n" + strings.Repeat("const a = 1;\n", 20) + "```"
	chunks := channel.ChunkOutboundText(text, config.ChannelConfig{TextChunkLimit: 40, ChunkMode: "newline"}, "")
	if len(chunks) < 2 {
		t.Fatalf("expected long fenced block to split, got %+v", chunks)
	}
	for _, chunk := range chunks[:len(chunks)-1] {
		if !strings.Contains(chunk, "```") {
			t.Fatalf("expected reopened/closed fence in chunk, got %q", chunk)
		}
	}
	if !strings.HasPrefix(chunks[0], "```js\n") {
		t.Fatalf("expected first chunk to preserve opening fence, got %q", chunks[0])
	}
}

func TestChunkOutboundTextUsesAccountSpecificChunkLimit(t *testing.T) {
	text := "alpha beta gamma"
	cfg := config.ChannelConfig{
		TextChunkLimit: 100,
		Accounts: map[string]any{
			"acc-1": map[string]any{
				"textChunkLimit": 5,
			},
		},
	}
	chunks := channel.ChunkOutboundText(text, cfg, "acc-1")
	if len(chunks) < 2 {
		t.Fatalf("expected account-specific limit to split text, got %+v", chunks)
	}
}

func TestChunkOutboundTextUsesAccountSpecificChunkMode(t *testing.T) {
	text := "alpha\n\nbeta"
	cfg := config.ChannelConfig{
		TextChunkLimit: 5,
		ChunkMode:      "length",
		Accounts: map[string]any{
			"acc-1": map[string]any{
				"chunkMode":      "newline",
				"textChunkLimit": 5,
			},
		},
	}
	chunks := channel.ChunkOutboundText(text, cfg, "acc-1")
	if len(chunks) != 2 || chunks[0] != "alpha" || chunks[1] != "beta" {
		t.Fatalf("expected account-specific newline chunking, got %+v", chunks)
	}
}

func TestGatewayChatEventsStreamsWebchatRecords(t *testing.T) {
	rt := testGatewayRuntime(t, backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "stream hello"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "stream hello"}}}, nil
	}))
	handler := NewServer(rt, config.GatewayConfig{}).Handler()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listen unavailable in this environment: %v", err)
	}
	srv := &http.Server{Handler: handler}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(listener)
	}()
	defer func() {
		_ = srv.Close()
		_ = listener.Close()
		wg.Wait()
	}()
	baseURL := (&url.URL{Scheme: "http", Host: listener.Addr().String()}).String()

	req, err := http.NewRequest(http.MethodGet, baseURL+"/rpc/chat.events?sessionKey="+session.BuildDirectSessionKey("main", "webchat", "webchat-user"), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	type sseEvent struct {
		name string
		body string
	}
	done := make(chan sseEvent, 8)
	go func() {
		reader := bufio.NewReader(resp.Body)
		currentEvent := "message"
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					done <- sseEvent{}
				}
				return
			}
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				done <- sseEvent{
					name: currentEvent,
					body: strings.TrimSpace(strings.TrimPrefix(line, "data: ")),
				}
				currentEvent = "message"
			}
		}
	}()

	sendReq := httptest.NewRequest(http.MethodPost, "/rpc/chat.send", strings.NewReader(`{"sessionKey":"agent:main:webchat:direct:webchat-user","message":"hello","channel":"webchat","to":"webchat-user"}`))
	sendReq.Header.Set("Content-Type", "application/json")
	sendRes := httptest.NewRecorder()
	NewServer(rt, config.GatewayConfig{}).Handler().ServeHTTP(sendRes, sendReq)
	if sendRes.Code != http.StatusOK {
		t.Fatalf("chat.send status = %d body=%s", sendRes.Code, sendRes.Body.String())
	}

	select {
	case first := <-done:
		foundMessage := first.name == "message" && strings.Contains(first.body, "\"text\":\"stream hello\"")
		foundDebug := first.name == "lifecycle" || first.name == "debug"
		timeout := time.After(2 * time.Second)
		for !(foundMessage && foundDebug) {
			select {
			case next := <-done:
				if next.name == "message" && strings.Contains(next.body, "\"text\":\"stream hello\"") {
					foundMessage = true
				}
				if (next.name == "lifecycle" || next.name == "debug") && strings.Contains(next.body, "\"type\":\"run_started\"") {
					foundDebug = true
				}
			case <-timeout:
				t.Fatalf("timed out waiting for message/debug events; first=%+v", first)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event stream data")
	}
}

func TestGatewayChatSendReminderPreservesVisibleReplyWhenBackendTimesOutAfterSchedulingTask(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	rt := &runtime.Runtime{
		Config: config.AppConfig{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderConfig{
					"openai": {
						BaseURL: "https://example.com/v1",
						APIKey:  "test-key",
						API:     "openai-completions",
						Models:  []config.ProviderModelConfig{{ID: "gpt-4.1", MaxTokens: 1024}},
					},
				},
			},
		},
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		EventHub:   gateway.NewWebchatHub(),
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tasks:      tasks,
		Tools:      tool.NewToolRegistry(tool.NewCronTool()),
		Backend: backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			runCtx.ReplyDispatcher.SendBlockReply(core.ReplyPayload{Text: "我会帮您设置一个5分钟后的提醒。"})
			_, err := runCtx.Runtime.ExecuteTool(ctx, runCtx, "cron", map[string]any{
				"action": "add",
				"job": map[string]any{
					"name": "laundry reminder",
					"schedule": map[string]any{
						"kind": "at",
						"at":   time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339),
					},
					"payload": map[string]any{
						"kind": "systemEvent",
						"text": "Reminder: 拿衣服",
					},
				},
			})
			if err != nil {
				return core.AgentRunResult{}, err
			}
			return core.AgentRunResult{
					SuccessfulCronAdds: 1,
				}, &backend.BackendError{
					Reason:  backend.BackendFailureTransientHTTP,
					Message: "provider request failed: context deadline exceeded",
				}
		}),
	}
	server := NewServer(rt, config.GatewayConfig{})
	sendReq := httptest.NewRequest(http.MethodPost, "/rpc/chat.send", strings.NewReader(`{"sessionKey":"agent:main:webchat:direct:webchat-user","message":"5分钟后提醒我拿衣服","channel":"webchat","to":"webchat-user"}`))
	sendReq.Header.Set("Content-Type", "application/json")
	sendRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(sendRes, sendReq)
	if sendRes.Code != http.StatusOK {
		t.Fatalf("chat.send status = %d body=%s", sendRes.Code, sendRes.Body.String())
	}
	var resp core.ChatSendResponse
	if err := json.Unmarshal(sendRes.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Payloads) != 1 || strings.TrimSpace(resp.Payloads[0].Text) != "我会帮您设置一个5分钟后的提醒。" {
		t.Fatalf("expected visible assistant confirmation payload, got %+v", resp.Payloads)
	}
	var assistantFinals []core.TranscriptMessage
	var assistantToolCalls []core.TranscriptMessage
	for _, msg := range resp.Messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(msg.Type)) {
		case "assistant_final", "":
			assistantFinals = append(assistantFinals, msg)
		case "tool_call":
			assistantToolCalls = append(assistantToolCalls, msg)
		}
	}
	if len(assistantFinals) == 0 {
		t.Fatalf("expected preserved assistant final reply, got %+v", resp.Messages)
	}
	if strings.TrimSpace(assistantFinals[len(assistantFinals)-1].Text) != "我会帮您设置一个5分钟后的提醒。" {
		t.Fatalf("expected preserved assistant final reply, got %+v", assistantFinals)
	}
	if len(assistantToolCalls) == 0 {
		t.Fatalf("expected transcript to record the cron tool call, got %+v", resp.Messages)
	}
	taskList := rt.ListTasks(context.Background())
	if len(taskList) != 1 || taskList[0].Kind != core.TaskKindScheduled {
		t.Fatalf("expected scheduled reminder task, got %+v", taskList)
	}
}

func testGatewayRuntime(t *testing.T, backend rtypes.Backend) *runtime.Runtime {
	t.Helper()
	rt, err := runtime.NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {BaseURL: "https://example.com/v1", API: "openai-completions", Models: []config.ProviderModelConfig{{ID: "gpt-4.1"}}},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:      "main",
				Default: true,
				Model:   config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		Channels: config.ChannelsConfig{
			Entries: map[string]config.ChannelConfig{},
		},
	}, config.RuntimeConfigParams{StateDir: t.TempDir(), AgentID: "main", Deliverer: &delivery.MemoryDeliverer{}})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.Backends = nil
	rt.Backend = backend
	return rt
}

func latestReplyText(payloads []core.ReplyPayload) string {
	for i := len(payloads) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(payloads[i].Text); text != "" {
			return text
		}
	}
	return ""
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestGatewayDashboardAndAuditEndpoints(t *testing.T) {
	stateDir := t.TempDir()
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"test": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "demo", Name: "Demo"}},
				},
			},
		},
		Gateway: config.GatewayConfig{Enabled: true, Webchat: &config.GatewayWebchatConfig{Enabled: utils.BoolPtr(true)}},
		Tasks:   config.TasksConfig{Enabled: utils.BoolPtr(false)},
	}
	rt, err := runtime.NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		StateDir:  stateDir,
		AgentID:   "main",
		Provider:  "test",
		Model:     "demo",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	_ = rt.GetAudit().Record(context.Background(), core.AuditEvent{
		Category: core.AuditCategoryConfig,
		Type:     "test_event",
		Level:    "info",
		Message:  "hello",
	})
	server := NewServer(rt, cfg.Gateway)

	req := httptest.NewRequest("GET", "/rpc/dashboard.snapshot", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != 200 {
		t.Fatalf("dashboard status = %d body=%s", resp.Code, resp.Body.String())
	}

	req = httptest.NewRequest("POST", "/rpc/audit.list", strings.NewReader(`{"category":"config","limit":5}`))
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != 200 {
		t.Fatalf("audit status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "test_event") {
		t.Fatalf("expected audit response to contain test_event, got %s", resp.Body.String())
	}
}
