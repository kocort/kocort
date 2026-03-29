// drivers.go wires all built-in channel drivers into the channel registry by
// importing their packages so that each driver's init() function runs and
// calls adapter.Register.
//
// To add a new channel driver:
//  1. Create internal/channel/<drivername>/ and implement adapter.ChannelAdapter.
//  2. Call adapter.Register("<drivername>", factory) in init().
//  3. Add a blank import below.
package channel

import (
	_ "github.com/kocort/kocort/internal/channel/feishu" // registers "feishu" driver
	_ "github.com/kocort/kocort/internal/channel/weixin" // registers "weixin" driver
)
