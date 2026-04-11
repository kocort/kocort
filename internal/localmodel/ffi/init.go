// Package ffi provides pure-Go dynamic loading of llama.cpp shared libraries.
//
// It replaces the previous CGO-based internal/llama package with zero CGO dependency,
// using github.com/kocort/purego for dlopen/dlsym at runtime. Libraries are
// downloaded from official llama.cpp GitHub Releases on first use.
//
// # Quick start
//
//	ffi.BackendInit()  // Initialize global library singleton
//	model, _ := ffi.LoadModelFromFile(ffi.DefaultLibrary(), path, params)
//	ctx, _ := ffi.NewContextWithModel(ffi.DefaultLibrary(), model, ctxParams)
//
// # Environment variables
//
//	KOCORT_LLAMA_LIB_DIR   — Override library directory (skip download)
//	KOCORT_LLAMA_VERSION   — Override download version (default: b8720)
//	KOCORT_LLAMA_GPU       — GPU type: cpu, vulkan, cuda-12.4, cuda-13.1, rocm-7.2
package ffi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

var (
	globalLib    *Library
	globalLibMu  sync.Mutex
	globalLibErr error

	// configuredVersion, configuredGPUType, and configuredCacheDir hold the
	// runtime config values used for library download.
	// Set via SetLibraryConfig before BackendInit.
	configuredVersion    string
	configuredGPUType    string
	configuredCacheDir   string // base dir for lib cache (e.g. configDir/lib); empty = ~/.kocort/lib
	configuredHTTPClient *http.Client
	configMu             sync.Mutex
)

// SetLibraryConfig sets the library version, GPU type, base cache directory,
// and HTTP client to use when downloading libraries. Must be called before
// BackendInit.
//
// cacheDir is the base directory for the library cache (e.g. configDir + "/lib").
// Pass "" to fall back to ~/.kocort/lib.
func SetLibraryConfig(version, gpuType, cacheDir string, httpClient *http.Client) {
	configMu.Lock()
	defer configMu.Unlock()
	configuredVersion = version
	configuredGPUType = gpuType
	configuredCacheDir = cacheDir
	configuredHTTPClient = httpClient
}

// getLibraryConfig returns the current configured version, GPU type, cache dir, and HTTP client.
func getLibraryConfig() (version, gpuType, cacheDir string, httpClient *http.Client) {
	configMu.Lock()
	defer configMu.Unlock()
	return configuredVersion, configuredGPUType, configuredCacheDir, configuredHTTPClient
}

// BackendInit initializes the llama.cpp backend by:
// 1. Locating or downloading the shared libraries
// 2. dlopen-ing all required libraries
// 3. Calling llama_backend_init()
//
// This is safe to call multiple times; subsequent calls are no-ops if the
// library is already loaded. Use BackendReinit to force re-initialization
// with a potentially different GPU variant.
func BackendInit() {
	globalLibMu.Lock()
	defer globalLibMu.Unlock()
	if globalLib != nil {
		return
	}
	backendInitLocked()
}

// BackendReinit closes any currently loaded library and re-initializes the
// backend from scratch using the current configuration. This allows switching
// between GPU variants (e.g. CPU → CUDA) at runtime without restarting the
// process.
//
// The caller must ensure no inference is active when calling this function.
func BackendReinit() error {
	globalLibMu.Lock()
	defer globalLibMu.Unlock()
	if globalLib != nil {
		slog.Info("[ffi] closing existing library for reinit", "libDir", globalLib.LibDir())
		globalLib.Close()
		globalLib = nil
		globalLibErr = nil
	}
	backendInitLocked()
	return globalLibErr
}

// backendInitLocked performs the actual init. Must be called with globalLibMu held.
func backendInitLocked() {
	lib, err := initLibrary()
	if err != nil {
		globalLibErr = err
		slog.Error("[ffi] backend init failed", "error", err)
		return
	}
	globalLib = lib
	globalLibErr = nil
	lib.fnLlamaBackendInit()

	// Set up log forwarding
	lib.fnLlamaLogSet(newLogCallback(), 0)

	slog.Info("[ffi] backend initialized", "libDir", lib.libDir)
}

// DefaultLibrary returns the global Library singleton.
// Must be called after BackendInit().
func DefaultLibrary() *Library {
	globalLibMu.Lock()
	defer globalLibMu.Unlock()
	return globalLib
}

// LibraryError returns any error from library initialization.
func LibraryError() error {
	globalLibMu.Lock()
	defer globalLibMu.Unlock()
	return globalLibErr
}

// LibraryStatus returns information about the currently loaded library.
// Returns version (from config or default), GPU type, library directory, and
// whether the library is loaded.
func LibraryStatus() (version, gpuType, libDir string, loaded bool) {
	cfgVer, cfgGPU, _, _ := getLibraryConfig()
	if cfgVer == "" {
		cfgVer = LlamaCppVersion
	}
	globalLibMu.Lock()
	lib := globalLib
	globalLibMu.Unlock()
	if lib != nil {
		return cfgVer, cfgGPU, lib.LibDir(), true
	}
	return cfgVer, cfgGPU, "", false
}

// EnsureLibrariesReady checks if libraries are present and downloads them
// if needed. This is intended to be called before model downloads to ensure
// the inference runtime is available.
func EnsureLibrariesReady() error {
	return EnsureLibrariesReadyWithContext(context.Background(), nil)
}

// EnsureLibrariesReadyWithContext is like EnsureLibrariesReady but supports
// cancellation via context and reports download progress through the callback.
func EnsureLibrariesReadyWithContext(ctx context.Context, progress LibProgressFunc) error {
	cfgVer, cfgGPU, _, cfgClient := getLibraryConfig()
	if cfgVer == "" {
		cfgVer = os.Getenv("KOCORT_LLAMA_VERSION")
		if cfgVer == "" {
			cfgVer = LlamaCppVersion
		}
	}
	if cfgGPU == "" {
		cfgGPU = os.Getenv("KOCORT_LLAMA_GPU")
	}

	cfg := DownloadConfig{
		Version:    cfgVer,
		GPUType:    cfgGPU,
		HTTPClient: cfgClient,
	}

	if CheckLibrariesExist(cfg) {
		return nil
	}

	slog.Info("[ffi] libraries not found, downloading...", "version", cfgVer, "gpu", cfgGPU)
	_, err := EnsureLibrariesWithContext(ctx, cfg, progress)
	return err
}

// initLibrary locates the library directory and opens all shared libraries.
func initLibrary() (*Library, error) {
	libDir, err := resolveLibDir()
	if err != nil {
		return nil, err
	}
	return Open(libDir)
}

// resolveLibDir determines the library directory to use based on environment
// variables, configured GPU type, and local cache. If no existing directory
// is found it downloads the libraries first.
func resolveLibDir() (string, error) {
	// 1. Check environment override
	if libDir := os.Getenv("KOCORT_LLAMA_LIB_DIR"); libDir != "" {
		slog.Info("[ffi] using KOCORT_LLAMA_LIB_DIR", "dir", libDir)
		return libDir, nil
	}

	// 2. Resolve configured version and GPU type
	cfgVer, cfgGPU, _, _ := getLibraryConfig()

	version := os.Getenv("KOCORT_LLAMA_VERSION")
	if version == "" {
		version = cfgVer
	}
	if version == "" {
		version = LlamaCppVersion
	}

	gpuType := os.Getenv("KOCORT_LLAMA_GPU")
	if gpuType == "" {
		gpuType = cfgGPU
	}
	gpuType = ResolveGPUType(gpuType)

	// 3. Check the configured variant directory first (GPU-type-aware).
	cacheDir := DefaultCacheDir()
	targetDir := filepath.Join(cacheDir, variantDirName(version, gpuType))
	required := requiredLibNames()
	binDir := filepath.Join(targetDir, "build", "bin")
	if checkLibsExist(binDir, required) {
		slog.Info("[ffi] found configured variant libraries", "dir", binDir, "gpu", gpuType)
		return binDir, nil
	}
	if checkLibsExist(targetDir, required) {
		slog.Info("[ffi] found configured variant libraries", "dir", targetDir, "gpu", gpuType)
		return targetDir, nil
	}

	// 4. Fall back to generic search (system paths, other cached variants)
	if libDir := findLibDir(); libDir != "" {
		slog.Info("[ffi] found existing libraries", "dir", libDir)
		return libDir, nil
	}

	// 5. Download from GitHub Releases
	_, _, _, cfgClient := getLibraryConfig()
	libDir, err := EnsureLibraries(DownloadConfig{
		Version:    version,
		GPUType:    gpuType,
		HTTPClient: cfgClient,
	})
	if err != nil {
		return "", fmt.Errorf("ensure libraries: %w", err)
	}

	return libDir, nil
}
