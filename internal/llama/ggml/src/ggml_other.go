//go:build !windows

package ggml

// setDllSearchDir is a no-op on non-Windows platforms.
func setDllSearchDir(_ string) {}
