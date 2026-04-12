package ffi

import (
	"syscall"

	"github.com/kocort/purego"
)

// newLogCallbackPlatform creates a log callback for Windows.
// Windows syscall.NewCallbackCDecl requires:
//   - all args uintptr-sized
//   - exactly one uintptr-sized return value
//
// C signature: void (*)(int level, const char *text, void *user_data)
// We return 0 (ignored by caller) to satisfy the Windows constraint.
func newLogCallbackPlatform(fn func(level int32, text *byte, userData uintptr)) uintptr {
	return purego.NewCallback(func(level int32, text *byte, userData uintptr) uintptr {
		fn(level, text, userData)
		return 0
	})
}

// newProgressCallbackPlatform creates a progress callback for Windows.
// C signature: bool (*)(float progress, void *user_data)
// On Windows x64, float is passed in XMM0 but syscall.NewCallbackCDecl receives
// integer registers only. The float bits are reinterpreted via the integer register.
func newProgressCallbackPlatform(fn func(progress float32, userData uintptr) bool) uintptr {
	return syscall.NewCallbackCDecl(func(progressBits uintptr, userData uintptr) uintptr {
		progress := uintptrToFloat32(progressBits)
		if fn(progress, userData) {
			return 1
		}
		return 0
	})
}

// newAbortCallbackPlatform creates an abort callback for Windows.
// C signature: bool (*)(void *data)
func newAbortCallbackPlatform(fn func(userData uintptr) bool) uintptr {
	return syscall.NewCallbackCDecl(func(userData uintptr) uintptr {
		if fn(userData) {
			return 1
		}
		return 0
	})
}
