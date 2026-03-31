package weixin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/channel/adapter"
)

// roundTripFunc implements http.RoundTripper for testing.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWeixinAdapterID(t *testing.T) {
	a := NewWeixinAdapter("weixin")
	if a.ID() != "weixin" {
		t.Fatalf("expected 'weixin', got %q", a.ID())
	}
}

func TestWeixinAdapterCapabilities(t *testing.T) {
	a := NewWeixinAdapter("weixin")
	if a.TextChunkLimit() != 4000 {
		t.Errorf("expected 4000, got %d", a.TextChunkLimit())
	}
}

func TestParseAdapterConfig(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		wantErr bool
	}{
		{
			name:    "nil config",
			raw:     nil,
			wantErr: true,
		},
		{
			name:    "missing token",
			raw:     map[string]any{"baseUrl": "https://example.com"},
			wantErr: true,
		},
		{
			name: "valid minimal",
			raw:  map[string]any{"token": "test-token"},
		},
		{
			name: "valid full",
			raw: map[string]any{
				"token":              "test-token",
				"baseUrl":            "https://custom.api.com",
				"pollTimeoutSeconds": 60,
				"enableTyping":       true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseAdapterConfig(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Token != "test-token" {
				t.Errorf("expected token 'test-token', got %q", cfg.Token)
			}
		})
	}
}

func TestParseAdapterConfigDefaults(t *testing.T) {
	cfg, err := parseAdapterConfig(map[string]any{"token": "t"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BaseURL != defaultBaseURL {
		t.Errorf("expected default baseURL %q, got %q", defaultBaseURL, cfg.BaseURL)
	}
	if cfg.CDNBaseURL != defaultCDNBaseURL {
		t.Errorf("expected default CDN URL %q, got %q", defaultCDNBaseURL, cfg.CDNBaseURL)
	}
}

func TestContextTokenCache(t *testing.T) {
	cache := newContextTokenCache(1 * time.Hour)
	cache.Put("key1", "token1")

	token, ok := cache.Get("key1")
	if !ok || token != "token1" {
		t.Fatalf("expected token1, got %q (ok=%v)", token, ok)
	}

	_, ok = cache.Get("nonexistent")
	if ok {
		t.Error("expected miss for nonexistent key")
	}
}

func TestBuildInboundMessage(t *testing.T) {
	msg := WeixinMessage{
		MessageID:    12345,
		FromUserID:   "user-abc",
		ToUserID:     "bot-xyz",
		MessageType:  MessageTypeUser,
		ContextToken: "ctx-tok-1",
		ItemList: []MessageItem{
			{Type: ItemTypeText, TextItem: &TextItem{Text: "Hello WeChat!"}},
		},
	}
	ch := adapter.ChannelConfig{Agent: "test-agent"}

	inbound, ok := buildInboundMessage("weixin", msg, ch, "")
	if !ok {
		t.Fatal("expected inbound message to be built")
	}
	if inbound.Channel != "weixin" {
		t.Errorf("expected channel 'weixin', got %q", inbound.Channel)
	}
	if inbound.From != "user-abc" {
		t.Errorf("expected from 'user-abc', got %q", inbound.From)
	}
	if inbound.Text != "Hello WeChat!" {
		t.Errorf("expected text 'Hello WeChat!', got %q", inbound.Text)
	}
	if inbound.ChatType != adapter.ChatTypeDirect {
		t.Errorf("expected direct chat type, got %q", inbound.ChatType)
	}
	if inbound.MessageID != "12345" {
		t.Errorf("expected messageID '12345', got %q", inbound.MessageID)
	}
}

func TestBuildInboundMessageUsesRuntimeChannelID(t *testing.T) {
	msg := WeixinMessage{
		MessageID:   12345,
		FromUserID:  "user-abc",
		ToUserID:    "bot-xyz",
		MessageType: MessageTypeUser,
		ItemList: []MessageItem{
			{Type: ItemTypeText, TextItem: &TextItem{Text: "Hello custom channel!"}},
		},
	}

	inbound, ok := buildInboundMessage("weixin50", msg, adapter.ChannelConfig{Agent: "test-agent"}, "")
	if !ok {
		t.Fatal("expected inbound message to be built")
	}
	if inbound.Channel != "weixin50" {
		t.Fatalf("expected channel 'weixin50', got %q", inbound.Channel)
	}
}

func TestBuildInboundMessageWithSeq(t *testing.T) {
	msg := WeixinMessage{
		MessageID:  12345,
		Seq:        3,
		FromUserID: "user-abc",
		ItemList: []MessageItem{
			{Type: ItemTypeText, TextItem: &TextItem{Text: "Hi"}},
		},
	}
	inbound, ok := buildInboundMessage("weixin", msg, adapter.ChannelConfig{}, "")
	if !ok {
		t.Fatal("expected inbound message")
	}
	if inbound.MessageID != "12345:3" {
		t.Errorf("expected '12345:3', got %q", inbound.MessageID)
	}
}

func TestBuildInboundMessageEmptySkipped(t *testing.T) {
	msg := WeixinMessage{
		MessageID:  1,
		FromUserID: "user-abc",
		ItemList:   nil,
	}
	_, ok := buildInboundMessage("weixin", msg, adapter.ChannelConfig{}, "")
	if ok {
		t.Error("expected empty message to be skipped")
	}
}

func TestBuildInboundMessageRefText(t *testing.T) {
	msg := WeixinMessage{
		MessageID:  1,
		FromUserID: "user-abc",
		ItemList: []MessageItem{
			{
				Type:     ItemTypeText,
				TextItem: &TextItem{Text: "\u56de\u590d\u5185\u5bb9"},
				RefMsg: &RefMessage{
					Title:       "\u539f\u59cb\u6d88\u606f",
					MessageItem: &MessageItem{Type: ItemTypeText, TextItem: &TextItem{Text: "\u539f\u6587"}},
				},
			},
		},
	}
	inbound, ok := buildInboundMessage("weixin", msg, adapter.ChannelConfig{}, "")
	if !ok {
		t.Fatal("expected inbound message")
	}
	if inbound.Text != "[引用: 原始消息 | 原文]\n回复内容" {
		t.Errorf("unexpected text: %q", inbound.Text)
	}
}

func TestExtractContentImage(t *testing.T) {
	msg := WeixinMessage{
		ItemList: []MessageItem{
			{
				Type: ItemTypeImage,
				ImageItem: &ImageItem{
					Media: &CDNMedia{EncryptQueryParam: "enc-param-1"},
				},
			},
		},
	}
	text, attachments := extractContent(msg, "")
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Type != "image" {
		t.Errorf("expected image type, got %q", attachments[0].Type)
	}
}

func TestExtractContentImageWithCDN(t *testing.T) {
	// Set up a fake CDN that returns encrypted image data.
	plaintext := []byte("fake-image-png-data")
	aesKey := []byte("0123456789abcdef") // 16 bytes
	encrypted, err := encryptAESECB(plaintext, aesKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(encrypted)
	}))
	defer cdnServer.Close()

	aesKeyB64 := encodeCDNMediaAESKey(aesKey) // base64(hex(key))

	msg := WeixinMessage{
		ItemList: []MessageItem{
			{
				Type: ItemTypeImage,
				ImageItem: &ImageItem{
					Media: &CDNMedia{
						EncryptQueryParam: "test-enc-param",
						AESKey:            aesKeyB64,
						EncryptType:       1,
					},
				},
			},
		},
	}
	text, attachments := extractContent(msg, cdnServer.URL)
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Type != "image" {
		t.Errorf("expected image type, got %q", attachments[0].Type)
	}
	if len(attachments[0].Content) == 0 {
		t.Fatal("expected image Content to be populated after CDN download")
	}
	if !bytes.Equal(attachments[0].Content, plaintext) {
		t.Errorf("image content mismatch: got %d bytes, want %d bytes", len(attachments[0].Content), len(plaintext))
	}
}

func TestExtractContentVoiceWithText(t *testing.T) {
	msg := WeixinMessage{
		ItemList: []MessageItem{
			{
				Type: ItemTypeVoice,
				VoiceItem: &VoiceItem{
					Text: "\u8bed\u97f3\u8f6c\u6587\u5b57\u5185\u5bb9",
				},
			},
		},
	}
	text, attachments := extractContent(msg, "")
	if text != "\u8bed\u97f3\u8f6c\u6587\u5b57\u5185\u5bb9" {
		t.Errorf("expected speech-to-text, got %q", text)
	}
	if len(attachments) != 0 {
		t.Errorf("expected no attachments for voice STT, got %d", len(attachments))
	}
}

func TestChunkText(t *testing.T) {
	short := "hello"
	chunks := chunkText(short, 100)
	if len(chunks) != 1 || chunks[0] != short {
		t.Errorf("short text should be a single chunk")
	}

	long := ""
	for i := 0; i < 100; i++ {
		long += "abcdefghij"
	}
	chunks = chunkText(long, 200)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for 1000 char text with limit 200")
	}
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	if total != len(long) {
		t.Errorf("chunking lost characters: expected %d, got %d", len(long), total)
	}
}

func TestSendTextCallsAPI(t *testing.T) {
	var capturedReq SendMessageRequest

	a := NewWeixinAdapter("weixin")
	a.client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(r.Body)
			json.Unmarshal(data, &capturedReq)
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ret":0}`))),
				Header:     make(http.Header),
			}, nil
		}),
	}

	// Pre-seed context token.
	a.contextCache.Put("weixin:user-123", "ctx-token-abc")

	result, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To:      "user-123",
		Payload: adapter.ReplyPayload{Text: "Hello from bot!"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "test-bot-token"},
	})
	if err != nil {
		t.Fatalf("SendText failed: %v", err)
	}
	if result.Channel != "weixin" {
		t.Errorf("expected channel 'weixin', got %q", result.Channel)
	}
	if result.ChatID != "user-123" {
		t.Errorf("expected chatID 'user-123', got %q", result.ChatID)
	}
	if capturedReq.Msg.ToUserID != "user-123" {
		t.Errorf("expected to_user_id 'user-123', got %q", capturedReq.Msg.ToUserID)
	}
	if len(capturedReq.Msg.ItemList) != 1 || capturedReq.Msg.ItemList[0].TextItem == nil {
		t.Fatal("expected a text item in the request")
	}
	if capturedReq.Msg.ItemList[0].TextItem.Text != "Hello from bot!" {
		t.Errorf("expected text 'Hello from bot!', got %q", capturedReq.Msg.ItemList[0].TextItem.Text)
	}
}

func TestSendTextUsesRuntimeChannelIDForContextCache(t *testing.T) {
	var capturedReq SendMessageRequest

	a := NewWeixinAdapter("weixin")
	a.client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(r.Body)
			json.Unmarshal(data, &capturedReq)
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ret":0}`))),
				Header:     make(http.Header),
			}, nil
		}),
	}

	if err := a.StartBackground(context.Background(), "weixin-prod", adapter.ChannelConfig{
		Config: map[string]any{"token": "test-bot-token"},
	}, nil, adapter.Callbacks{
		OnMessage: func(context.Context, adapter.InboundMessage) error { return nil },
	}); err != nil {
		t.Fatalf("StartBackground failed: %v", err)
	}
	a.StopBackground()

	a.contextCache.Put("weixin-prod:user-123", "ctx-token-abc")

	result, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To:      "user-123",
		Payload: adapter.ReplyPayload{Text: "Hello from runtime-bound bot!"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "test-bot-token"},
	})
	if err != nil {
		t.Fatalf("SendText failed: %v", err)
	}
	if result.Channel != "weixin" {
		t.Errorf("expected channel 'weixin', got %q", result.Channel)
	}
	if capturedReq.Msg.ContextToken != "ctx-token-abc" {
		t.Fatalf("expected cached context_token to be used, got %q", capturedReq.Msg.ContextToken)
	}
	if capturedReq.Msg.ToUserID != "user-123" {
		t.Fatalf("expected to_user_id user-123, got %q", capturedReq.Msg.ToUserID)
	}
}

func TestSendTextRequiresContextToken(t *testing.T) {
	a := NewWeixinAdapter("weixin")
	// Don't seed context token.

	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To:      "user-123",
		Payload: adapter.ReplyPayload{Text: "Hello"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "test-bot-token"},
	})
	if err == nil {
		t.Fatal("expected error when no context_token is cached")
	}
	if !contains(err.Error(), "context_token") {
		t.Errorf("error should mention context_token, got: %v", err)
	}
}

func TestSendTextReturnsAPIErrorWhenBusinessRetFails(t *testing.T) {
	a := NewWeixinAdapter("weixin")
	a.client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ret":-1,"errcode":40001,"errmsg":"context token invalid"}`))),
				Header:     make(http.Header),
			}, nil
		}),
	}

	a.contextCache.Put("weixin:user-123", "ctx-token-abc")

	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To:      "user-123",
		Payload: adapter.ReplyPayload{Text: "Hello from bot!"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "test-bot-token"},
	})
	if err == nil {
		t.Fatal("expected business-layer sendmessage error")
	}
	if !contains(err.Error(), "sendmessage api error") {
		t.Fatalf("expected sendmessage api error, got %v", err)
	}
}

func TestSendTextRequiresToken(t *testing.T) {
	a := NewWeixinAdapter("weixin")
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To:      "user-123",
		Payload: adapter.ReplyPayload{Text: "Hello"},
	}, adapter.ChannelConfig{
		Config: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error when token is missing")
	}
}

func TestSendMediaFallsBackToText(t *testing.T) {
	var capturedReq SendMessageRequest

	a := NewWeixinAdapter("weixin")
	a.client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(r.Body)
			json.Unmarshal(data, &capturedReq)
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ret":0}`))),
				Header:     make(http.Header),
			}, nil
		}),
	}

	a.contextCache.Put("weixin:user-456", "ctx-token-def")

	result, err := a.SendMedia(context.Background(), adapter.OutboundMessage{
		To: "user-456",
		Payload: adapter.ReplyPayload{
			Text:     "Check this out",
			MediaURL: "https://example.com/photo.jpg",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "test-bot-token"},
	})
	if err != nil {
		t.Fatalf("SendMedia failed: %v", err)
	}
	if result.ChatID != "user-456" {
		t.Errorf("expected chatID 'user-456', got %q", result.ChatID)
	}
	// Should have sent as text with URL appended.
	if capturedReq.Msg.ItemList[0].TextItem == nil {
		t.Fatal("expected text item")
	}
	text := capturedReq.Msg.ItemList[0].TextItem.Text
	if !contains(text, "Check this out") || !contains(text, "https://example.com/photo.jpg") {
		t.Errorf("expected text+URL, got %q", text)
	}
}

func TestCryptoAESECBRoundTrip(t *testing.T) {
	key := []byte("1234567890123456") // 16 bytes
	plaintext := []byte("Hello, WeChat AES-ECB test data!")

	ciphertext, err := encryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if len(ciphertext)%16 != 0 {
		t.Fatalf("ciphertext not block-aligned: %d", len(ciphertext))
	}

	decrypted, err := decryptAESECB(ciphertext, key)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("round-trip mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestCryptoPKCS7Padding(t *testing.T) {
	data := []byte("test")
	padded := pkcs7Pad(data, 16)
	if len(padded) != 16 {
		t.Fatalf("expected padded len 16, got %d", len(padded))
	}
	unpadded, err := pkcs7Unpad(padded, 16)
	if err != nil {
		t.Fatalf("unpad failed: %v", err)
	}
	if string(unpadded) != "test" {
		t.Errorf("expected 'test', got %q", unpadded)
	}
}

func TestAESECBPaddedSize(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{0, 16},
		{1, 16},
		{15, 16},
		{16, 32},
		{17, 32},
		{31, 32},
		{32, 48},
	}
	for _, tt := range tests {
		got := aesECBPaddedSize(tt.input)
		if got != tt.expected {
			t.Errorf("aesECBPaddedSize(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestUploadToCDNUsesUploadParamAndEncryptedBody(t *testing.T) {
	plaintext := []byte("hello weixin image")
	aesKey := []byte("1234567890123456")
	filekey := "file-key-123"
	uploadParam := "opaque upload param+/="

	var gotQuery url.Values
	var gotContentType string
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		gotQuery = r.URL.Query()
		gotContentType = r.Header.Get("Content-Type")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		w.Header().Set("x-encrypted-param", "download-token")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	downloadParam, err := uploadToCDN(server.URL, uploadParam, filekey, plaintext, aesKey)
	if err != nil {
		t.Fatalf("uploadToCDN failed: %v", err)
	}
	if downloadParam != "download-token" {
		t.Fatalf("expected download-token, got %q", downloadParam)
	}
	if gotQuery.Get("encrypted_query_param") != uploadParam {
		t.Fatalf("expected encrypted_query_param %q, got %q", uploadParam, gotQuery.Get("encrypted_query_param"))
	}
	if gotQuery.Get("upload_param") != "" {
		t.Fatalf("did not expect upload_param, got %q", gotQuery.Get("upload_param"))
	}
	if gotQuery.Get("filekey") != filekey {
		t.Fatalf("expected filekey %q, got %q", filekey, gotQuery.Get("filekey"))
	}
	if gotContentType != "application/octet-stream" {
		t.Fatalf("expected application/octet-stream, got %q", gotContentType)
	}
	wantBody, err := encryptAESECB(plaintext, aesKey)
	if err != nil {
		t.Fatalf("encryptAESECB failed: %v", err)
	}
	if !bytes.Equal(gotBody, wantBody) {
		t.Fatalf("unexpected uploaded body")
	}
}

func TestEncodeCDNMediaAESKeyMatchesSupportedFormat(t *testing.T) {
	rawKey := []byte("1234567890123456")
	got := encodeCDNMediaAESKey(rawKey)
	want := base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(rawKey)))
	if got != want {
		t.Fatalf("expected encoded aes key %q, got %q", want, got)
	}
	parsed, err := parseAESKey(got)
	if err != nil {
		t.Fatalf("parseAESKey failed: %v", err)
	}
	if !bytes.Equal(parsed, rawKey) {
		t.Fatalf("expected parsed key %x, got %x", rawKey, parsed)
	}
}

func TestReadHelpers(t *testing.T) {
	raw := map[string]any{
		"token":    "abc",
		"count":    42,
		"enabled":  true,
		"countStr": "99",
	}

	if readString(raw, "token") != "abc" {
		t.Error("readString failed for 'token'")
	}
	if readString(raw, "missing") != "" {
		t.Error("readString should return empty for missing key")
	}

	v, ok := readInt(raw, "count")
	if !ok || v != 42 {
		t.Errorf("readInt failed: got %d, ok=%v", v, ok)
	}

	v, ok = readInt(raw, "countStr")
	if !ok || v != 99 {
		t.Errorf("readInt failed for string: got %d, ok=%v", v, ok)
	}

	b, ok := readBool(raw, "enabled")
	if !ok || !b {
		t.Errorf("readBool failed: got %v, ok=%v", b, ok)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
