package llamadl

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// LlamaCppVersion is the pinned compatible llama.cpp release version.
	LlamaCppVersion = "b8720"

	// GithubReleaseBase is the base URL for llama.cpp GitHub releases.
	GithubReleaseBase = "https://github.com/ggml-org/llama.cpp/releases/download"
)

// DownloadConfig configures library download behavior.
type DownloadConfig struct {
	Version  string // llama.cpp version (default: LlamaCppVersion)
	CacheDir string // local cache directory (default: ~/.kocort/lib/)
	GPUType  string // "cpu", "vulkan", "cuda-12.4", "cuda-13.1", "rocm-7.2", "hip"
	ProxyURL string // HTTP proxy URL
}

// EnsureLibraries ensures the required shared libraries are present locally.
// Downloads from GitHub Releases if not found. Returns the library directory path.
func EnsureLibraries(cfg DownloadConfig) (string, error) {
	if cfg.Version == "" {
		cfg.Version = LlamaCppVersion
	}
	if cfg.CacheDir == "" {
		home, _ := os.UserHomeDir()
		cfg.CacheDir = filepath.Join(home, ".kocort", "lib")
	}

	targetDir := filepath.Join(cfg.CacheDir, "llama-"+cfg.Version)
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
	slog.Info("[llamadl] downloading llama.cpp libraries",
		"url", downloadURL,
		"target", targetDir)

	// Download and extract
	if err := downloadAndExtract(downloadURL, targetDir, cfg.ProxyURL); err != nil {
		return "", fmt.Errorf("download llama.cpp libraries: %w", err)
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

// downloadAndExtract downloads an archive from url and extracts it to targetDir.
func downloadAndExtract(archiveURL, targetDir, proxyURL string) error {
	// Create target directory
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Build HTTP client with optional proxy
	client := &http.Client{}
	if proxyURL != "" {
		proxyParsed, err := url.Parse(proxyURL)
		if err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyParsed)}
		}
	}

	resp, err := client.Get(archiveURL)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, archiveURL)
	}

	if strings.HasSuffix(archiveURL, ".zip") {
		return extractZip(resp.Body, targetDir)
	}
	return extractTarGz(resp.Body, targetDir)
}

// extractTarGz extracts a .tar.gz archive to targetDir.
func extractTarGz(r io.Reader, targetDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		target := filepath.Join(targetDir, header.Name)

		// Security: prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(targetDir)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

// extractZip extracts a .zip archive to targetDir.
// Since zip requires seeking, we first save to a temp file.
func extractZip(r io.Reader, targetDir string) error {
	// Save to temp file for seeking
	tmpFile, err := os.CreateTemp("", "kocort-llama-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, r); err != nil {
		return err
	}

	stat, err := tmpFile.Stat()
	if err != nil {
		return err
	}

	zr, err := zip.NewReader(tmpFile, stat.Size())
	if err != nil {
		return err
	}

	for _, f := range zr.File {
		target := filepath.Join(targetDir, f.Name)

		// Security: prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(targetDir)) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
