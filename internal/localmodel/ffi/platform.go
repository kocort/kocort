package ffi

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LibExtension returns the shared library file extension for the current platform.
func LibExtension() string {
	switch runtime.GOOS {
	case "darwin":
		return ".dylib"
	case "windows":
		return ".dll"
	default:
		return ".so"
	}
}

// LibPrefix returns the shared library file prefix for the current platform.
func LibPrefix() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return "lib"
}

// LibName builds the full shared library filename for the given base name.
// e.g. LibName("llama") → "libllama.dylib" on macOS, "llama.dll" on Windows.
func LibName(base string) string {
	return LibPrefix() + base + LibExtension()
}

// defaultLibSearchPaths returns platform-specific directories to search for libraries.
func defaultLibSearchPaths() []string {
	var paths []string

	// User override via environment variable
	if p := os.Getenv("KOCORT_LLAMA_LIB_DIR"); p != "" {
		paths = append(paths, p)
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		// Default download cache location
		paths = append(paths, filepath.Join(home, ".kocort", "lib"))
	}

	// System library paths
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths,
			"/usr/local/lib",
			"/opt/homebrew/lib",
		)
	case "linux":
		paths = append(paths,
			"/usr/local/lib",
			"/usr/lib",
		)
		if ldPath := os.Getenv("LD_LIBRARY_PATH"); ldPath != "" {
			paths = append(paths, strings.Split(ldPath, ":")...)
		}
	case "windows":
		if sysRoot := os.Getenv("SystemRoot"); sysRoot != "" {
			paths = append(paths, filepath.Join(sysRoot, "System32"))
		}
	}

	return paths
}

// discoverOllamaLibPath tries to locate Ollama's lib directory as a fallback
// for GPU backend libraries.
func discoverOllamaLibPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/usr/local/lib/ollama",
			filepath.Join(home, ".ollama", "lib"),
		}
	case "linux":
		candidates = []string{
			"/usr/local/lib/ollama",
			"/usr/lib/ollama",
			filepath.Join(home, ".ollama", "lib"),
		}
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "Ollama", "lib"))
		}
		candidates = append(candidates, filepath.Join(home, ".ollama", "lib"))
	}

	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// findLibDir searches for a directory containing the required llama.cpp libraries.
// It checks KOCORT_LLAMA_LIB_DIR first, then the default cache, then system paths.
// Returns empty string if no suitable directory is found.
func findLibDir() string {
	required := requiredLibNames()
	for _, dir := range defaultLibSearchPaths() {
		if checkLibsExist(dir, required) {
			return dir
		}
		// Also check subdirectories that match downloaded release layout
		// e.g. ~/.kocort/lib/llama-b8720/build/bin/
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			subDir := filepath.Join(dir, entry.Name())
			if checkLibsExist(subDir, required) {
				return subDir
			}
			// Release packages typically have build/bin/ structure
			binDir := filepath.Join(subDir, "build", "bin")
			if checkLibsExist(binDir, required) {
				return binDir
			}
		}
	}
	return ""
}

// requiredLibNames returns the library filenames required on the current platform.
func requiredLibNames() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"libllama.dylib", "libggml.dylib", "libggml-base.dylib", "libggml-cpu.dylib"}
	case "linux":
		return []string{"libllama.so", "libggml.so", "libggml-base.so", "libggml-cpu.so"}
	case "windows":
		return []string{"llama.dll", "ggml.dll", "ggml-base.dll", "ggml-cpu.dll"}
	}
	return nil
}

// checkLibsExist verifies that all given library files exist in dir.
func checkLibsExist(dir string, names []string) bool {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}
