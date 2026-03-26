// Canonical implementation — migrated from runtime/skills.go.
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/utils"
)

const (
	DefaultSkillFilename             = "SKILL.md"
	DefaultMaxSkillsLoadedPerSource  = 200
	DefaultMaxSkillsInPrompt         = 150
	DefaultMaxSkillsPromptChars      = 30_000
	DefaultMaxSkillFileBytes         = 256 * 1024
	SkillCommandDescriptionMaxLength = 100
	skillCommandMaxLength            = 32
)

type WorkspaceSkillBuildOptions struct {
	Config           *config.AppConfig
	ManagedSkillsDir string
	BundledSkillsDir string
	Entries          []core.SkillEntry
	SkillFilter      []string
	IncludeDisabled  bool
}

func LoadWorkspaceSkillEntries(workspaceDir string, opts *WorkspaceSkillBuildOptions) ([]core.SkillEntry, error) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return nil, nil
	}
	limits := resolveSkillsLimits(opts)
	normalizedFilter := normalizeSkillFilter(optsSkillFilter(opts))
	sources := resolveSkillSources(workspaceDir, opts)

	merged := map[string]core.SkillEntry{}
	for _, source := range sources {
		entries, err := loadSkillEntriesFromRoot(source, normalizedFilter, limits, opts)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			merged[strings.ToLower(entry.Name)] = entry
		}
	}
	out := make([]core.SkillEntry, 0, len(merged))
	for _, entry := range merged {
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func BuildWorkspaceSkillSnapshot(workspaceDir string, skillFilter []string, cfg ...*config.AppConfig) (*core.SkillSnapshot, error) {
	var appConfig *config.AppConfig
	if len(cfg) > 0 {
		appConfig = cfg[0]
	}
	ensureSkillsWatcher(workspaceDir, appConfig)
	version := getSkillsSnapshotVersion(workspaceDir)
	if cached := getCachedSkillsSnapshot(workspaceDir, version, skillFilter, appConfig); cached != nil {
		return cached, nil
	}
	opts := &WorkspaceSkillBuildOptions{Config: appConfig, SkillFilter: skillFilter}
	entries, err := LoadWorkspaceSkillEntries(workspaceDir, opts)
	if err != nil {
		return nil, err
	}
	promptEntries := make([]core.SkillEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Invocation.DisableModelInvocation {
			continue
		}
		promptEntries = append(promptEntries, entry)
	}
	prompt, truncated := buildWorkspaceSkillsPromptWithLimits(promptEntries, resolveSkillsLimits(opts))
	filterCopy := append([]string{}, normalizeSkillFilter(skillFilter)...)
	commands := BuildWorkspaceSkillCommandSpecs(entries, nil)
	resolvedNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		resolvedNames = append(resolvedNames, entry.Name)
	}
	if version <= 0 {
		version = 1
		if truncated {
			version = 2
		}
	}
	snapshot := &core.SkillSnapshot{
		Prompt:       prompt,
		Skills:       entries,
		Commands:     commands,
		SkillFilter:  filterCopy,
		Version:      version,
		ResolvedName: resolvedNames,
	}
	putCachedSkillsSnapshot(workspaceDir, version, skillFilter, appConfig, snapshot)
	return snapshot, nil
}

func BuildWorkspaceSkillsPrompt(entries []core.SkillEntry) string {
	prompt, _ := buildWorkspaceSkillsPromptWithLimits(entries, resolveSkillsLimits(nil)) // best-effort; empty prompt is acceptable
	return prompt
}

func BuildWorkspaceSkillCommandSpecs(entries []core.SkillEntry, reservedNames []string) []core.SkillCommandSpec {
	used := map[string]struct{}{}
	for _, reserved := range reservedNames {
		normalized := strings.ToLower(strings.TrimSpace(reserved))
		if normalized != "" {
			used[normalized] = struct{}{}
		}
	}
	specs := make([]core.SkillCommandSpec, 0, len(entries))
	for _, entry := range entries {
		if !entry.Invocation.UserInvocable {
			continue
		}
		name := sanitizeSkillCommandName(entry.Name)
		name = resolveUniqueSkillCommandName(name, used)
		description := strings.TrimSpace(entry.Description)
		if description == "" {
			description = entry.Name
		}
		dispatch := resolveSkillCommandDispatch(entry.Frontmatter)
		specs = append(specs, core.SkillCommandSpec{
			Name:        name,
			SkillName:   entry.Name,
			Description: truncateSkillDescription(description, SkillCommandDescriptionMaxLength),
			Dispatch:    dispatch,
		})
	}
	return specs
}

func normalizeSkillFilter(skillFilter []string) []string {
	if skillFilter == nil {
		return nil
	}
	set := map[string]struct{}{}
	var out []string
	for _, item := range skillFilter {
		normalized := strings.TrimSpace(item)
		if normalized == "" {
			continue
		}
		if _, ok := set[normalized]; ok {
			continue
		}
		set[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func normalizeSkillFilterForComparison(skillFilter []string) []string {
	if skillFilter == nil {
		return nil
	}
	normalized := normalizeSkillFilter(skillFilter)
	set := map[string]struct{}{}
	out := make([]string, 0, len(normalized))
	for _, item := range normalized {
		if _, ok := set[item]; ok {
			continue
		}
		set[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func MatchesSkillFilter(cached []string, next []string) bool {
	cachedNormalized := normalizeSkillFilterForComparison(cached)
	nextNormalized := normalizeSkillFilterForComparison(next)
	if cachedNormalized == nil || nextNormalized == nil {
		return cachedNormalized == nil && nextNormalized == nil
	}
	if len(cachedNormalized) != len(nextNormalized) {
		return false
	}
	for i := range cachedNormalized {
		if cachedNormalized[i] != nextNormalized[i] {
			return false
		}
	}
	return true
}

func filterWorkspaceSkillEntries(entries []core.SkillEntry, skillFilter []string) []core.SkillEntry {
	normalized := normalizeSkillFilter(skillFilter)
	if len(normalized) == 0 {
		return append([]core.SkillEntry{}, entries...)
	}
	allowed := map[string]struct{}{}
	for _, item := range normalized {
		allowed[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	var filtered []core.SkillEntry
	for _, entry := range entries {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(entry.Name))]; ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

type skillsLimits struct {
	MaxCandidatesPerRoot     int
	MaxSkillsLoadedPerSource int
	MaxSkillsInPrompt        int
	MaxSkillsPromptChars     int
	MaxSkillFileBytes        int
}

type skillSource struct {
	name      string
	dir       string
	allowlist map[string]struct{}
}

func resolveSkillsLimits(opts *WorkspaceSkillBuildOptions) skillsLimits {
	limits := skillsLimits{
		MaxCandidatesPerRoot:     300,
		MaxSkillsLoadedPerSource: DefaultMaxSkillsLoadedPerSource,
		MaxSkillsInPrompt:        DefaultMaxSkillsInPrompt,
		MaxSkillsPromptChars:     DefaultMaxSkillsPromptChars,
		MaxSkillFileBytes:        DefaultMaxSkillFileBytes,
	}
	if opts == nil || opts.Config == nil {
		return limits
	}
	cfg := opts.Config.Skills.Limits
	if cfg.MaxCandidatesPerRoot > 0 {
		limits.MaxCandidatesPerRoot = cfg.MaxCandidatesPerRoot
	}
	if cfg.MaxSkillsLoadedPerSource > 0 {
		limits.MaxSkillsLoadedPerSource = cfg.MaxSkillsLoadedPerSource
	}
	if cfg.MaxSkillsInPrompt > 0 {
		limits.MaxSkillsInPrompt = cfg.MaxSkillsInPrompt
	}
	if cfg.MaxSkillsPromptChars > 0 {
		limits.MaxSkillsPromptChars = cfg.MaxSkillsPromptChars
	}
	if cfg.MaxSkillFileBytes > 0 {
		limits.MaxSkillFileBytes = cfg.MaxSkillFileBytes
	}
	return limits
}

func optsSkillFilter(opts *WorkspaceSkillBuildOptions) []string {
	if opts == nil {
		return nil
	}
	return opts.SkillFilter
}

func resolveSkillSources(workspaceDir string, opts *WorkspaceSkillBuildOptions) []skillSource {
	var sources []skillSource
	allowBundled := map[string]struct{}{}
	if opts != nil && opts.Config != nil {
		for _, rawName := range opts.Config.Skills.AllowBundled {
			if trimmed := strings.ToLower(strings.TrimSpace(rawName)); trimmed != "" {
				allowBundled[trimmed] = struct{}{}
			}
		}
	}
	if opts != nil && opts.Config != nil {
		for _, dir := range opts.Config.Skills.Load.ExtraDirs {
			if trimmed := strings.TrimSpace(dir); trimmed != "" {
				sources = append(sources, skillSource{name: "extra", dir: trimmed})
			}
		}
	}
	if opts != nil && strings.TrimSpace(opts.BundledSkillsDir) != "" {
		sources = append(sources, skillSource{name: "bundled", dir: strings.TrimSpace(opts.BundledSkillsDir), allowlist: allowBundled})
	}
	if opts != nil && strings.TrimSpace(opts.ManagedSkillsDir) != "" {
		sources = append(sources, skillSource{name: "managed", dir: strings.TrimSpace(opts.ManagedSkillsDir)})
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		sources = append(sources, skillSource{name: "agents-skills-personal", dir: filepath.Join(home, ".agents", "skills")})
	}
	sources = append(sources, skillSource{name: "agents-skills-project", dir: filepath.Join(workspaceDir, ".agents", "skills")})
	sources = append(sources, skillSource{name: "workspace", dir: filepath.Join(workspaceDir, "skills")})
	return sources
}

func loadSkillEntriesFromRoot(source skillSource, filter []string, limits skillsLimits, opts *WorkspaceSkillBuildOptions) ([]core.SkillEntry, error) {
	root := source.dir
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	if nested := filepath.Join(root, "skills"); root != nested {
		if nestedInfo, err := os.Stat(nested); err == nil && nestedInfo.IsDir() {
			root = nested
		}
	}
	rootSkill := filepath.Join(root, DefaultSkillFilename)
	if stat, err := os.Stat(rootSkill); err == nil && !stat.IsDir() {
		if stat.Size() > int64(limits.MaxSkillFileBytes) {
			return nil, nil
		}
		entry, loadErr := loadSingleSkillEntry(rootSkill, source.name)
		if loadErr != nil {
			return nil, loadErr
		}
		if !isSkillAllowedFromSource(entry, source) || !isSkillEnabledByConfig(entry, opts) {
			return nil, nil
		}
		return filterWorkspaceSkillEntries([]core.SkillEntry{entry}, filter), nil
	}

	children, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	if limits.MaxCandidatesPerRoot > 0 && len(children) > limits.MaxCandidatesPerRoot {
		children = children[:limits.MaxCandidatesPerRoot]
	}
	sort.SliceStable(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	var entries []core.SkillEntry
	for _, child := range children {
		if !child.IsDir() || strings.HasPrefix(child.Name(), ".") {
			continue
		}
		path := filepath.Join(root, child.Name(), DefaultSkillFilename)
		stat, err := os.Stat(path)
		if err != nil || stat.IsDir() {
			continue
		}
		if stat.Size() > int64(limits.MaxSkillFileBytes) {
			continue
		}
		entry, err := loadSingleSkillEntry(path, source.name)
		if err != nil {
			continue
		}
		if !isSkillAllowedFromSource(entry, source) || !isSkillEnabledByConfig(entry, opts) {
			continue
		}
		entries = append(entries, entry)
		if len(entries) >= limits.MaxSkillsLoadedPerSource {
			break
		}
	}
	return filterWorkspaceSkillEntries(entries, filter), nil
}

func isSkillAllowedFromSource(entry core.SkillEntry, source skillSource) bool {
	if source.name != "bundled" || len(source.allowlist) == 0 {
		return true
	}
	_, ok := source.allowlist[strings.ToLower(strings.TrimSpace(entry.Name))]
	return ok
}

func isSkillEnabledByConfig(entry core.SkillEntry, opts *WorkspaceSkillBuildOptions) bool {
	if opts == nil {
		return true
	}
	if opts.IncludeDisabled {
		return true
	}
	skillCfg := resolveSkillConfigForEntry(opts.Config, entry)
	if skillCfg != nil && skillCfg.Enabled != nil && !*skillCfg.Enabled {
		return false
	}
	eval := evaluateSkillRequirements(entry, opts.Config, nil)
	return eval.Eligible
}

func loadSingleSkillEntry(skillPath string, source string) (core.SkillEntry, error) {
	content, err := os.ReadFile(skillPath)
	if err != nil {
		return core.SkillEntry{}, err
	}
	frontmatter, body := parseSkillFrontmatter(string(content))
	name := strings.TrimSpace(frontmatter["name"])
	if name == "" {
		name = filepath.Base(filepath.Dir(skillPath))
	}
	description := strings.TrimSpace(frontmatter["description"])
	if description == "" {
		description = firstBodyLine(body)
	}
	return core.SkillEntry{
		Name:         name,
		Description:  description,
		FilePath:     compactHomePath(skillPath),
		ResolvedPath: skillPath,
		Frontmatter:  frontmatter,
		Metadata: &core.SkillMetadata{
			Always:     parseFrontmatterBool(frontmatter["always"], false),
			SkillKey:   strings.TrimSpace(frontmatter["skill-key"]),
			PrimaryEnv: strings.TrimSpace(frontmatter["primary-env"]),
			Emoji:      strings.TrimSpace(frontmatter["emoji"]),
			Homepage:   strings.TrimSpace(frontmatter["homepage"]),
			OS:         parseFrontmatterList(frontmatter["os"]),
			Requires: &core.SkillRequirementSpec{
				Bins:    parseFrontmatterList(utils.NonEmpty(frontmatter["requires-bins"], frontmatter["requires_bins"])),
				AnyBins: parseFrontmatterList(utils.NonEmpty(frontmatter["requires-any-bins"], frontmatter["requires_any_bins"])),
				Env:     parseFrontmatterList(utils.NonEmpty(frontmatter["requires-env"], frontmatter["requires_env"])),
				Config:  parseFrontmatterList(utils.NonEmpty(frontmatter["requires-config"], frontmatter["requires_config"])),
			},
			Install: parseInstallSpecs(frontmatter),
			Source:  source,
			BaseDir: filepath.Dir(skillPath),
		},
		Invocation: core.SkillInvocationPolicy{
			UserInvocable:          parseFrontmatterBool(frontmatter["user-invocable"], true),
			DisableModelInvocation: parseFrontmatterBool(frontmatter["disable-model-invocation"], false),
		},
	}, nil
}

func buildWorkspaceSkillsPromptWithLimits(entries []core.SkillEntry, limits skillsLimits) (string, bool) {
	if len(entries) == 0 {
		return "", false
	}
	truncated := false
	if len(entries) > limits.MaxSkillsInPrompt {
		entries = append([]core.SkillEntry{}, entries[:limits.MaxSkillsInPrompt]...)
		truncated = true
	}
	build := func(items []core.SkillEntry) string {
		lines := []string{"<available_skills>"}
		for _, entry := range items {
			lines = append(lines, "<skill>")
			lines = append(lines, "<name>"+xmlEscape(entry.Name)+"</name>")
			if trimmed := strings.TrimSpace(entry.Description); trimmed != "" {
				lines = append(lines, "<description>"+xmlEscape(trimmed)+"</description>")
			}
			lines = append(lines, "<location>"+xmlEscape(entry.FilePath)+"</location>")
			lines = append(lines, "</skill>")
		}
		lines = append(lines, "</available_skills>")
		return strings.Join(lines, "\n")
	}
	prompt := build(entries)
	for len(prompt) > limits.MaxSkillsPromptChars && len(entries) > 0 {
		truncated = true
		entries = entries[:len(entries)-1]
		prompt = build(entries)
	}
	if prompt == "" {
		return "", truncated
	}
	if truncated {
		prompt = fmt.Sprintf("⚠️ Skills truncated: included %d entries.\n%s", len(entries), prompt)
	}
	return prompt, truncated
}

func parseSkillFrontmatter(content string) (map[string]string, string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]string{}, content
	}
	frontmatter := map[string]string{}
	i := 1
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			i++
			break
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:colon]))
		value := strings.TrimSpace(line[colon+1:])
		if key != "" && value != "" {
			frontmatter[key] = value
		}
	}
	return frontmatter, strings.Join(lines[i:], "\n")
}

func firstBodyLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseFrontmatterBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "1", "on":
		return true
	case "false", "no", "0", "off":
		return false
	default:
		return fallback
	}
}

func parseFrontmatterList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';'
	})
	var out []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseInstallSpecs(frontmatter map[string]string) []core.SkillInstallSpec {
	kind := strings.TrimSpace(frontmatter["install-kind"])
	if kind == "" {
		kind = strings.TrimSpace(frontmatter["install_kind"])
	}
	if kind == "" {
		return nil
	}
	spec := core.SkillInstallSpec{
		ID:      strings.TrimSpace(frontmatter["install-id"]),
		Kind:    kind,
		Label:   strings.TrimSpace(frontmatter["install-label"]),
		Bins:    parseFrontmatterList(frontmatter["install-bins"]),
		OS:      parseFrontmatterList(frontmatter["install-os"]),
		Formula: strings.TrimSpace(frontmatter["install-formula"]),
		Package: strings.TrimSpace(frontmatter["install-package"]),
		Module:  strings.TrimSpace(frontmatter["install-module"]),
		URL:     strings.TrimSpace(frontmatter["install-url"]),
	}
	return []core.SkillInstallSpec{spec}
}

func sanitizeSkillCommandName(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "skill"
	}
	replaced := strings.NewReplacer("/", "_", "-", "_", " ", "_").Replace(normalized)
	var b strings.Builder
	lastUnderscore := false
	for _, ch := range replaced {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			b.WriteRune(ch)
			lastUnderscore = false
		case ch == '_':
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "skill"
	}
	if len(out) > skillCommandMaxLength {
		out = strings.Trim(out[:skillCommandMaxLength], "_")
	}
	if out == "" {
		return "skill"
	}
	return out
}

func resolveUniqueSkillCommandName(base string, used map[string]struct{}) string {
	if _, ok := used[strings.ToLower(base)]; !ok {
		used[strings.ToLower(base)] = struct{}{}
		return base
	}
	for i := 2; i < 10_000; i++ {
		suffix := fmt.Sprintf("_%d", i)
		maxBaseLength := skillCommandMaxLength - len(suffix)
		if maxBaseLength < 1 {
			maxBaseLength = 1
		}
		trimmedBase := base
		if len(trimmedBase) > maxBaseLength {
			trimmedBase = trimmedBase[:maxBaseLength]
		}
		candidate := fmt.Sprintf("%s%s", trimmedBase, suffix)
		if _, ok := used[strings.ToLower(candidate)]; ok {
			continue
		}
		used[strings.ToLower(candidate)] = struct{}{}
		return candidate
	}
	return base
}

func truncateSkillDescription(description string, maxLen int) string {
	description = strings.TrimSpace(description)
	if len(description) <= maxLen {
		return description
	}
	if maxLen <= 1 {
		return description[:maxLen]
	}
	return description[:maxLen-1] + "…"
}

func resolveSkillCommandDispatch(frontmatter map[string]string) *core.SkillCommandDispatchSpec {
	kindRaw := strings.ToLower(strings.TrimSpace(utils.NonEmpty(frontmatter["command-dispatch"], frontmatter["command_dispatch"])))
	if kindRaw == "" || kindRaw != "tool" {
		return nil
	}
	toolName := strings.TrimSpace(utils.NonEmpty(frontmatter["command-tool"], frontmatter["command_tool"]))
	if toolName == "" {
		return nil
	}
	argMode := strings.ToLower(strings.TrimSpace(utils.NonEmpty(frontmatter["command-arg-mode"], frontmatter["command_arg_mode"])))
	if argMode == "" || argMode != "raw" {
		argMode = "raw"
	}
	return &core.SkillCommandDispatchSpec{
		Kind:     "tool",
		ToolName: toolName,
		ArgMode:  argMode,
	}
}

func compactHomePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err == nil {
		rel = filepath.Clean(rel)
		if rel == "." {
			return "~"
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return filepath.ToSlash(filepath.Join("~", rel))
		}
	}
	return path
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func (opts *WorkspaceSkillBuildOptions) GetEntries() []core.SkillEntry {
	if opts == nil {
		return nil
	}
	return opts.Entries
}

func OptsConfig(opts *WorkspaceSkillBuildOptions) *config.AppConfig {
	if opts == nil {
		return nil
	}
	return opts.Config
}
