package ggml

// #cgo CXXFLAGS: -std=c++17
// #cgo CPPFLAGS: -DNDEBUG -DGGML_USE_CPU -DGGML_VERSION=0x0 -DGGML_COMMIT=0x0
// #cgo CPPFLAGS: -I${SRCDIR}/../include -I${SRCDIR}/ggml-cpu
// #cgo windows CFLAGS: -Wno-dll-attribute-on-redeclaration
// #cgo windows LDFLAGS: -lmsvcrt -static -static-libgcc -static-libstdc++
// #include <stdlib.h>
// #include "ggml-backend.h"
// extern void sink(int level, char *text, void *user_data);
// static struct ggml_backend_feature * first_feature(ggml_backend_get_features_t fp, ggml_backend_reg_t reg) { return fp(reg); }
// static struct ggml_backend_feature * next_feature(struct ggml_backend_feature * feature) { return &feature[1]; }
/*
typedef enum { COMPILER_CLANG, COMPILER_GNUC, COMPILER_UNKNOWN } COMPILER;
static COMPILER compiler_name(void) {
#if defined(__clang__)
	return COMPILER_CLANG;
#elif defined(__GNUC__)
	return COMPILER_GNUC;
#else
	return COMPILER_UNKNOWN;
#endif
}
*/
import "C"

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	_ "github.com/kocort/kocort/internal/llama/ggml/src/ggml-cpu"
)

func init() {
	C.ggml_log_set(C.ggml_log_callback(C.sink), nil)
}

//export sink
func sink(level C.int, text *C.char, _ unsafe.Pointer) {
	// slog levels zeros INFO and are multiples of 4
	if slog.Default().Enabled(context.TODO(), slog.Level(int(level-C.GGML_LOG_LEVEL_INFO)*4)) {
		fmt.Fprint(os.Stderr, C.GoString(text))
	}
}

var OnceLoad = sync.OnceFunc(func() {
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("failed to get executable path", "error", err)
		exe = "."
	}

	// Compute exe-relative library path based on platform convention.
	var exeLib string
	switch runtime.GOOS {
	case "darwin":
		exeLib = filepath.Dir(exe)
	case "windows":
		exeLib = filepath.Join(filepath.Dir(exe), "lib", "kocort")
	default:
		exeLib = filepath.Join(filepath.Dir(exe), "..", "lib", "kocort")
	}

	// Check KOCORT_LIBRARY_PATH first, then OLLAMA_LIBRARY_PATH, then default.
	// This allows reusing pre-built GPU backend libraries (e.g. ggml-cuda.dll)
	// from an existing ollama installation.
	paths, ok := os.LookupEnv("KOCORT_LIBRARY_PATH")
	if !ok {
		paths, ok = os.LookupEnv("OLLAMA_LIBRARY_PATH")
	}
	if !ok {
		// Build default search paths, only include directories that actually exist.
		// Priority: exe-relative → cwd/lib/kocort → ollama fallback.
		// cwd-relative is needed for `go run` where the temp exe is not alongside lib/.
		var candidates []string
		if dirExists(exeLib) {
			candidates = append(candidates, exeLib)
		}
		if cwd, err := os.Getwd(); err == nil {
			cwdLib := filepath.Join(cwd, "lib", "kocort")
			if cwdLib != exeLib && dirExists(cwdLib) {
				candidates = append(candidates, cwdLib)
			}
		}
		if ollamaFallback := discoverOllamaLibPath(); ollamaFallback != "" {
			slog.Debug("auto-detected ollama library path", "path", ollamaFallback)
			candidates = append(candidates, ollamaFallback)
		}
		if len(candidates) == 0 {
			slog.Warn("no GPU backend library directories found")
		}
		paths = strings.Join(candidates, string(filepath.ListSeparator))
		slog.Debug("no library path env set, using defaults", "search", paths)
	}

	libPaths = filepath.SplitList(paths)
	visited := make(map[string]struct{}, len(libPaths))
	for _, path := range libPaths {
		abspath, err := filepath.Abs(path)
		if err != nil {
			slog.Error("failed to get absolute path", "error", err)
			continue
		}

		if _, ok := visited[abspath]; ok {
			continue
		}
		visited[abspath] = struct{}{}

		if !dirExists(abspath) {
			slog.Debug("skipping non-existent library path", "path", abspath)
			continue
		}

		slog.Debug("ggml backend load all from path", "path", abspath)
		// On Windows, add the library directory to the DLL search path
		// so that ggml-vulkan.dll can find ggml-base.dll in the same directory.
		if runtime.GOOS == "windows" {
			setDllSearchDir(abspath)
		}
		func() {
			cpath := C.CString(abspath)
			defer C.free(unsafe.Pointer(cpath))
			C.ggml_backend_load_all_from_path(cpath)
		}()
	}

	slog.Info("system", "", system{})
})

// dirExists returns true if path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

var libPaths []string

func LibPaths() []string {
	return libPaths
}

type system struct{}

func (system) LogValue() slog.Value {
	var attrs []slog.Attr
	names := make(map[string]int)
	for i := range C.ggml_backend_dev_count() {
		r := C.ggml_backend_dev_backend_reg(C.ggml_backend_dev_get(i))

		func() {
			fName := C.CString("ggml_backend_get_features")
			defer C.free(unsafe.Pointer(fName))

			if fn := C.ggml_backend_reg_get_proc_address(r, fName); fn != nil {
				var features []any
				for f := C.first_feature(C.ggml_backend_get_features_t(fn), r); f.name != nil; f = C.next_feature(f) {
					features = append(features, C.GoString(f.name), C.GoString(f.value))
				}

				name := C.GoString(C.ggml_backend_reg_name(r))
				attrs = append(attrs, slog.Group(name+"."+strconv.Itoa(names[name]), features...))
				names[name] += 1
			}
		}()
	}

	switch C.compiler_name() {
	case C.COMPILER_CLANG:
		attrs = append(attrs, slog.String("compiler", "cgo(clang)"))
	case C.COMPILER_GNUC:
		attrs = append(attrs, slog.String("compiler", "cgo(gcc)"))
	default:
		attrs = append(attrs, slog.String("compiler", "cgo(unknown)"))
	}

	return slog.GroupValue(attrs...)
}

// discoverOllamaLibPath tries to find an installed ollama's library directory
// on the current platform. Returns empty string if not found.
func discoverOllamaLibPath() string {
	switch runtime.GOOS {
	case "windows":
		// Standard ollama install location on Windows
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			return ""
		}
		candidate := filepath.Join(localAppData, "Programs", "Ollama", "lib", "ollama")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	case "linux":
		// Standard ollama locations on Linux
		for _, candidate := range []string{
			"/usr/local/lib/ollama",
			"/usr/lib/ollama",
		} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		}
	case "darwin":
		// On macOS, ollama typically bundles Metal support statically
		return ""
	}
	return ""
}
