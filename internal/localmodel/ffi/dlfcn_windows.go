package ffi

import (
	"syscall"
	"unsafe"
)

var procSetDllDirectoryW = syscall.NewLazyDLL("kernel32.dll").NewProc("SetDllDirectoryW")

// setDllSearchDir adds dir to the Windows DLL search order so that
// implicit dependencies of subsequently loaded DLLs (e.g. cublas64_13.dll
// required by ggml-cuda.dll) can be found.
// Pass "" to restore the default search order.
func setDllSearchDir(dir string) {
	if dir == "" {
		procSetDllDirectoryW.Call(0)
		return
	}
	p, _ := syscall.UTF16PtrFromString(dir)
	procSetDllDirectoryW.Call(uintptr(unsafe.Pointer(p)))
}

func openLib(path string) (uintptr, error) {
	h, err := syscall.LoadLibrary(path)
	return uintptr(h), err
}

func closeLib(handle uintptr) {
	syscall.FreeLibrary(syscall.Handle(handle))
}

func findSymbol(handle uintptr, name string) (uintptr, error) {
	proc, err := syscall.GetProcAddress(syscall.Handle(handle), name)
	return proc, err
}
