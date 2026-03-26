package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// External tool binary resolution
//
// Tools like ripgrep (rg) and fd are shipped in <exe_dir>/bin/tools/ and
// distributed alongside the application binary.
// ---------------------------------------------------------------------------

var toolBinCache struct {
	mu    sync.Mutex
	paths map[string]string
}

func init() {
	toolBinCache.paths = map[string]string{}
}

// ResolveToolBin resolves an external tool binary path.
// Search order:
//  1. <executable_dir>/bin/tools/<name>
//  2. <working_dir>/bin/tools/<name>
//  3. System PATH
//
// Returns the absolute path to the binary, or "" if not found.
func ResolveToolBin(name string) string {
	toolBinCache.mu.Lock()
	defer toolBinCache.mu.Unlock()
	if p, ok := toolBinCache.paths[name]; ok {
		return p
	}
	p := resolveToolBinUncached(name)
	toolBinCache.paths[name] = p
	return p
}

func resolveToolBinUncached(name string) string {
	bin := name
	if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") {
		bin = name + ".exe"
	}

	// 1. Next to the executable: <exe_dir>/bin/tools/<name>
	//    For standalone deployment this is the normal location.
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidate := filepath.Join(exeDir, "bin", "tools", bin)
		if isExecutableFile(candidate) {
			return candidate
		}

		// 2. macOS .app bundle awareness.
		//    Go binary lives at Contents/Resources/kocort; also check
		//    Contents/MacOS/bin/tools/ and Contents/Resources/bin/tools/ so
		//    we find tools regardless of where in the bundle they are placed.
		if runtime.GOOS == "darwin" {
			contentsDir := filepath.Dir(exeDir) // .app/Contents
			for _, sub := range []string{
				filepath.Join("MacOS", "bin", "tools", bin),
				filepath.Join("Resources", "bin", "tools", bin),
			} {
				candidate = filepath.Join(contentsDir, sub)
				if isExecutableFile(candidate) {
					return candidate
				}
			}
		}
	}

	// 3. Working directory: ./bin/tools/<name>
	if wd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(wd, "bin", "tools", bin)
		if isExecutableFile(candidate) {
			return candidate
		}
	}

	// 4. System PATH
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}

	return ""
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ClearToolBinCache clears the resolved tool binary paths cache (for testing).
func ClearToolBinCache() {
	toolBinCache.mu.Lock()
	defer toolBinCache.mu.Unlock()
	toolBinCache.paths = map[string]string{}
}
