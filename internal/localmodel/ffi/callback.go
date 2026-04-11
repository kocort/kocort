package ffi

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync/atomic"
)

// newLogCallback creates a C-callable function pointer for llama.cpp log messages.
// The C signature is: void (*)(enum ggml_log_level, const char *, void *)
func newLogCallback() uintptr {
	const ggmlLogLevelInfo = 3
	return newLogCallbackPlatform(func(level int32, text *byte, userData uintptr) {
		goText := gostr(text)
		slogLevel := slog.Level(int(level-ggmlLogLevelInfo) * 4)
		if slog.Default().Enabled(context.TODO(), slogLevel) {
			fmt.Fprint(os.Stderr, goText)
		}
	})
}

// newProgressCallback creates a C-callable function pointer for model loading progress.
// The C signature is: bool (*)(float progress, void * user_data)
func newProgressCallback(fn func(float32)) uintptr {
	return newProgressCallbackPlatform(func(progress float32, userData uintptr) bool {
		fn(progress)
		return true // continue loading
	})
}

// newAbortCallback creates a C-callable function pointer for the abort check.
// The C signature is: bool (*)(void * data)
func newAbortCallback(flag *atomic.Bool) uintptr {
	return newAbortCallbackPlatform(func(userData uintptr) bool {
		return flag.Load()
	})
}

// float32ToUintptr reinterprets float32 bits as a uintptr value.
func float32ToUintptr(f float32) uintptr {
	return uintptr(math.Float32bits(f))
}

// uintptrToFloat32 reinterprets uintptr bits as a float32.
func uintptrToFloat32(u uintptr) float32 {
	return math.Float32frombits(uint32(u))
}
