package feishu

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestFeishuChannelParsesSDKFileInboundMessage(t *testing.T) {
	fa := NewFeishuAdapter()
	if err := fa.StartBackground(context.Background(), "feishu-custom", adapter.ChannelConfig{}, nil, adapter.Callbacks{}); err != nil {
		t.Fatalf("StartBackground failed: %v", err)
	}
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages/om_message_2/resources/file-key" && r.URL.Query().Get("type") == "file":
				return fileHTTPResponse(http.StatusOK, "text/plain", "upload.txt", "hello from inbound file"), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.String())
				return nil, nil
			}
		}),
	})
	msg, err := fa.feishuInboundMessageFromSDKEvent(context.Background(), feishuResolvedAccount{
		ID:        "main",
		AppID:     "cli_main",
		AppSecret: "secret_main",
		Domain:    "https://feishu.test",
	}, "main", &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: stringPtr("ou_user_456"),
				},
			},
			Message: &larkim.EventMessage{
				MessageId:   stringPtr("om_message_2"),
				RootId:      stringPtr("om_root_1"),
				ChatId:      stringPtr("oc_group_1"),
				ChatType:    stringPtr("group"),
				MessageType: stringPtr("file"),
				Content:     stringPtr(`{"file_key":"file-key"}`),
			},
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{
			"mediaMaxMb": 5,
		},
	})
	if err != nil {
		t.Fatalf("parse group inbound: %v", err)
	}
	if msg.ChatType != adapter.ChatTypeGroup {
		t.Fatalf("expected group chat type, got %+v", msg)
	}
	if msg.To != "oc_group_1" {
		t.Fatalf("expected group target, got %+v", msg)
	}
	if msg.ThreadID != "om_root_1" || msg.Text != "[file]" {
		t.Fatalf("unexpected group message parse: %+v", msg)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Name != "upload.txt" || string(msg.Attachments[0].Content) != "hello from inbound file" {
		t.Fatalf("unexpected group attachments: %+v", msg.Attachments)
	}
	if msg.Channel != "feishu-custom" {
		t.Fatalf("expected custom channel id, got %+v", msg)
	}
}

func TestFeishuChannelParsesSDKInboundMessage(t *testing.T) {
	fa := NewFeishuAdapter()
	if err := fa.StartBackground(context.Background(), "feishu-custom", adapter.ChannelConfig{}, nil, adapter.Callbacks{}); err != nil {
		t.Fatalf("StartBackground failed: %v", err)
	}
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages/om_sdk_1/resources/img_post_1" && r.URL.Query().Get("type") == "image":
				return fileHTTPResponse(http.StatusOK, "image/png", "post.png", "PNGDATA"), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.String())
				return nil, nil
			}
		}),
	})
	msg, err := fa.feishuInboundMessageFromSDKEvent(context.Background(), feishuResolvedAccount{
		ID:        "main",
		AppID:     "cli_main",
		AppSecret: "secret_main",
		Domain:    "https://feishu.test",
	}, "main", &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: stringPtr("ou_sdk_123"),
				},
			},
			Message: &larkim.EventMessage{
				MessageId:   stringPtr("om_sdk_1"),
				ChatId:      stringPtr("oc_group_sdk"),
				ThreadId:    stringPtr("omt_thread_1"),
				ChatType:    stringPtr("topic_group"),
				MessageType: stringPtr("post"),
				Content:     stringPtr(`{"zh_cn":{"title":"post title","content":[[{"tag":"text","text":"hello from sdk"},{"tag":"img","image_key":"img_post_1"}]]}}`),
			},
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{
			"mediaMaxMb": 5,
		},
	})
	if err != nil {
		t.Fatalf("parse sdk inbound: %v", err)
	}
	if msg.AccountID != "main" || msg.MessageID != "om_sdk_1" {
		t.Fatalf("unexpected sdk metadata: %+v", msg)
	}
	if msg.ChatType != adapter.ChatTypeTopic || msg.To != "oc_group_sdk" {
		t.Fatalf("unexpected sdk routing: %+v", msg)
	}
	if msg.ThreadID != "omt_thread_1" || !strings.Contains(msg.Text, "hello from sdk") {
		t.Fatalf("unexpected sdk parse: %+v", msg)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Name != "post.png" || msg.Attachments[0].MIMEType != "image/png" {
		t.Fatalf("unexpected sdk attachments: %+v", msg.Attachments)
	}
	if msg.Channel != "feishu-custom" {
		t.Fatalf("expected custom channel id, got %+v", msg)
	}
}

func TestFeishuChannelSendText(t *testing.T) {
	var (
		tokenCalls int
		sendCalls  int
	)
	fa := NewFeishuAdapter()
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				tokenCalls++
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages/om_parent_1/reply":
				sendCalls++
				if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
					t.Fatalf("unexpected auth header: %s", got)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode send payload: %v", err)
				}
				if payload["msg_type"] != "text" {
					t.Fatalf("unexpected send payload: %+v", payload)
				}
				if _, exists := payload["receive_id"]; exists {
					t.Fatalf("reply payload should not include receive_id: %+v", payload)
				}
				if !strings.Contains(payload["content"].(string), "channel reply") {
					t.Fatalf("unexpected content: %v", payload["content"])
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"message_id":"om_sent_1"}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	})
	result, err := fa.SendText(context.Background(), adapter.OutboundMessage{
		AccountID: "main",
		To:        "chat:oc_group_1",
		ReplyToID: "om_parent_1",
		Payload: adapter.ReplyPayload{
			Text: "channel reply",
		},
	}, adapter.ChannelConfig{
		DefaultAccount: "main",
		Accounts: map[string]any{
			"main": map[string]any{
				"appId":     "cli_main",
				"appSecret": "secret_main",
				"domain":    "https://feishu.test",
			},
		},
	})
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if result.MessageID != "om_sent_1" {
		t.Fatalf("unexpected delivery result: %+v", result)
	}
	if sendCalls != 1 {
		t.Fatalf("unexpected request counts: token=%d send=%d", tokenCalls, sendCalls)
	}
}

func TestFeishuChannelSendMedia(t *testing.T) {
	fa := NewFeishuAdapter()
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/files":
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"file_key":"file-key-1"}}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages":
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode send payload: %v", err)
				}
				if payload["msg_type"] != "file" {
					t.Fatalf("expected file msg_type, got %+v", payload)
				}
				content, _ := payload["content"].(string)
				if !strings.Contains(content, "file-key-1") {
					t.Fatalf("expected file key in content, got %q", content)
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"message_id":"om_media_1","chat_id":"oc_group_1"}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	})

	result, err := fa.SendMedia(context.Background(), adapter.OutboundMessage{
		AccountID: "main",
		To:        "chat:oc_group_1",
		Payload: adapter.ReplyPayload{
			MediaURL: "data:text/plain;base64,aGVsbG8=",
		},
	}, adapter.ChannelConfig{
		DefaultAccount: "main",
		Accounts: map[string]any{
			"main": map[string]any{
				"appId":     "cli_main",
				"appSecret": "secret_main",
				"domain":    "https://feishu.test",
			},
		},
	})
	if err != nil {
		t.Fatalf("send media: %v", err)
	}
	if result.MessageID != "om_media_1" || result.ChatID != "oc_group_1" {
		t.Fatalf("unexpected delivery result: %+v", result)
	}
}

func TestFeishuChannelSendTextEscapesJSONContent(t *testing.T) {
	var sendCalls int
	fa := NewFeishuAdapter()
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages":
				sendCalls++
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode send payload: %v", err)
				}
				content, _ := payload["content"].(string)
				var parsed map[string]string
				if err := json.Unmarshal([]byte(content), &parsed); err != nil {
					t.Fatalf("expected valid json content, got %q err=%v", content, err)
				}
				if parsed["text"] != "hello \"feishu\"\nline2" {
					t.Fatalf("unexpected parsed text: %+v", parsed)
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"message_id":"om_sent_json","chat_id":"oc_chat_1"}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	})
	result, err := fa.SendText(context.Background(), adapter.OutboundMessage{
		AccountID: "main",
		To:        "user:ou_user_json",
		Payload: adapter.ReplyPayload{
			Text: "hello \"feishu\"\nline2",
		},
	}, adapter.ChannelConfig{
		DefaultAccount: "main",
		Accounts: map[string]any{
			"main": map[string]any{
				"appId":     "cli_main",
				"appSecret": "secret_main",
				"domain":    "https://feishu.test",
			},
		},
	})
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if result.MessageID != "om_sent_json" || sendCalls != 1 {
		t.Fatalf("unexpected result: %+v sendCalls=%d", result, sendCalls)
	}
}

func TestFeishuChannelSendTextUsesPostForMarkdown(t *testing.T) {
	var sendCalls int
	fa := NewFeishuAdapter()
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages":
				sendCalls++
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode send payload: %v", err)
				}
				if payload["msg_type"] != "post" {
					t.Fatalf("expected post msg type, got %+v", payload)
				}
				content, _ := payload["content"].(string)
				// With the md tag, Feishu renders markdown server-side.
				// The content should contain a md-tagged element with the
				// original markdown text (including the link URL).
				if !strings.Contains(content, `"tag":"md"`) || !strings.Contains(content, `https://example.com`) {
					t.Fatalf("expected md-tagged post content with link, got %s", content)
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"message_id":"om_post_1","chat_id":"oc_chat_1"}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	})
	result, err := fa.SendText(context.Background(), adapter.OutboundMessage{
		AccountID: "main",
		To:        "chat:oc_group_1",
		Payload: adapter.ReplyPayload{
			Text: "# Title\n- item one\n[kocort](https://example.com)",
		},
	}, adapter.ChannelConfig{
		DefaultAccount: "main",
		Accounts: map[string]any{
			"main": map[string]any{
				"appId":     "cli_main",
				"appSecret": "secret_main",
				"domain":    "https://feishu.test",
			},
		},
	})
	if err != nil {
		t.Fatalf("send markdown text: %v", err)
	}
	if result.MessageID != "om_post_1" || sendCalls != 1 {
		t.Fatalf("unexpected result: %+v sendCalls=%d", result, sendCalls)
	}
}

func TestFeishuChannelSendsAckReaction(t *testing.T) {
	var reactionCalls int
	fa := NewFeishuAdapter()
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages/om_message_1/reactions":
				reactionCalls++
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode reaction payload: %v", err)
				}
				reactionType, _ := payload["reaction_type"].(map[string]any)
				if reactionType["emoji_type"] != FeishuAckEmojiDefault {
					t.Fatalf("unexpected reaction payload: %+v", payload)
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"reaction_id":"reaction_1"}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.String())
				return nil, nil
			}
		}),
	})
	err := fa.sendFeishuAckReaction(context.Background(), feishuResolvedAccount{
		ID:        "main",
		AppID:     "cli_main",
		AppSecret: "secret_main",
		Domain:    "https://feishu.test",
	}, "om_message_1", adapter.ChannelConfig{})
	if err != nil {
		t.Fatalf("send ack reaction: %v", err)
	}
	if reactionCalls != 1 {
		t.Fatalf("expected one reaction call, got %d", reactionCalls)
	}
}

func TestFeishuChannelSendImageFromLocalFile(t *testing.T) {
	imageFile, err := os.CreateTemp("", "kocort-feishu-*.png")
	if err != nil {
		t.Fatalf("create temp image: %v", err)
	}
	defer os.Remove(imageFile.Name())
	if _, err := imageFile.Write([]byte("fake-png-data")); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	_ = imageFile.Close()

	var (
		tokenCalls  int
		uploadCalls int
		sendCalls   int
	)
	fa := NewFeishuAdapter()
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				tokenCalls++
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/images":
				uploadCalls++
				if !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
					t.Fatalf("expected multipart upload, got %s", r.Header.Get("Content-Type"))
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"image_key":"img_key_1"}}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages":
				sendCalls++
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode image send payload: %v", err)
				}
				if payload["msg_type"] != "image" {
					t.Fatalf("unexpected image send payload: %+v", payload)
				}
				if !strings.Contains(payload["content"].(string), "img_key_1") {
					t.Fatalf("unexpected image content: %v", payload["content"])
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"message_id":"om_img_1"}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	})
	result, err := fa.SendMedia(context.Background(), adapter.OutboundMessage{
		AccountID: "main",
		To:        "user:ou_user_1",
		Payload: adapter.ReplyPayload{
			MediaURL: imageFile.Name(),
		},
	}, adapter.ChannelConfig{
		DefaultAccount: "main",
		Accounts: map[string]any{
			"main": map[string]any{
				"appId":     "cli_main",
				"appSecret": "secret_main",
				"domain":    "https://feishu.test",
			},
		},
	})
	if err != nil {
		t.Fatalf("send image: %v", err)
	}
	if result.MessageID != "om_img_1" {
		t.Fatalf("unexpected image result: %+v", result)
	}
	if uploadCalls != 1 || sendCalls != 1 {
		t.Fatalf("unexpected call counts: token=%d upload=%d send=%d", tokenCalls, uploadCalls, sendCalls)
	}
}

func TestFeishuChannelSendFileFromLocalFile(t *testing.T) {
	file, err := os.CreateTemp("", "kocort-feishu-*.txt")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString("hello from file"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_ = file.Close()

	var (
		tokenCalls  int
		uploadCalls int
		sendCalls   int
	)
	fa := NewFeishuAdapter()
	fa.dc = infra.NewDynamicHTTPClientFromClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
				tokenCalls++
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`), nil
			case r.URL.Path == "/open-apis/im/v1/files":
				uploadCalls++
				mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
					t.Fatalf("unexpected content type: %s", r.Header.Get("Content-Type"))
				}
				reader := multipart.NewReader(r.Body, params["boundary"])
				partNames := map[string]bool{}
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatalf("read multipart: %v", err)
					}
					partNames[part.FormName()] = true
				}
				if !partNames["file"] || !partNames["file_name"] || !partNames["file_type"] {
					t.Fatalf("missing multipart fields: %+v", partNames)
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"file_key":"file_key_1"}}`), nil
			case r.URL.Path == "/open-apis/im/v1/messages":
				sendCalls++
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode file send payload: %v", err)
				}
				if payload["msg_type"] != "file" {
					t.Fatalf("unexpected file send payload: %+v", payload)
				}
				if !strings.Contains(payload["content"].(string), "file_key_1") {
					t.Fatalf("unexpected file content: %v", payload["content"])
				}
				return jsonHTTPResponse(http.StatusOK, `{"code":0,"msg":"ok","data":{"message_id":"om_file_1"}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	})
	result, err := fa.SendMedia(context.Background(), adapter.OutboundMessage{
		AccountID: "main",
		To:        "chat:oc_group_1",
		Payload: adapter.ReplyPayload{
			MediaURL: file.Name(),
		},
	}, adapter.ChannelConfig{
		DefaultAccount: "main",
		Accounts: map[string]any{
			"main": map[string]any{
				"appId":     "cli_main",
				"appSecret": "secret_main",
				"domain":    "https://feishu.test",
			},
		},
	})
	if err != nil {
		t.Fatalf("send file: %v", err)
	}
	if result.MessageID != "om_file_1" {
		t.Fatalf("unexpected file result: %+v", result)
	}
	if uploadCalls != 1 || sendCalls != 1 {
		t.Fatalf("unexpected call counts: token=%d upload=%d send=%d", tokenCalls, uploadCalls, sendCalls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func fileHTTPResponse(status int, contentType, fileName, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type":        []string{contentType},
			"Content-Disposition": []string{`attachment; filename="` + fileName + `"`},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func stringPtr(value string) *string {
	return &value
}
