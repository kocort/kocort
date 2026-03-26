// Package channel defines the channel integration layer for processing
// inbound messages from external platforms and delivering outbound replies.
//
// # Architecture
//
// This package contains:
//   - ChannelRegistry for registering and resolving channel adapters
//   - adapter.BuildIntegration for config-driven adapter creation
//   - Text chunking utilities (chunking.go)
//   - HTTP client helpers (http_client.go)
//
// All channel drivers live in sub-packages and self-register via adapter.Register
// in their init() function:
//
//	internal/channel/adapter/  — ChannelAdapter interface, types, registry
//	internal/channel/feishu/   — Feishu/Lark channel
//	internal/channel/weixin/   — WeiXin/WeChat channel
//
// # Adding a New Channel
//
//  1. Create internal/channel/<drivername>/ with an adapter.go file.
//  2. Implement the adapter.ChannelAdapter interface.
//  3. Call adapter.Register("<drivername>", factory) from init().
//  4. Add a blank import in runtime/channels.go to load the driver.
package channel
