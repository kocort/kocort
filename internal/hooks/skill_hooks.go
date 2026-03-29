package hooks

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillHookEntry represents a hook handler discovered inside a skill directory.
type SkillHookEntry struct {
	SkillName  string         // Owning skill name.
	HookName   string         // Hook directory name (used as config key).
	BaseDir    string         // Hook directory (contains HOOK.md + handler script).
	ScriptPath string         // Absolute path to the handler script.
	Events     []string       // Event keys this hook handles, declared in HOOK.md frontmatter.
	Metadata   HookMDMetadata // Additional metadata from HOOK.md frontmatter.
}

// HookMDMetadata holds optional metadata parsed from HOOK.md frontmatter.
type HookMDMetadata struct {
	OS       []string        `yaml:"os,omitempty"`       // OS filter: ["linux", "darwin", "windows"]
	Requires *HookMDRequires `yaml:"requires,omitempty"` // System requirements.
}

// HookMDRequires specifies system requirements for a hook.
type HookMDRequires struct {
	Bins    []string `yaml:"bins,omitempty"`    // All of these binaries must exist on PATH.
	AnyBins []string `yaml:"anyBins,omitempty"` // At least one of these binaries must exist.
	Env     []string `yaml:"env,omitempty"`     // All of these environment variables must be set.
}

// DiscoverSkillHooks scans the given skill directories for hook definitions.
// It looks for a hooks/ subdirectory where each child directory represents a
// single hook. Each hook directory must contain:
//
//   - HOOK.md — YAML frontmatter with an "events" field listing the event keys
//     this hook subscribes to (e.g. ["agent:bootstrap", "tool:post_execute"]).
//   - A handler script — the first match from: handler.sh, handler.bat,
//     handler.ps1, index.sh, index.bat, index.ps1.
//
// hookEntries maps hook names to per-hook config overrides. Hooks whose entry
// has Enabled=false are skipped. OS and requires constraints from the HOOK.md
// frontmatter are also checked.
//
// This is fully data-driven: no script names or event keys are hardcoded.
func DiscoverSkillHooks(skillDirs map[string]string, hookEntries map[string]HookEntryConfig) []SkillHookEntry {
	var entries []SkillHookEntry
	for skillName, baseDir := range skillDirs {
		hooksDir := filepath.Join(baseDir, "hooks")
		info, err := os.Stat(hooksDir)
		if err != nil || !info.IsDir() {
			continue
		}
		dirEntries, err := os.ReadDir(hooksDir)
		if err != nil {
			continue
		}
		for _, de := range dirEntries {
			if !de.IsDir() {
				continue
			}
			hookName := de.Name()
			hookDir := filepath.Join(hooksDir, hookName)

			// Check per-hook config: skip if explicitly disabled.
			if entry, ok := hookEntries[hookName]; ok && !entry.HookEntryEnabled() {
				slog.Debug("[hooks] hook disabled by config",
					"skill", skillName, "hook", hookName)
				continue
			}

			// Parse HOOK.md frontmatter.
			fm := parseHookMDFull(filepath.Join(hookDir, "HOOK.md"))
			if len(fm.Events) == 0 {
				continue
			}

			// Check OS constraint.
			if !isOSEligible(fm.Metadata.OS) {
				slog.Debug("[hooks] hook skipped: OS not eligible",
					"skill", skillName, "hook", hookName,
					"required", fm.Metadata.OS, "current", currentOS())
				continue
			}

			// Check requires constraint.
			if reason := checkRequires(fm.Metadata.Requires); reason != "" {
				slog.Debug("[hooks] hook skipped: requirement not met",
					"skill", skillName, "hook", hookName, "reason", reason)
				continue
			}

			// Discover handler script.
			scriptPath := discoverHandlerScript(hookDir)
			if scriptPath == "" {
				slog.Warn("[hooks] HOOK.md found but no handler script",
					"skill", skillName, "hookDir", hookDir)
				continue
			}

			entries = append(entries, SkillHookEntry{
				SkillName:  skillName,
				HookName:   hookName,
				BaseDir:    hookDir,
				ScriptPath: scriptPath,
				Events:     fm.Events,
				Metadata:   fm.Metadata,
			})
		}
	}
	return entries
}

// handlerCandidates lists handler script filenames in discovery order.
var handlerCandidates = []string{
	"handler.sh", "handler.bat", "handler.ps1",
	"index.sh", "index.bat", "index.ps1",
}

// discoverHandlerScript returns the first matching handler script in hookDir.
func discoverHandlerScript(hookDir string) string {
	for _, name := range handlerCandidates {
		p := filepath.Join(hookDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// hookMDFrontmatter is the full parsed HOOK.md frontmatter.
type hookMDFrontmatter struct {
	Events   []string       `yaml:"events"`
	Metadata HookMDMetadata `yaml:",inline"`
}

// parseHookMDFull reads a HOOK.md file and extracts the full frontmatter
// including events, OS constraints, and requirements.
func parseHookMDFull(path string) hookMDFrontmatter {
	raw, err := extractFrontmatter(path)
	if err != nil || raw == "" {
		return hookMDFrontmatter{}
	}
	var fm hookMDFrontmatter
	if err := yaml.Unmarshal([]byte(raw), &fm); err != nil {
		slog.Warn("[hooks] failed to parse HOOK.md frontmatter",
			"path", path, "error", err)
		return hookMDFrontmatter{}
	}
	return fm
}

// parseHookMDEvents reads a HOOK.md file and extracts the "events" list from
// its YAML frontmatter. This is a convenience wrapper around parseHookMDFull.
func parseHookMDEvents(path string) []string {
	return parseHookMDFull(path).Events
}

// extractFrontmatter reads the YAML frontmatter block (between "---" lines)
// from a markdown file and returns it as a string.
func extractFrontmatter(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// Expect first line to be "---".
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", nil
	}

	var sb strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			return sb.String(), nil
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return "", nil // No closing "---" found.
}

// RegisterSkillHooks registers all discovered skill hooks into the registry.
// Each hook script is wrapped in a Handler that executes the script with
// environment variables carrying the event context.
// hookEntries provides per-hook env overrides from config.
func RegisterSkillHooks(reg *Registry, entries []SkillHookEntry, hookEntries map[string]HookEntryConfig) int {
	count := 0
	for _, entry := range entries {
		envOverrides := hookEntries[entry.HookName].Env
		for _, eventKey := range entry.Events {
			handler := makeScriptHandler(entry.SkillName, entry.BaseDir, entry.ScriptPath, envOverrides)
			reg.Register(eventKey, handler)
			slog.Info("[hooks] registered skill hook",
				"skill", entry.SkillName,
				"hook", entry.HookName,
				"event", eventKey,
				"script", entry.ScriptPath)
			count++
		}
	}
	return count
}

// makeScriptHandler returns a Handler that executes the given script.
// The script receives event context via environment variables:
//
//	HOOK_EVENT_TYPE, HOOK_EVENT_ACTION, HOOK_SESSION_KEY,
//	HOOK_TOOL_NAME (for tool events), HOOK_ERROR (for error detection).
func makeScriptHandler(skillName, baseDir, scriptPath string, envOverrides map[string]string) Handler {
	return func(ctx context.Context, event *Event) error {
		shell, args := resolveShellCommand(scriptPath)

		cmd := exec.CommandContext(ctx, shell, args...)
		cmd.Dir = baseDir
		cmd.Env = append(os.Environ(),
			"HOOK_EVENT_TYPE="+string(event.Type),
			"HOOK_EVENT_ACTION="+event.Action,
			"HOOK_SESSION_KEY="+event.SessionKey,
			"HOOK_SKILL_NAME="+skillName,
		)
		// Apply per-hook env overrides from config.
		for k, v := range envOverrides {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		// Pass tool-specific context.
		if toolName, ok := event.Context["toolName"].(string); ok {
			cmd.Env = append(cmd.Env, "HOOK_TOOL_NAME="+toolName)
		}
		if exitCode, ok := event.Context["exitCode"].(int); ok {
			cmd.Env = append(cmd.Env, fmt.Sprintf("HOOK_EXIT_CODE=%d", exitCode))
		}
		if errMsg, ok := event.Context["error"].(string); ok {
			cmd.Env = append(cmd.Env, "HOOK_ERROR="+errMsg)
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			slog.Warn("[hooks] skill hook script failed",
				"skill", skillName,
				"script", scriptPath,
				"error", err,
				"output", strings.TrimSpace(string(output)))
			return fmt.Errorf("hook script %q failed: %w", scriptPath, err)
		}

		// Scripts can emit messages by writing to stdout (one per line).
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			for _, line := range strings.Split(trimmed, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					event.Messages = append(event.Messages, line)
				}
			}
		}
		return nil
	}
}

// HookEntryConfig mirrors config.HookEntryConfig to avoid import cycles.
// The caller (runtime) maps from config.HookEntryConfig to this type.
type HookEntryConfig struct {
	Enabled *bool
	Env     map[string]string
}

// HookEntryEnabled reports whether this hook entry is enabled.
func (c HookEntryConfig) HookEntryEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// isOSEligible checks whether the current OS matches the HOOK.md os constraint.
// An empty list means all OSes are eligible.
func isOSEligible(osList []string) bool {
	if len(osList) == 0 {
		return true
	}
	cur := currentOS()
	for _, allowed := range osList {
		if strings.EqualFold(allowed, cur) {
			return true
		}
	}
	return false
}

// currentOS returns the normalised OS name.
func currentOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	case "windows":
		return "windows"
	default:
		return runtime.GOOS // "linux", "freebsd", etc.
	}
}

// checkRequires validates system requirements from HOOK.md frontmatter.
// Returns an empty string if all requirements are met, or a human-readable
// reason string if any check fails.
func checkRequires(req *HookMDRequires) string {
	if req == nil {
		return ""
	}
	// All bins required.
	for _, bin := range req.Bins {
		if _, err := exec.LookPath(bin); err != nil {
			return "missing required binary: " + bin
		}
	}
	// At least one of anyBins required.
	if len(req.AnyBins) > 0 {
		found := false
		for _, bin := range req.AnyBins {
			if _, err := exec.LookPath(bin); err == nil {
				found = true
				break
			}
		}
		if !found {
			return "none of the required binaries found: " + strings.Join(req.AnyBins, ", ")
		}
	}
	// All env vars required.
	for _, envVar := range req.Env {
		if os.Getenv(envVar) == "" {
			return "missing required environment variable: " + envVar
		}
	}
	return ""
}

func resolveShellCommand(scriptPath string) (string, []string) {
	ext := strings.ToLower(filepath.Ext(scriptPath))
	if runtime.GOOS == "windows" {
		switch ext {
		case ".ps1":
			return "powershell", []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
		case ".bat", ".cmd":
			return "cmd", []string{"/c", scriptPath}
		default:
			// Try bash (Git Bash / WSL) for .sh
			if bashPath, err := exec.LookPath("bash"); err == nil {
				return bashPath, []string{scriptPath}
			}
			return "powershell", []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
		}
	}
	switch ext {
	case ".sh", ".bash":
		return "bash", []string{scriptPath}
	default:
		return "sh", []string{scriptPath}
	}
}
