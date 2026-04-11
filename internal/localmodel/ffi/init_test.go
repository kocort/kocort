package ffi

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// createFakeLibs creates fake library files in dir to satisfy checkLibsExist.
func createFakeLibs(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredLibNames() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// On Windows, checkLibsExist also requires at least one ggml-cpu-*.dll
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(filepath.Join(dir, "ggml-cpu-haswell.dll"), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// setupFakeHome creates a temporary directory structure mimicking ~/.kocort/lib/
// and sets the HOME/USERPROFILE env var so DefaultCacheDir() resolves there.
// Returns the lib cache directory where variant subdirectories should be placed.
func setupFakeHome(t *testing.T) string {
	t.Helper()
	fakeHome := t.TempDir()
	cacheDir := filepath.Join(fakeHome, ".kocort", "lib")
	os.MkdirAll(cacheDir, 0o755)

	switch runtime.GOOS {
	case "windows":
		t.Setenv("USERPROFILE", fakeHome)
	default:
		t.Setenv("HOME", fakeHome)
	}
	return cacheDir
}

// TestResolveLibDir_PrefersConfiguredGPUVariant verifies that when both CPU
// and CUDA variant directories exist, the configured GPU type determines
// which directory is selected — NOT whichever happens to be found first.
func TestResolveLibDir_PrefersConfiguredGPUVariant(t *testing.T) {
	cacheDir := setupFakeHome(t)
	cpuDir := filepath.Join(cacheDir, variantDirName(LlamaCppVersion, "cpu"))
	cudaDir := filepath.Join(cacheDir, variantDirName(LlamaCppVersion, "cuda-12.4"))
	createFakeLibs(t, cpuDir)
	createFakeLibs(t, cudaDir)

	t.Setenv("KOCORT_LLAMA_LIB_DIR", "")
	t.Setenv("KOCORT_LLAMA_VERSION", "")
	t.Setenv("KOCORT_LLAMA_GPU", "")

	// Test 1: Configure for CUDA → should pick CUDA dir
	SetLibraryConfig(LlamaCppVersion, "cuda-12.4", cacheDir, nil)
	dir, err := resolveLibDir()
	if err != nil {
		t.Fatalf("resolveLibDir with cuda config: %v", err)
	}
	if dir != cudaDir {
		t.Errorf("expected cuda dir %q, got %q", cudaDir, dir)
	}

	// Test 2: Configure for CPU → should pick CPU dir
	SetLibraryConfig(LlamaCppVersion, "cpu", cacheDir, nil)
	dir, err = resolveLibDir()
	if err != nil {
		t.Fatalf("resolveLibDir with cpu config: %v", err)
	}
	if dir != cpuDir {
		t.Errorf("expected cpu dir %q, got %q", cpuDir, dir)
	}
}

// TestResolveLibDir_GPUVariantBuildBin tests the build/bin subdirectory layout.
func TestResolveLibDir_GPUVariantBuildBin(t *testing.T) {
	cacheDir := setupFakeHome(t)
	binDir := filepath.Join(cacheDir, variantDirName(LlamaCppVersion, "vulkan"), "build", "bin")
	createFakeLibs(t, binDir)

	t.Setenv("KOCORT_LLAMA_LIB_DIR", "")
	t.Setenv("KOCORT_LLAMA_VERSION", "")
	t.Setenv("KOCORT_LLAMA_GPU", "")

	SetLibraryConfig(LlamaCppVersion, "vulkan", cacheDir, nil)
	dir, err := resolveLibDir()
	if err != nil {
		t.Fatalf("resolveLibDir: %v", err)
	}
	if dir != binDir {
		t.Errorf("expected build/bin dir %q, got %q", binDir, dir)
	}
}

// TestResolveLibDir_EnvOverride verifies KOCORT_LLAMA_LIB_DIR takes precedence.
func TestResolveLibDir_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("KOCORT_LLAMA_LIB_DIR", tmpDir)

	SetLibraryConfig(LlamaCppVersion, "cuda-12.4", "", nil)
	dir, err := resolveLibDir()
	if err != nil {
		t.Fatalf("resolveLibDir: %v", err)
	}
	if dir != tmpDir {
		t.Errorf("expected env override %q, got %q", tmpDir, dir)
	}
}

// TestResolveLibDir_SwitchGPU simulates the user switching from CPU to CUDA:
// Both variants exist, first resolve picks CPU, then config changes to CUDA,
// second resolve picks CUDA.
func TestResolveLibDir_SwitchGPU(t *testing.T) {
	cacheDir := setupFakeHome(t)
	cpuDir := filepath.Join(cacheDir, variantDirName(LlamaCppVersion, "cpu"))
	cudaDir := filepath.Join(cacheDir, variantDirName(LlamaCppVersion, "cuda-12.4"))
	createFakeLibs(t, cpuDir)
	createFakeLibs(t, cudaDir)

	t.Setenv("KOCORT_LLAMA_LIB_DIR", "")
	t.Setenv("KOCORT_LLAMA_VERSION", "")
	t.Setenv("KOCORT_LLAMA_GPU", "")

	// Initially CPU
	SetLibraryConfig(LlamaCppVersion, "cpu", cacheDir, nil)
	dir1, err := resolveLibDir()
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if dir1 != cpuDir {
		t.Fatalf("first resolve: expected %q, got %q", cpuDir, dir1)
	}

	// Switch to CUDA
	SetLibraryConfig(LlamaCppVersion, "cuda-12.4", cacheDir, nil)
	dir2, err := resolveLibDir()
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if dir2 != cudaDir {
		t.Fatalf("second resolve: expected %q, got %q", cudaDir, dir2)
	}

	if dir1 == dir2 {
		t.Error("GPU switch did not change resolved directory")
	}
}

// TestResolveLibDir_FallbackToGenericSearch verifies that when the configured
// GPU variant directory does not exist, resolveLibDir falls back to findLibDir
// which scans all subdirectories.
func TestResolveLibDir_FallbackToGenericSearch(t *testing.T) {
	cacheDir := setupFakeHome(t)
	// Only CPU variant exists, but user configured CUDA
	cpuDir := filepath.Join(cacheDir, variantDirName(LlamaCppVersion, "cpu"))
	createFakeLibs(t, cpuDir)

	t.Setenv("KOCORT_LLAMA_LIB_DIR", "")
	t.Setenv("KOCORT_LLAMA_VERSION", "")
	t.Setenv("KOCORT_LLAMA_GPU", "")

	// Configure for CUDA, but only CPU is available → should fall back to CPU
	SetLibraryConfig(LlamaCppVersion, "cuda-12.4", cacheDir, nil)
	dir, err := resolveLibDir()
	if err != nil {
		t.Fatalf("resolveLibDir: %v", err)
	}
	if dir != cpuDir {
		t.Errorf("expected fallback to cpu dir %q, got %q", cpuDir, dir)
	}
}

// ---------------------------------------------------------------------------
// Integration tests — require real llama.cpp libraries on the host machine.
// Skipped automatically when libraries are not present.
// ---------------------------------------------------------------------------

// realCUDALibDir returns the path to a locally downloaded CUDA variant
// directory, or "" if none is found.
func realCUDALibDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	cacheDir := filepath.Join(home, ".kocort", "lib")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return ""
	}
	required := requiredLibNames()
	for _, e := range entries {
		if !e.IsDir() || !strings.Contains(e.Name(), "cuda") {
			continue
		}
		dir := filepath.Join(cacheDir, e.Name())
		binDir := filepath.Join(dir, "build", "bin")
		if checkLibsExist(binDir, required) {
			return binDir
		}
		if checkLibsExist(dir, required) {
			return dir
		}
	}
	return ""
}

// resetGlobalLib clears the global library state for testing, saving the
// previous state so it can be restored. The returned cleanup function MUST
// be deferred.
func resetGlobalLib(t *testing.T) func() {
	t.Helper()
	globalLibMu.Lock()
	savedLib := globalLib
	savedErr := globalLibErr
	globalLib = nil
	globalLibErr = nil
	globalLibMu.Unlock()
	return func() {
		globalLibMu.Lock()
		if globalLib != nil {
			globalLib.Close()
		}
		globalLib = savedLib
		globalLibErr = savedErr
		globalLibMu.Unlock()
	}
}

// TestBackendReinit_RealCUDA loads the real CUDA llama.cpp libraries that were
// previously downloaded to ~/.kocort/lib/llama-*-cuda-* and verifies:
//  1. The library loads successfully
//  2. At least one GPU device is enumerated (CUDA backend present)
//  3. BackendReinit can close and re-open successfully
//
// This test is skipped when no CUDA variant is available locally.
func TestBackendReinit_RealCUDA(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("CUDA test only runs on Windows")
	}
	cudaDir := realCUDALibDir()
	if cudaDir == "" {
		t.Skip("no local CUDA variant found in ~/.kocort/lib/; download one first")
	}
	t.Logf("using CUDA lib dir: %s", cudaDir)

	cleanup := resetGlobalLib(t)
	defer cleanup()

	// Point directly at the CUDA dir
	t.Setenv("KOCORT_LLAMA_LIB_DIR", cudaDir)
	SetLibraryConfig(LlamaCppVersion, "cuda-13.1", "", nil)

	// First init
	BackendInit()
	lib := DefaultLibrary()
	if lib == nil {
		t.Fatalf("BackendInit failed: %v", LibraryError())
	}
	t.Logf("library loaded from %s", lib.LibDir())

	if lib.LibDir() != cudaDir {
		t.Errorf("expected libDir %q, got %q", cudaDir, lib.LibDir())
	}

	// Verify GPU devices are present (CUDA backend should expose at least one)
	gpus := EnumerateGPUs(lib)
	t.Logf("enumerated %d GPU device(s):", len(gpus))
	for _, g := range gpus {
		t.Logf("  id=%s backend=%s llamaID=%d", g.ID, g.Library, g.LlamaID)
	}
	hasCUDA := false
	for _, g := range gpus {
		if strings.Contains(strings.ToLower(g.Library), "cuda") {
			hasCUDA = true
			break
		}
	}
	if !hasCUDA {
		t.Errorf("expected at least one CUDA GPU device, found %d GPUs: %v", len(gpus), gpus)
	}

	// Reinit to verify close+reopen works
	if err := BackendReinit(); err != nil {
		t.Fatalf("BackendReinit failed: %v", err)
	}
	lib2 := DefaultLibrary()
	if lib2 == nil {
		t.Fatal("library nil after reinit")
	}
	t.Logf("reinit succeeded, library at %s", lib2.LibDir())

	// Verify GPU still enumerable after reinit
	gpus2 := EnumerateGPUs(lib2)
	if len(gpus2) == 0 {
		t.Error("no GPUs after reinit")
	}
}

// TestBackendReinit_SwitchCPUtoCUDA tests switching from CPU to CUDA variant
// using BackendReinit with real libraries. Requires both CPU and CUDA variants
// downloaded. Skipped if either is missing.
func TestBackendReinit_SwitchCPUtoCUDA(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("variant switch test only runs on Windows")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}
	cacheDir := filepath.Join(home, ".kocort", "lib")
	required := requiredLibNames()

	findVariantDir := func(gpuType string) string {
		dir := filepath.Join(cacheDir, variantDirName(LlamaCppVersion, gpuType))
		binDir := filepath.Join(dir, "build", "bin")
		if checkLibsExist(binDir, required) {
			return binDir
		}
		if checkLibsExist(dir, required) {
			return dir
		}
		return ""
	}

	cpuDir := findVariantDir("cpu")
	if cpuDir == "" {
		t.Skip("no CPU variant found")
	}
	cudaDir := realCUDALibDir()
	if cudaDir == "" {
		t.Skip("no CUDA variant found")
	}
	t.Logf("CPU dir:  %s", cpuDir)
	t.Logf("CUDA dir: %s", cudaDir)

	cleanup := resetGlobalLib(t)
	defer cleanup()

	t.Setenv("KOCORT_LLAMA_LIB_DIR", "")
	t.Setenv("KOCORT_LLAMA_VERSION", "")
	t.Setenv("KOCORT_LLAMA_GPU", "")

	// Step 1: Load CPU variant
	SetLibraryConfig(LlamaCppVersion, "cpu", cacheDir, nil)
	BackendInit()
	lib1 := DefaultLibrary()
	if lib1 == nil {
		t.Fatalf("CPU init failed: %v", LibraryError())
	}
	t.Logf("step 1: loaded CPU from %s", lib1.LibDir())
	gpus1 := EnumerateGPUs(lib1)
	t.Logf("step 1: %d GPU(s) with CPU backend", len(gpus1))

	// Step 2: Switch to CUDA via BackendReinit
	SetLibraryConfig(LlamaCppVersion, "cuda-13.1", cacheDir, nil)
	if err := BackendReinit(); err != nil {
		t.Fatalf("reinit to CUDA failed: %v", err)
	}
	lib2 := DefaultLibrary()
	if lib2 == nil {
		t.Fatalf("CUDA init failed: %v", LibraryError())
	}
	t.Logf("step 2: loaded CUDA from %s", lib2.LibDir())

	if lib1.LibDir() == lib2.LibDir() {
		t.Error("expected different library directories after GPU switch")
	}

	gpus2 := EnumerateGPUs(lib2)
	t.Logf("step 2: %d GPU(s) with CUDA backend", len(gpus2))
	hasCUDA := false
	for _, g := range gpus2 {
		t.Logf("  id=%s backend=%s", g.ID, g.Library)
		if strings.Contains(strings.ToLower(g.Library), "cuda") {
			hasCUDA = true
		}
	}
	if !hasCUDA {
		t.Error("expected CUDA GPU after switching to CUDA variant")
	}
}
