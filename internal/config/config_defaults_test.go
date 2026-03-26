package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestLoadDefaultAppConfigMatchesEmbeddedDefaults(t *testing.T) {
	cfg, err := LoadDefaultAppConfig(embeddedDefaultConfigJSON)
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	if cfg.Memory.Backend != "builtin" {
		t.Fatalf("expected builtin memory backend, got %q", cfg.Memory.Backend)
	}
	if cfg.Memory.Citations != "auto" {
		t.Fatalf("expected auto memory citations, got %q", cfg.Memory.Citations)
	}
	if cfg.Gateway.Port != 18789 {
		t.Fatalf("expected default gateway port 18789, got %d", cfg.Gateway.Port)
	}
	if cfg.Gateway.Auth == nil || cfg.Gateway.Auth.Mode != "none" {
		t.Fatalf("expected none auth mode in embedded defaults, got %+v", cfg.Gateway.Auth)
	}
	if cfg.Skills.Load.Watch == nil || !*cfg.Skills.Load.Watch {
		t.Fatalf("expected default skills watcher enabled")
	}
	if cfg.Skills.Install.NodeManager != "npm" {
		t.Fatalf("expected default skills node manager npm, got %q", cfg.Skills.Install.NodeManager)
	}
	if cfg.Session.ToolsVisibility != core.SessionVisibilityTree {
		t.Fatalf("expected default session visibility tree, got %q", cfg.Session.ToolsVisibility)
	}
}

func TestLoadRuntimeConfigOverlaysMainModelsAndChannels(t *testing.T) {
	baseDir := t.TempDir()
	mainPath := filepath.Join(baseDir, "kocort.json")
	modelsPath := filepath.Join(baseDir, "models.json")
	channelsPath := filepath.Join(baseDir, "channels.json")
	if err := os.WriteFile(mainPath, []byte(`{
		"agents":{"defaults":{"timeoutSeconds":42}},
		"skills":{"install":{"nodeManager":"pnpm"}}
	}`), 0o644); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(modelsPath, []byte(`{
		"models":{"providers":{"test":{"baseUrl":"https://example.invalid","apiKey":"k","api":"openai-completions","models":[{"id":"test/model","name":"Test","reasoning":true}]}}}
	}`), 0o644); err != nil {
		t.Fatalf("write models config: %v", err)
	}
	if err := os.WriteFile(channelsPath, []byte(`{
		"channels":{"entries":{"mock":{"enabled":true,"defaultTo":"u1","agent":"main","config":{"driver":"mock"}}}}
	}`), 0o644); err != nil {
		t.Fatalf("write channels config: %v", err)
	}
	cfg, err := LoadRuntimeConfig(embeddedDefaultConfigJSON, ConfigLoadOptions{
		ConfigDir:          baseDir,
		ConfigPath:         mainPath,
		ModelsConfigPath:   modelsPath,
		ChannelsConfigPath: channelsPath,
	})
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	if cfg.Agents.Defaults == nil || cfg.Agents.Defaults.TimeoutSeconds != 42 {
		t.Fatalf("expected main config overlay, got %+v", cfg.Agents.Defaults)
	}
	if cfg.Skills.Install.NodeManager != "pnpm" {
		t.Fatalf("expected main config nodeManager overlay, got %q", cfg.Skills.Install.NodeManager)
	}
	if _, ok := cfg.Models.Providers["test"]; !ok {
		t.Fatalf("expected models overlay provider")
	}
	if _, ok := cfg.Channels.Entries["mock"]; !ok {
		t.Fatalf("expected channels overlay entry")
	}
	if cfg.Memory.Backend != "builtin" {
		t.Fatalf("expected embedded defaults preserved, got %q", cfg.Memory.Backend)
	}
}

func TestResolveDefaultStateDirUsesKocortHome(t *testing.T) {
	originalState := os.Getenv("KOCORT_STATE_DIR")
	originalConfig := os.Getenv("KOCORT_CONFIG_DIR")
	defer func() {
		if originalState == "" {
			_ = os.Unsetenv("KOCORT_STATE_DIR")
		} else {
			_ = os.Setenv("KOCORT_STATE_DIR", originalState)
		}
		if originalConfig == "" {
			_ = os.Unsetenv("KOCORT_CONFIG_DIR")
		} else {
			_ = os.Setenv("KOCORT_CONFIG_DIR", originalConfig)
		}
	}()
	_ = os.Unsetenv("KOCORT_STATE_DIR")
	_ = os.Unsetenv("KOCORT_CONFIG_DIR")
	// When no env vars, should return a non-empty directory
	dir := ResolveDefaultStateDir()
	if dir == "" {
		t.Fatalf("expected non-empty default state dir, got %q", dir)
	}
}

// TestResolveDefaultConfigDirFallsToPwdWhenNothingExists verifies that when
// neither PWD/.kocort nor ~/.kocort exist, the function returns PWD/.kocort.
func TestResolveDefaultConfigDirFallsToPwdWhenNothingExists(t *testing.T) {
	originalConfig := os.Getenv("KOCORT_CONFIG_DIR")
	defer func() {
		if originalConfig == "" {
			_ = os.Unsetenv("KOCORT_CONFIG_DIR")
		} else {
			_ = os.Setenv("KOCORT_CONFIG_DIR", originalConfig)
		}
	}()
	_ = os.Unsetenv("KOCORT_CONFIG_DIR")

	// Use a temp dir with no .kocort subdirectory and override HOME so
	// neither PWD/.kocort nor ~/.kocort can be found.
	tmpDir, _ := filepath.EvalSymlinks(t.TempDir()) // resolve /var→/private/var on macOS
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir) //nolint:errcheck
	_ = os.Chdir(tmpDir)

	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	os.Setenv("HOME", tmpDir) // no .kocort under this fake HOME either

	got := ResolveDefaultConfigDir()
	expected := filepath.Join(tmpDir, ".kocort")
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

// TestResolveDefaultConfigDirPrefersHomeKocortOverFallback verifies that when
// PWD/.kocort does NOT exist but ~/.kocort DOES exist, the function returns ~/.kocort.
func TestResolveDefaultConfigDirPrefersHomeKocortOverFallback(t *testing.T) {
	originalConfig := os.Getenv("KOCORT_CONFIG_DIR")
	defer func() {
		if originalConfig == "" {
			_ = os.Unsetenv("KOCORT_CONFIG_DIR")
		} else {
			_ = os.Setenv("KOCORT_CONFIG_DIR", originalConfig)
		}
	}()
	_ = os.Unsetenv("KOCORT_CONFIG_DIR")

	tmpDir, _ := filepath.EvalSymlinks(t.TempDir())
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir) //nolint:errcheck
	_ = os.Chdir(tmpDir)

	// Create ~/.kocort under a fake HOME
	fakeHome, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Mkdir(filepath.Join(fakeHome, ".kocort"), 0o755)
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	os.Setenv("HOME", fakeHome)

	got := ResolveDefaultConfigDir()
	expected := filepath.Join(fakeHome, ".kocort")
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

// TestResolveDefaultConfigDirPrefersPwdOverHome verifies that when BOTH
// PWD/.kocort and ~/.kocort exist, PWD/.kocort wins.
func TestResolveDefaultConfigDirPrefersPwdOverHome(t *testing.T) {
	originalConfig := os.Getenv("KOCORT_CONFIG_DIR")
	defer func() {
		if originalConfig == "" {
			_ = os.Unsetenv("KOCORT_CONFIG_DIR")
		} else {
			_ = os.Setenv("KOCORT_CONFIG_DIR", originalConfig)
		}
	}()
	_ = os.Unsetenv("KOCORT_CONFIG_DIR")

	tmpDir, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Mkdir(filepath.Join(tmpDir, ".kocort"), 0o755)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir) //nolint:errcheck
	_ = os.Chdir(tmpDir)

	fakeHome, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Mkdir(filepath.Join(fakeHome, ".kocort"), 0o755)
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	os.Setenv("HOME", fakeHome)

	got := ResolveDefaultConfigDir()
	expected := filepath.Join(tmpDir, ".kocort")
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestResolveDefaultConfigDirPrefersExplicitConfigDir(t *testing.T) {
	originalConfig := os.Getenv("KOCORT_CONFIG_DIR")
	defer func() {
		if originalConfig == "" {
			_ = os.Unsetenv("KOCORT_CONFIG_DIR")
		} else {
			_ = os.Setenv("KOCORT_CONFIG_DIR", originalConfig)
		}
	}()
	_ = os.Setenv("KOCORT_CONFIG_DIR", "/tmp/config-b")
	if got := ResolveDefaultConfigDir(); got != "/tmp/config-b" {
		t.Fatalf("expected config dir override, got %q", got)
	}
}

func TestLoadRuntimeConfigUsesConfigDirDefaults(t *testing.T) {
	baseDir := t.TempDir()
	mainPath := filepath.Join(baseDir, "kocort.json")
	if err := os.WriteFile(mainPath, []byte(`{"agents":{"defaults":{"timeoutSeconds":33}}}`), 0o644); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	cfg, err := LoadRuntimeConfig(embeddedDefaultConfigJSON, ConfigLoadOptions{ConfigDir: baseDir})
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	if cfg.Agents.Defaults == nil || cfg.Agents.Defaults.TimeoutSeconds != 33 {
		t.Fatalf("expected config-dir kocort.json to load, got %+v", cfg.Agents.Defaults)
	}
}

func TestResolveDesktopConfigDirDefaultsToHomeKocort(t *testing.T) {
	// Save and clear KOCORT_CONFIG_DIR so the function falls through to ~/.kocort
	originalConfig := os.Getenv("KOCORT_CONFIG_DIR")
	defer func() {
		if originalConfig == "" {
			_ = os.Unsetenv("KOCORT_CONFIG_DIR")
		} else {
			_ = os.Setenv("KOCORT_CONFIG_DIR", originalConfig)
		}
	}()
	_ = os.Unsetenv("KOCORT_CONFIG_DIR")

	got := ResolveDesktopConfigDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}
	expected := filepath.Join(home, ".kocort")
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestResolveDesktopConfigDirRespectsEnvOverride(t *testing.T) {
	originalConfig := os.Getenv("KOCORT_CONFIG_DIR")
	defer func() {
		if originalConfig == "" {
			_ = os.Unsetenv("KOCORT_CONFIG_DIR")
		} else {
			_ = os.Setenv("KOCORT_CONFIG_DIR", originalConfig)
		}
	}()
	_ = os.Setenv("KOCORT_CONFIG_DIR", "/tmp/desktop-override")
	if got := ResolveDesktopConfigDir(); got != "/tmp/desktop-override" {
		t.Fatalf("expected env override, got %q", got)
	}
}

func TestResolveConfigPathsSandboxDirsStayAbsolute(t *testing.T) {
	cfg := AppConfig{
		StateDir: "state",
		Agents: AgentsConfig{
			Defaults: &AgentDefaultsConfig{
				Workspace:   "workspace-main",
				AgentDir:    "agents/main/agent",
				SandboxDirs: []string{"/absolute/sandbox/dir", "/another/sandbox"},
			},
			List: []AgentConfig{
				{
					ID:          "worker",
					Workspace:   "workspace-worker",
					AgentDir:    "agents/worker/agent",
					SandboxDirs: []string{"/worker/sandbox"},
				},
			},
		},
		BrainLocal: BrainLocalConfig{ModelsDir: "models"},
		Cerebellum: CerebellumConfig{ModelsDir: "models"},
		Tools: ToolsConfig{
			BrowserDriverDir: "playwright-driver",
		},
	}
	configDir := "/home/user/.kocort"
	ResolveConfigPaths(&cfg, configDir)

	// Relative paths should be resolved to absolute
	if cfg.StateDir != filepath.Join(configDir, "state") {
		t.Fatalf("StateDir not resolved: %q", cfg.StateDir)
	}
	if cfg.Agents.Defaults.Workspace != filepath.Join(configDir, "workspace-main") {
		t.Fatalf("Defaults.Workspace not resolved: %q", cfg.Agents.Defaults.Workspace)
	}
	if cfg.Agents.Defaults.AgentDir != filepath.Join(configDir, "agents/main/agent") {
		t.Fatalf("Defaults.AgentDir not resolved: %q", cfg.Agents.Defaults.AgentDir)
	}
	if cfg.Agents.List[0].Workspace != filepath.Join(configDir, "workspace-worker") {
		t.Fatalf("List[0].Workspace not resolved: %q", cfg.Agents.List[0].Workspace)
	}
	if cfg.BrainLocal.ModelsDir != filepath.Join(configDir, "models") {
		t.Fatalf("BrainLocal.ModelsDir not resolved: %q", cfg.BrainLocal.ModelsDir)
	}
	if cfg.Cerebellum.ModelsDir != filepath.Join(configDir, "models") {
		t.Fatalf("Cerebellum.ModelsDir not resolved: %q", cfg.Cerebellum.ModelsDir)
	}
	if cfg.Tools.BrowserDriverDir != filepath.Join(configDir, "playwright-driver") {
		t.Fatalf("BrowserDriverDir not resolved: %q", cfg.Tools.BrowserDriverDir)
	}

	// SandboxDirs must stay absolute — NOT resolved
	if cfg.Agents.Defaults.SandboxDirs[0] != "/absolute/sandbox/dir" {
		t.Fatalf("Defaults.SandboxDirs[0] was modified: %q", cfg.Agents.Defaults.SandboxDirs[0])
	}
	if cfg.Agents.Defaults.SandboxDirs[1] != "/another/sandbox" {
		t.Fatalf("Defaults.SandboxDirs[1] was modified: %q", cfg.Agents.Defaults.SandboxDirs[1])
	}
	if cfg.Agents.List[0].SandboxDirs[0] != "/worker/sandbox" {
		t.Fatalf("List[0].SandboxDirs[0] was modified: %q", cfg.Agents.List[0].SandboxDirs[0])
	}
}

// TestUnresolveConfigPathsRoundTrip verifies that Resolve → Unresolve produces
// the original relative paths, while SandboxDirs (always absolute) stay unchanged.
func TestUnresolveConfigPathsRoundTrip(t *testing.T) {
	configDir := "/home/user/.kocort"

	// Original config with relative paths (as stored on disk).
	original := AppConfig{
		StateDir: "state",
		Agents: AgentsConfig{
			Defaults: &AgentDefaultsConfig{
				Workspace:   "workspace-main",
				AgentDir:    "agents/main/agent",
				SandboxDirs: []string{"/absolute/sandbox/dir", "/another/sandbox"},
			},
			List: []AgentConfig{
				{
					ID:          "worker",
					Workspace:   "workspace-worker",
					AgentDir:    "agents/worker/agent",
					SandboxDirs: []string{"/worker/sandbox"},
				},
			},
		},
		BrainLocal: BrainLocalConfig{ModelsDir: "models"},
		Cerebellum: CerebellumConfig{ModelsDir: "cerebellum-models"},
		Tools:      ToolsConfig{BrowserDriverDir: "playwright-driver"},
		Data:       DataConfig{Entries: map[string]DataSourceConfig{"kb": {Path: "data/kb"}}},
		Logging:    LoggingConfig{File: "logs/kocort.log"},
	}

	// Step 1: Resolve (relative → absolute)
	resolved := original
	ResolveConfigPaths(&resolved, configDir)

	if resolved.StateDir != filepath.Join(configDir, "state") {
		t.Fatalf("resolve: StateDir %q", resolved.StateDir)
	}
	if resolved.Agents.Defaults.Workspace != filepath.Join(configDir, "workspace-main") {
		t.Fatalf("resolve: Defaults.Workspace %q", resolved.Agents.Defaults.Workspace)
	}
	if resolved.Data.Entries["kb"].Path != filepath.Join(configDir, "data/kb") {
		t.Fatalf("resolve: Data.Entries[kb].Path %q", resolved.Data.Entries["kb"].Path)
	}
	if resolved.Logging.File != filepath.Join(configDir, "logs/kocort.log") {
		t.Fatalf("resolve: Logging.File %q", resolved.Logging.File)
	}

	// Step 2: Unresolve (absolute → relative)
	unresolved := resolved
	UnresolveConfigPaths(&unresolved, configDir)

	// All relative paths should be restored to their original values.
	assertEqual := func(field, got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("unresolve round-trip %s: got %q, want %q", field, got, want)
		}
	}
	assertEqual("StateDir", unresolved.StateDir, "state")
	assertEqual("Defaults.Workspace", unresolved.Agents.Defaults.Workspace, "workspace-main")
	assertEqual("Defaults.AgentDir", unresolved.Agents.Defaults.AgentDir, "agents/main/agent")
	assertEqual("List[0].Workspace", unresolved.Agents.List[0].Workspace, "workspace-worker")
	assertEqual("List[0].AgentDir", unresolved.Agents.List[0].AgentDir, "agents/worker/agent")
	assertEqual("BrainLocal.ModelsDir", unresolved.BrainLocal.ModelsDir, "models")
	assertEqual("Cerebellum.ModelsDir", unresolved.Cerebellum.ModelsDir, "cerebellum-models")
	assertEqual("BrowserDriverDir", unresolved.Tools.BrowserDriverDir, "playwright-driver")
	assertEqual("Data.Entries[kb].Path", unresolved.Data.Entries["kb"].Path, "data/kb")
	assertEqual("Logging.File", unresolved.Logging.File, "logs/kocort.log")

	// SandboxDirs must remain absolute after round-trip.
	if unresolved.Agents.Defaults.SandboxDirs[0] != "/absolute/sandbox/dir" {
		t.Fatalf("SandboxDirs[0] changed after round-trip: %q", unresolved.Agents.Defaults.SandboxDirs[0])
	}
	if unresolved.Agents.List[0].SandboxDirs[0] != "/worker/sandbox" {
		t.Fatalf("List[0].SandboxDirs[0] changed after round-trip: %q", unresolved.Agents.List[0].SandboxDirs[0])
	}
}

// TestUnresolveConfigPathsKeepsOutsidePathsAbsolute verifies that absolute paths
// outside the configDir are NOT converted to relative (because they would start
// with ".." which is forbidden by the convention).
func TestUnresolveConfigPathsKeepsOutsidePathsAbsolute(t *testing.T) {
	configDir := "/home/user/.kocort"
	cfg := AppConfig{
		StateDir: "/opt/kocort/state", // outside configDir
		Agents: AgentsConfig{
			Defaults: &AgentDefaultsConfig{
				Workspace: "/var/workspace",                        // outside configDir
				AgentDir:  filepath.Join(configDir, "agents/main"), // inside configDir
			},
		},
		BrainLocal: BrainLocalConfig{ModelsDir: "/mnt/models"}, // outside configDir
	}

	UnresolveConfigPaths(&cfg, configDir)

	// Paths outside configDir stay absolute.
	if cfg.StateDir != "/opt/kocort/state" {
		t.Fatalf("outside StateDir should stay absolute, got %q", cfg.StateDir)
	}
	if cfg.Agents.Defaults.Workspace != "/var/workspace" {
		t.Fatalf("outside Workspace should stay absolute, got %q", cfg.Agents.Defaults.Workspace)
	}
	if cfg.BrainLocal.ModelsDir != "/mnt/models" {
		t.Fatalf("outside ModelsDir should stay absolute, got %q", cfg.BrainLocal.ModelsDir)
	}

	// Path inside configDir should be unresolved to relative.
	if cfg.Agents.Defaults.AgentDir != "agents/main" {
		t.Fatalf("inside AgentDir should be relative, got %q", cfg.Agents.Defaults.AgentDir)
	}
}
