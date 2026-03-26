package core

// ---------------------------------------------------------------------------
// Value Objects — Skills
// ---------------------------------------------------------------------------

type SkillInvocationPolicy struct {
	UserInvocable          bool `json:"userInvocable"`
	DisableModelInvocation bool `json:"disableModelInvocation"`
}

type SkillCommandDispatchSpec struct {
	Kind     string `json:"kind"`
	ToolName string `json:"toolName"`
	ArgMode  string `json:"argMode,omitempty"`
}

type SkillCommandSpec struct {
	Name        string                    `json:"name"`
	SkillName   string                    `json:"skillName"`
	Description string                    `json:"description"`
	Dispatch    *SkillCommandDispatchSpec `json:"dispatch,omitempty"`
}

type SkillInstallSpec struct {
	ID              string   `json:"id,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	Label           string   `json:"label,omitempty"`
	Bins            []string `json:"bins,omitempty"`
	OS              []string `json:"os,omitempty"`
	Formula         string   `json:"formula,omitempty"`
	Package         string   `json:"package,omitempty"`
	Module          string   `json:"module,omitempty"`
	URL             string   `json:"url,omitempty"`
	Archive         string   `json:"archive,omitempty"`
	Extract         bool     `json:"extract,omitempty"`
	StripComponents int      `json:"stripComponents,omitempty"`
	TargetDir       string   `json:"targetDir,omitempty"`
}

type SkillRequirementSpec struct {
	Bins    []string `json:"bins,omitempty"`
	AnyBins []string `json:"anyBins,omitempty"`
	Env     []string `json:"env,omitempty"`
	Config  []string `json:"config,omitempty"`
}

type SkillMetadata struct {
	Always     bool                  `json:"always,omitempty"`
	SkillKey   string                `json:"skillKey,omitempty"`
	PrimaryEnv string                `json:"primaryEnv,omitempty"`
	Emoji      string                `json:"emoji,omitempty"`
	Homepage   string                `json:"homepage,omitempty"`
	OS         []string              `json:"os,omitempty"`
	Requires   *SkillRequirementSpec `json:"requires,omitempty"`
	Install    []SkillInstallSpec    `json:"install,omitempty"`
	Source     string                `json:"source,omitempty"`
	BaseDir    string                `json:"baseDir,omitempty"`
}

type SkillEntry struct {
	Name         string                `json:"name"`
	Description  string                `json:"description"`
	FilePath     string                `json:"filePath"`
	ResolvedPath string                `json:"-"`
	Frontmatter  map[string]string     `json:"frontmatter,omitempty"`
	Metadata     *SkillMetadata        `json:"metadata,omitempty"`
	Invocation   SkillInvocationPolicy `json:"invocation"`
}

type SkillSnapshot struct {
	Prompt       string             `json:"prompt"`
	Skills       []SkillEntry       `json:"skills"`
	Commands     []SkillCommandSpec `json:"commands,omitempty"`
	SkillFilter  []string           `json:"skillFilter,omitempty"`
	Version      int                `json:"version,omitempty"`
	ResolvedName []string           `json:"resolvedName,omitempty"`
}

type SkillSnapshotSummary struct {
	Version      int      `json:"version,omitempty"`
	SkillNames   []string `json:"skillNames,omitempty"`
	CommandNames []string `json:"commandNames,omitempty"`
}

type SkillRemoteEligibility struct {
	Platforms []string
	HasBin    func(bin string) bool
	HasAnyBin func(bins []string) bool
	Note      string
}

type SkillEligibilityContext struct {
	Remote *SkillRemoteEligibility
}

type SkillInstallPreferences struct {
	PreferBrew  bool   `json:"preferBrew,omitempty"`
	NodeManager string `json:"nodeManager,omitempty"`
}

type SkillInstallOption struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"`
	Label string   `json:"label"`
	Bins  []string `json:"bins,omitempty"`
}

type SkillStatusEntry struct {
	Name               string               `json:"name"`
	Description        string               `json:"description"`
	Source             string               `json:"source,omitempty"`
	FilePath           string               `json:"filePath"`
	BaseDir            string               `json:"baseDir,omitempty"`
	SkillKey           string               `json:"skillKey,omitempty"`
	PrimaryEnv         string               `json:"primaryEnv,omitempty"`
	Emoji              string               `json:"emoji,omitempty"`
	Homepage           string               `json:"homepage,omitempty"`
	Always             bool                 `json:"always,omitempty"`
	Disabled           bool                 `json:"disabled,omitempty"`
	Eligible           bool                 `json:"eligible,omitempty"`
	BlockedByAllowlist bool                 `json:"blockedByAllowlist,omitempty"`
	MissingBins        []string             `json:"missingBins,omitempty"`
	MissingEnv         []string             `json:"missingEnv,omitempty"`
	MissingConfig      []string             `json:"missingConfig,omitempty"`
	ConfigChecks       []SkillConfigCheck   `json:"configChecks,omitempty"`
	Install            []SkillInstallOption `json:"install,omitempty"`
}

type SkillConfigCheck struct {
	Path      string `json:"path"`
	Satisfied bool   `json:"satisfied"`
}

type SkillStatusReport struct {
	WorkspaceDir string             `json:"workspaceDir"`
	Version      int                `json:"version,omitempty"`
	Skills       []SkillStatusEntry `json:"skills"`
}

type SkillInstallResult struct {
	OK       bool     `json:"ok"`
	Message  string   `json:"message"`
	Stdout   string   `json:"stdout,omitempty"`
	Stderr   string   `json:"stderr,omitempty"`
	Code     int      `json:"code,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}
