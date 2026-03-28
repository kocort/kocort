package heartbeat

import "sync/atomic"

var heartbeatsEnabled atomic.Bool

func init() {
	heartbeatsEnabled.Store(true)
}

func SetHeartbeatsEnabled(enabled bool) {
	heartbeatsEnabled.Store(enabled)
}

func AreHeartbeatsEnabled() bool {
	return heartbeatsEnabled.Load()
}
