package ggml

import (
	"log/slog"
	"syscall"
	"unsafe"
)

// setDllSearchDir adds a directory to the Windows DLL search path so that
// dynamically loaded backend DLLs (e.g. ggml-vulkan.dll) can find their
// dependencies (e.g. ggml-base.dll) in the same directory.
func setDllSearchDir(dir string) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("SetDllDirectoryW")
	dirW, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		slog.Warn("failed to convert DLL directory path", "error", err)
		return
	}
	ret, _, _ := proc.Call(uintptr(unsafe.Pointer(dirW)))
	if ret == 0 {
		slog.Warn("SetDllDirectoryW failed", "dir", dir)
	} else {
		slog.Debug("set DLL search directory", "dir", dir)
	}
}
