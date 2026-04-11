//go:build !windows

package ffi

import "github.com/kocort/purego"

// setDllSearchDir is a no-op on Unix; LD_LIBRARY_PATH / RPATH handle this.
func setDllSearchDir(_ string) {}

func openLib(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func closeLib(handle uintptr) {
	purego.Dlclose(handle)
}

func findSymbol(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}
