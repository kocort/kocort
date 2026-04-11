package ffi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kocort/kocort/internal/localmodel/download"
)

// LibProgressFunc is a callback for library download progress.
// downloaded and total are in bytes; total may be -1 if unknown.
type LibProgressFunc func(downloaded, total int64)

const (
	// LlamaCppVersion is the pinned compatible llama.cpp release version.
	LlamaCppVersion = "b8720"

	// GithubReleaseBase is the base URL for llama.cpp GitHub releases.
	GithubReleaseBase = "https://github.com/ggml-org/llama.cpp/releases/download"
)

// DownloadConfig configures library download behavior.
type DownloadConfig struct {
	Version    string       // llama.cpp version (default: LlamaCppVersion)
	CacheDir   string       // local cache directory (default: ~/.kocort/lib/)
	GPUType    string       // "cpu", "vulkan", "cuda-12.4", "cuda-13.1", "rocm-7.2", "hip"
	HTTPClient *http.Client // HTTP client (with proxy support); nil uses http.DefaultClient
}

// LibVariant describes a downloaded library variant (version + GPU type).
type LibVariant struct {
	Version string `json:"version"`
	GPUType string `json:"gpuType"`
}

// variantDirName returns the cache directory name for a version+GPU combination.
// Format: "llama-{version}-{gpu}" (e.g. "llama-b8720-cpu", "llama-b8720-cuda-12.4").
func variantDirName(version, gpuType string) string {
	gpu := gpuType
	if gpu == "" {
		gpu = "cpu"
	}
	return fmt.Sprintf("llama-%s-%s", version, gpu)
}

// DefaultCacheDir returns the library cache directory.
// It uses the configured cache dir (set via SetLibraryConfig) if available,
// otherwise falls back to ~/.kocort/lib/.
func DefaultCacheDir() string {
	_, _, cacheDir, _ := getLibraryConfig()
	if cacheDir != "" {
		return cacheDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kocort", "lib")
}

// CheckLibrariesExist checks whether the required shared libraries are present
// locally for the given version and GPU type. Returns true if found, false otherwise.
func CheckLibrariesExist(cfg DownloadConfig) bool {
	if cfg.Version == "" {
		cfg.Version = LlamaCppVersion
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = DefaultCacheDir()
	}
	cfg.GPUType = ResolveGPUType(cfg.GPUType)

	dir := filepath.Join(cfg.CacheDir, variantDirName(cfg.Version, cfg.GPUType))
	required := requiredLibNames()
	if checkLibsExist(filepath.Join(dir, "build", "bin"), required) || checkLibsExist(dir, required) {
		return true
	}
	// Also check the default search paths
	if findLibDir() != "" {
		return true
	}
	return false
}

// ListDownloadedVariants returns the list of llama.cpp version+GPU variants
// that have been downloaded locally (in the cache directory).
func ListDownloadedVariants(cacheDir string) []LibVariant {
	if cacheDir == "" {
		cacheDir = DefaultCacheDir()
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil
	}
	required := requiredLibNames()
	var variants []LibVariant
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "llama-") {
			continue
		}
		rest := strings.TrimPrefix(name, "llama-")
		subDir := filepath.Join(cacheDir, name)
		binDir := filepath.Join(subDir, "build", "bin")
		if !checkLibsExist(binDir, required) && !checkLibsExist(subDir, required) {
			continue
		}
		// Parse version and GPU type from directory name.
		// New format: llama-{version}-{gpu} e.g. "llama-b8720-cpu", "llama-b8720-cuda-12.4"
		// Legacy format: llama-{version} e.g. "llama-b8720"
		version, gpuType := parseVariantDir(rest)
		variants = append(variants, LibVariant{Version: version, GPUType: gpuType})
	}
	return variants
}

// parseVariantDir splits a directory suffix (after "llama-") into version and GPU type.
// Format: "b8720-cpu", "b8720-cuda-12.4", etc.
func parseVariantDir(rest string) (version, gpuType string) {
	// Known GPU suffixes to check (longest first to avoid partial matches)
	knownGPU := []string{"cuda-12.4", "cuda-13.1", "rocm-7.2", "vulkan", "cpu", "hip"}
	for _, g := range knownGPU {
		suffix := "-" + g
		if strings.HasSuffix(rest, suffix) {
			return strings.TrimSuffix(rest, suffix), g
		}
	}
	// Unknown suffix – treat entire rest as version, GPU unknown
	return rest, ""
}

// AvailableGPUTypes returns the list of valid GPU type options for the current platform.
func AvailableGPUTypes() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"auto", "cpu"}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return []string{"auto", "cpu", "vulkan", "rocm-7.2"}
		case "arm64":
			return []string{"auto", "cpu", "vulkan"}
		default:
			return []string{"auto", "cpu"}
		}
	case "windows":
		if runtime.GOARCH == "amd64" {
			return []string{"auto", "cpu", "vulkan", "cuda-12.4", "cuda-13.1", "hip"}
		}
		return []string{"auto", "cpu"}
	default:
		return []string{"auto", "cpu"}
	}
}

// EnsureLibraries ensures the required shared libraries are present locally.
// Downloads from GitHub Releases if not found. Returns the library directory path.
func EnsureLibraries(cfg DownloadConfig) (string, error) {
	return EnsureLibrariesWithContext(context.Background(), cfg, nil)
}

// EnsureLibrariesWithContext is like EnsureLibraries but supports cancellation
// via context and reports download progress through the optional callback.
func EnsureLibrariesWithContext(ctx context.Context, cfg DownloadConfig, progress LibProgressFunc) (string, error) {
	if cfg.Version == "" {
		cfg.Version = LlamaCppVersion
	}
	if cfg.CacheDir == "" {
		home, _ := os.UserHomeDir()
		cfg.CacheDir = filepath.Join(home, ".kocort", "lib")
	}
	cfg.GPUType = ResolveGPUType(cfg.GPUType)

	targetDir := filepath.Join(cfg.CacheDir, variantDirName(cfg.Version, cfg.GPUType))
	binDir := filepath.Join(targetDir, "build", "bin")

	// Check if already downloaded (try both targetDir and binDir)
	required := requiredLibNames()
	if checkLibsExist(binDir, required) {
		return binDir, nil
	}
	if checkLibsExist(targetDir, required) {
		return targetDir, nil
	}

	// Determine download URL
	downloadURL := buildDownloadURL(cfg.Version, cfg.GPUType)
	slog.Info("[ffi] downloading llama.cpp libraries",
		"url", downloadURL,
		"target", targetDir)

	// Download and extract
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	if err := download.DownloadAndExtract(ctx, downloadURL, targetDir, client, download.ProgressCallback(progress)); err != nil {
		return "", fmt.Errorf("download llama.cpp libraries: %w", err)
	}

	// Download CUDA runtime DLLs if needed (Windows + CUDA only)
	if cudartURL := buildCUDARTDownloadURL(cfg.Version, cfg.GPUType); cudartURL != "" {
		slog.Info("[ffi] downloading CUDA runtime DLLs", "url", cudartURL)
		if err := download.DownloadAndExtract(ctx, cudartURL, targetDir, client, nil); err != nil {
			slog.Warn("[ffi] CUDA runtime DLLs download failed (may already be installed)", "error", err)
		}
	}

	// Find the actual lib directory (may be in build/bin/)
	if checkLibsExist(binDir, required) {
		return binDir, nil
	}
	if checkLibsExist(targetDir, required) {
		return targetDir, nil
	}

	// Try to find libs recursively
	var libDir string
	_ = filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || libDir != "" {
			return err
		}
		if info.Name() == required[0] {
			libDir = filepath.Dir(path)
			return filepath.SkipAll
		}
		return nil
	})
	if libDir != "" {
		return libDir, nil
	}

	return "", fmt.Errorf("libraries not found after extraction in %s", targetDir)
}

// buildDownloadURL constructs the download URL for the given platform/GPU.
func buildDownloadURL(version, gpuType string) string {
	var suffix string
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			suffix = "macos-arm64"
		} else {
			suffix = "macos-x64"
		}
	case "linux":
		arch := "x64"
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		}
		switch gpuType {
		case "vulkan":
			suffix = fmt.Sprintf("ubuntu-vulkan-%s", arch)
		case "rocm-7.2":
			suffix = fmt.Sprintf("ubuntu-rocm-7.2-%s", arch)
		default:
			suffix = fmt.Sprintf("ubuntu-%s", arch)
		}
	case "windows":
		arch := "x64"
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		}
		switch gpuType {
		case "cuda-12.4":
			suffix = fmt.Sprintf("win-cuda-12.4-%s", arch)
		case "cuda-13.1":
			suffix = fmt.Sprintf("win-cuda-13.1-%s", arch)
		case "vulkan":
			suffix = fmt.Sprintf("win-vulkan-%s", arch)
		case "hip":
			suffix = fmt.Sprintf("win-hip-radeon-%s", arch)
		default:
			suffix = fmt.Sprintf("win-cpu-%s", arch)
		}
	}

	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}

	return fmt.Sprintf("%s/%s/llama-%s-bin-%s.%s",
		GithubReleaseBase, version, version, suffix, ext)
}

// buildCUDARTDownloadURL returns the URL for the CUDA runtime DLLs archive
// that must be downloaded alongside CUDA-based builds on Windows.
// Returns "" if no CUDA runtime download is needed.
func buildCUDARTDownloadURL(version, gpuType string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	var cudaVer string
	switch gpuType {
	case "cuda-12.4":
		cudaVer = "12.4"
	case "cuda-13.1":
		cudaVer = "13.1"
	default:
		return ""
	}
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	return fmt.Sprintf("%s/%s/cudart-llama-bin-win-cuda-%s-%s.zip",
		GithubReleaseBase, version, cudaVer, arch)
}
