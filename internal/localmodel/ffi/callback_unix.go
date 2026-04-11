//go:build !windows

package ffi

import "github.com/kocort/purego"

// newLogCallbackPlatform creates a log callback. On Unix, purego supports
// void-returning callbacks natively.
func newLogCallbackPlatform(fn func(level int32, text *byte, userData uintptr)) uintptr {
	return purego.NewCallback(fn)
}

// newProgressCallbackPlatform creates a progress callback. On Unix, purego
// supports float32 arguments natively.
func newProgressCallbackPlatform(fn func(progress float32, userData uintptr) bool) uintptr {
	return purego.NewCallback(func(progress float32, userData uintptr) uintptr {
		if fn(progress, userData) {
			return 1
		}
		return 0
	})
}

// newAbortCallbackPlatform creates an abort callback.
func newAbortCallbackPlatform(fn func(userData uintptr) bool) uintptr {
	return purego.NewCallback(func(userData uintptr) uintptr {
		if fn(userData) {
			return 1
		}
		return 0
	})
}
