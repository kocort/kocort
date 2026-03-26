// Canonical implementation — migrated from runtime/skills_install.go.
package skill

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

// SkillInstallRequest describes a skill install operation.
type SkillInstallRequest struct {
	WorkspaceDir string
	SkillName    string
	InstallID    string
	Config       *config.AppConfig
	Timeout      time.Duration
}

func InstallSkill(ctx context.Context, req SkillInstallRequest) (core.SkillInstallResult, error) {
	opts := &WorkspaceSkillBuildOptions{Config: req.Config}
	entries, err := LoadWorkspaceSkillEntries(req.WorkspaceDir, opts)
	if err != nil {
		return core.SkillInstallResult{}, err
	}
	var selected *core.SkillEntry
	for i := range entries {
		if strings.EqualFold(entries[i].Name, req.SkillName) {
			selected = &entries[i]
			break
		}
	}
	if selected == nil || selected.Metadata == nil {
		return core.SkillInstallResult{}, fmt.Errorf("skill %q not found", req.SkillName)
	}
	prefs := ResolveSkillsInstallPreferences(req.Config)
	spec := resolveInstallSpec(*selected, strings.TrimSpace(req.InstallID), prefs)
	if spec == nil {
		return core.SkillInstallResult{}, fmt.Errorf("install option %q not found for skill %q", req.InstallID, req.SkillName)
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	installCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runSkillInstall(installCtx, *spec, prefs)
}

func ResolveInstallSpec(entry core.SkillEntry, installID string, prefs core.SkillInstallPreferences) *core.SkillInstallSpec {
	specs := entry.Metadata.Install
	if len(specs) == 0 {
		return nil
	}
	if installID != "" {
		for i := range specs {
			candidate := &specs[i]
			if strings.EqualFold(strings.TrimSpace(candidate.ID), installID) {
				return candidate
			}
		}
		return nil
	}
	selected := selectPreferredInstallSpec(specs, prefs)
	if selected == nil {
		return &specs[0]
	}
	return &specs[selected.index]
}

// resolveInstallSpec is the internal alias used by InstallSkill.
var resolveInstallSpec = ResolveInstallSpec

type indexedInstallSpec struct {
	spec  core.SkillInstallSpec
	index int
}

func selectPreferredInstallSpec(install []core.SkillInstallSpec, prefs core.SkillInstallPreferences) *indexedInstallSpec {
	if len(install) == 0 {
		return nil
	}
	indexed := make([]indexedInstallSpec, 0, len(install))
	for i, spec := range install {
		indexed = append(indexed, indexedInstallSpec{spec: spec, index: i})
	}
	findKind := func(kind string) *indexedInstallSpec {
		for i := range indexed {
			if strings.EqualFold(strings.TrimSpace(indexed[i].spec.Kind), kind) {
				return &indexed[i]
			}
		}
		return nil
	}
	brewSpec := findKind("brew")
	nodeSpec := findKind("node")
	goSpec := findKind("go")
	uvSpec := findKind("uv")
	downloadSpec := findKind("download")
	brewAvailable := hasBinary("brew", nil)

	pickers := []func() *indexedInstallSpec{
		func() *indexedInstallSpec {
			if prefs.PreferBrew && brewAvailable {
				return brewSpec
			}
			return nil
		},
		func() *indexedInstallSpec { return uvSpec },
		func() *indexedInstallSpec { return nodeSpec },
		func() *indexedInstallSpec {
			if brewAvailable {
				return brewSpec
			}
			return nil
		},
		func() *indexedInstallSpec { return goSpec },
		func() *indexedInstallSpec { return downloadSpec },
		func() *indexedInstallSpec { return brewSpec },
		func() *indexedInstallSpec { return &indexed[0] },
	}
	for _, pick := range pickers {
		if selected := pick(); selected != nil {
			return selected
		}
	}
	return nil
}

func runSkillInstall(ctx context.Context, spec core.SkillInstallSpec, prefs core.SkillInstallPreferences) (core.SkillInstallResult, error) {
	argv, err := buildInstallCommand(spec, prefs)
	if err != nil {
		return core.SkillInstallResult{}, err
	}
	if len(argv) == 0 {
		return core.SkillInstallResult{}, fmt.Errorf("unsupported install kind %q", spec.Kind)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	output, err := cmd.CombinedOutput()
	result := core.SkillInstallResult{
		Stdout: strings.TrimSpace(string(output)),
		Stderr: "",
		Code:   0,
	}
	if err != nil {
		result.OK = false
		result.Message = strings.TrimSpace(err.Error())
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.Code = exitErr.ExitCode()
		}
		return result, nil
	}
	result.OK = true
	result.Message = "Installed"
	return result, nil
}

func buildInstallCommand(spec core.SkillInstallSpec, prefs core.SkillInstallPreferences) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(spec.Kind)) {
	case "brew":
		if strings.TrimSpace(spec.Formula) == "" {
			return nil, fmt.Errorf("missing brew formula")
		}
		return []string{"brew", "install", spec.Formula}, nil
	case "node":
		if strings.TrimSpace(spec.Package) == "" {
			return nil, fmt.Errorf("missing node package")
		}
		manager := strings.TrimSpace(prefs.NodeManager)
		if manager == "" {
			manager = "npm"
		}
		switch manager {
		case "bun":
			return []string{"bun", "install", "-g", spec.Package}, nil
		case "pnpm":
			return []string{"pnpm", "add", "-g", spec.Package}, nil
		case "yarn":
			return []string{"yarn", "global", "add", spec.Package}, nil
		default:
			return []string{"npm", "install", "-g", "--ignore-scripts", spec.Package}, nil
		}
	case "go":
		if strings.TrimSpace(spec.Module) == "" {
			return nil, fmt.Errorf("missing go module")
		}
		return []string{"go", "install", spec.Module}, nil
	case "uv":
		if strings.TrimSpace(spec.Package) == "" {
			return nil, fmt.Errorf("missing uv package")
		}
		return []string{"uv", "tool", "install", spec.Package}, nil
	case "download":
		if strings.TrimSpace(spec.URL) == "" {
			return nil, fmt.Errorf("missing download url")
		}
		return []string{"sh", "-lc", "curl -fsSL " + shellEscape(spec.URL) + " >/dev/null"}, nil
	default:
		return nil, fmt.Errorf("unsupported installer %q", spec.Kind)
	}
}

func shellEscape(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
