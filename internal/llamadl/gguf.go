package llamadl

import (
	"errors"
	"runtime"
	"unsafe"
)

// GetModelArch reads the "general.architecture" metadata from a GGUF file.
func GetModelArch(lib *Library, modelPath string) (string, error) {
	pathPtr, pathBuf := cstr(modelPath)

	params := cGgufInitParams{NoAlloc: true, Ctx: 0}
	ctx := lib.fnGgufInitFromFile(pathPtr, uintptr(unsafe.Pointer(&params)))
	runtime.KeepAlive(pathBuf)

	if ctx == 0 {
		return "", errors.New("unable to load model file")
	}
	defer lib.fnGgufFree(ctx)

	keyPtr, keyBuf := cstr("general.architecture")
	idx := lib.fnGgufFindKey(ctx, keyPtr)
	runtime.KeepAlive(keyBuf)

	if idx < 0 {
		return "", errors.New("unknown model architecture")
	}

	archPtr := lib.fnGgufGetValStr(ctx, idx)
	return gostr(archPtr), nil
}
