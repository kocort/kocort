package llamadl

import "github.com/kocort/purego"

// newCallback wraps purego.NewCallback for use in this package.
// It creates a C-callable function pointer from a Go function.
func newCallback(fn any) uintptr {
	return purego.NewCallback(fn)
}
