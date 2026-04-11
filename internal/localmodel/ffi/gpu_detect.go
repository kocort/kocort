package ffi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DetectBestGPU returns the best available GPU backend type for the current
// platform by probing for drivers and runtime libraries.
//
// Detection priority per OS:
//
//	Windows (x64): NVIDIA CUDA > AMD HIP > Vulkan > CPU
//	Linux   (x64): AMD ROCm > Vulkan > CPU
//	Linux (arm64): Vulkan > CPU
//	macOS:         CPU (Metal is built into the macOS binary)
func DetectBestGPU() string {
	switch runtime.GOOS {
	case "windows":
		return detectGPUWindows()
	case "linux":
		return detectGPULinux()
	default:
		return "cpu"
	}
}

// ResolveGPUType resolves "auto" or empty GPU type to the actual detected value.
func ResolveGPUType(gpuType string) string {
	if gpuType == "" || gpuType == "auto" {
		return DetectBestGPU()
	}
	return gpuType
}

// ── Windows GPU detection ────────────────────────────────────────────────────

func detectGPUWindows() string {
	// GPU-accelerated builds are only available for x64 on Windows.
	if runtime.GOARCH != "amd64" {
		return "cpu"
	}

	sys32 := filepath.Join(os.Getenv("WINDIR"), "System32")

	// NVIDIA CUDA (highest priority)
	// nvcuda.dll is installed by the NVIDIA display driver.
	if fileExists(filepath.Join(sys32, "nvcuda.dll")) {
		if dv := nvidiaDriverMajorVersion(); dv > 0 {
			// CUDA 13.1 requires driver >= 580
			if dv >= 580 {
				return "cuda-13.1"
			}
			// CUDA 12.4 requires driver >= 550
			if dv >= 550 {
				return "cuda-12.4"
			}
		}
		// NVIDIA GPU present but driver too old for our CUDA builds.
		// Vulkan may still work via the NVIDIA Vulkan ICD.
	}

	// AMD HIP (amdhip64.dll is shipped with AMD GPU drivers that support HIP)
	if fileExists(filepath.Join(sys32, "amdhip64.dll")) {
		return "hip"
	}

	// Vulkan (vulkan-1.dll is the Vulkan loader)
	if fileExists(filepath.Join(sys32, "vulkan-1.dll")) {
		return "vulkan"
	}

	return "cpu"
}

// ── Linux GPU detection ──────────────────────────────────────────────────────

func detectGPULinux() string {
	// ROCm (AMD) – x64-only in official releases
	if runtime.GOARCH == "amd64" && hasROCm() {
		return "rocm-7.2"
	}

	// Vulkan (works with NVIDIA and AMD GPUs, available for x64 and arm64)
	if (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") && hasVulkanLinux() {
		return "vulkan"
	}

	return "cpu"
}

// hasROCm checks for an AMD ROCm installation.
func hasROCm() bool {
	if fileExists("/opt/rocm") {
		return true
	}
	if _, err := exec.LookPath("rocminfo"); err == nil {
		return true
	}
	return false
}

// hasVulkanLinux checks for the Vulkan loader library on Linux.
func hasVulkanLinux() bool {
	paths := []string{
		"/usr/lib/x86_64-linux-gnu/libvulkan.so.1",
		"/usr/lib/aarch64-linux-gnu/libvulkan.so.1",
		"/usr/lib/libvulkan.so.1",
		"/usr/lib64/libvulkan.so.1",
	}
	for _, p := range paths {
		if fileExists(p) {
			return true
		}
	}
	return false
}

// ── NVIDIA driver version detection ──────────────────────────────────────────

// nvidiaDriverMajorVersion runs nvidia-smi and parses the major driver version.
// Returns 0 if nvidia-smi is not available or the output cannot be parsed.
//
// Minimum NVIDIA driver versions for CUDA compatibility:
//
//	CUDA 13.x: >= 580
//	CUDA 12.4: >= 550 (Linux >= 550.54, Windows >= 551.61)
func nvidiaDriverMajorVersion() int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=driver_version", "--format=csv,noheader").Output()
	if err != nil {
		return 0
	}

	line := strings.TrimSpace(string(out))
	// Multi-GPU returns multiple lines; use the first.
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	// Driver version format: "551.86" (Windows) or "550.54.14" (Linux)
	if idx := strings.IndexByte(line, '.'); idx > 0 {
		line = line[:idx]
	}
	v, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return 0
	}
	return v
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
