// Canonical implementation — migrated from runtime/skills_status.go.
package skill

import (
	"os"
	"os/exec"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/utils"
)

func BuildWorkspaceSkillStatus(workspaceDir string, opts *WorkspaceSkillBuildOptions, eligibility *core.SkillEligibilityContext) (core.SkillStatusReport, error) {
	version := getSkillsSnapshotVersion(workspaceDir)
	if cached, ok := getCachedSkillsStatus(workspaceDir, version, opts, eligibility); ok {
		return cached, nil
	}
	entries := opts.GetEntries()
	if entries == nil {
		var err error
		loadOpts := &WorkspaceSkillBuildOptions{}
		if opts != nil {
			*loadOpts = *opts
		}
		loadOpts.IncludeDisabled = true
		entries, err = LoadWorkspaceSkillEntries(workspaceDir, loadOpts)
		if err != nil {
			return core.SkillStatusReport{}, err
		}
	}
	report := core.SkillStatusReport{
		WorkspaceDir: workspaceDir,
		Version:      version,
		Skills:       make([]core.SkillStatusEntry, 0, len(entries)),
	}
	if report.Version <= 0 {
		report.Version = 1
	}
	prefs := ResolveSkillsInstallPreferences(OptsConfig(opts))
	for _, entry := range entries {
		report.Skills = append(report.Skills, buildSkillStatusEntry(entry, prefs, eligibility, OptsConfig(opts)))
	}
	putCachedSkillsStatus(workspaceDir, report.Version, opts, eligibility, report)
	return report, nil
}

func buildSkillStatusEntry(entry core.SkillEntry, prefs core.SkillInstallPreferences, eligibility *core.SkillEligibilityContext, cfg *config.AppConfig) core.SkillStatusEntry {
	status := core.SkillStatusEntry{
		Name:        entry.Name,
		Description: entry.Description,
		FilePath:    entry.FilePath,
		Eligible:    true,
	}
	if entry.Metadata != nil && entry.Metadata.Source == "bundled" {
		allowed := map[string]struct{}{}
		if cfg != nil {
			for _, name := range cfg.Skills.AllowBundled {
				if trimmed := strings.ToLower(strings.TrimSpace(name)); trimmed != "" {
					allowed[trimmed] = struct{}{}
				}
			}
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToLower(strings.TrimSpace(entry.Name))]; !ok {
				status.BlockedByAllowlist = true
				status.Eligible = false
			}
		}
	}
	if skillCfg := resolveSkillConfigForEntry(cfg, entry); skillCfg != nil && skillCfg.Enabled != nil && !*skillCfg.Enabled {
		status.Disabled = true
		status.Eligible = false
	}
	if entry.Metadata != nil {
		status.Source = entry.Metadata.Source
		status.BaseDir = entry.Metadata.BaseDir
		status.SkillKey = utils.NonEmpty(entry.Metadata.SkillKey, entry.Name)
		status.PrimaryEnv = entry.Metadata.PrimaryEnv
		status.Emoji = entry.Metadata.Emoji
		status.Homepage = entry.Metadata.Homepage
		status.Always = entry.Metadata.Always
		status.Install = normalizeInstallOptions(entry, prefs)
	}
	eval := evaluateSkillRequirements(entry, cfg, eligibility)
	status.MissingBins = append(status.MissingBins, eval.MissingBins...)
	status.MissingEnv = append(status.MissingEnv, eval.MissingEnv...)
	status.MissingConfig = append(status.MissingConfig, eval.MissingConfig...)
	status.ConfigChecks = append(status.ConfigChecks, eval.ConfigChecks...)
	if !eval.Eligible {
		status.Eligible = false
	}
	return status
}

func ResolveSkillsInstallPreferences(cfg *config.AppConfig) core.SkillInstallPreferences {
	prefs := core.SkillInstallPreferences{
		PreferBrew:  true,
		NodeManager: "npm",
	}
	if cfg == nil {
		return prefs
	}
	if cfg.Skills.Install.NodeManager != "" {
		prefs.NodeManager = strings.TrimSpace(cfg.Skills.Install.NodeManager)
	}
	if cfg.Skills.Install.PreferBrew != nil {
		prefs.PreferBrew = *cfg.Skills.Install.PreferBrew
	}
	return prefs
}

func normalizeInstallOptions(entry core.SkillEntry, prefs core.SkillInstallPreferences) []core.SkillInstallOption {
	if entry.Metadata == nil || len(entry.Metadata.Install) == 0 {
		return nil
	}
	var out []core.SkillInstallOption
	for i, spec := range entry.Metadata.Install {
		id := strings.TrimSpace(spec.ID)
		if id == "" {
			id = spec.Kind
			if id == "" {
				id = "install"
			}
			if i > 0 {
				id = id + "-" + string(rune('0'+i))
			}
		}
		label := strings.TrimSpace(spec.Label)
		if label == "" {
			switch spec.Kind {
			case "brew":
				label = "Install " + utils.NonEmpty(spec.Formula, entry.Name) + " (brew)"
			case "node":
				label = "Install " + utils.NonEmpty(spec.Package, entry.Name) + " (" + prefs.NodeManager + ")"
			case "go":
				label = "Install " + utils.NonEmpty(spec.Module, entry.Name) + " (go)"
			case "uv":
				label = "Install " + utils.NonEmpty(spec.Package, entry.Name) + " (uv)"
			case "download":
				label = "Download " + utils.NonEmpty(spec.URL, entry.Name)
			default:
				label = "Install " + entry.Name
			}
		}
		out = append(out, core.SkillInstallOption{
			ID:    id,
			Kind:  spec.Kind,
			Label: label,
			Bins:  append([]string{}, spec.Bins...),
		})
	}
	return out
}

func hasBinary(bin string, eligibility *core.SkillEligibilityContext) bool {
	bin = strings.TrimSpace(bin)
	if bin == "" {
		return false
	}
	if eligibility != nil && eligibility.Remote != nil && eligibility.Remote.HasBin != nil && eligibility.Remote.HasBin(bin) {
		return true
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

func hasAnyBinary(bins []string, eligibility *core.SkillEligibilityContext) bool {
	if eligibility != nil && eligibility.Remote != nil && eligibility.Remote.HasAnyBin != nil && eligibility.Remote.HasAnyBin(bins) {
		return true
	}
	for _, bin := range bins {
		if hasBinary(bin, nil) {
			return true
		}
	}
	return false
}

func lookupEnv(cfg *config.AppConfig, entry core.SkillEntry, key string) string {
	if skillCfg := resolveSkillConfigForEntry(cfg, entry); skillCfg != nil {
		if value := strings.TrimSpace(skillCfg.Env[strings.TrimSpace(key)]); value != "" {
			return value
		}
		primary := ""
		if entry.Metadata != nil {
			primary = strings.TrimSpace(entry.Metadata.PrimaryEnv)
		}
		if primary != "" && strings.EqualFold(strings.TrimSpace(key), primary) {
			if value := strings.TrimSpace(skillCfg.APIKey); value != "" {
				return value
			}
		}
	}
	return os.Getenv(strings.TrimSpace(key))
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
