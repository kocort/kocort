package infra

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/core"
	memorypkg "github.com/kocort/kocort/internal/memory"
)

// PromptTool is a minimal interface for tools used in prompt building.
// The full Tool interface (with Execute) remains in runtime/.
type PromptTool interface {
	Name() string
	Description() string
}

type PromptMode string

const (
	PromptModeFull    PromptMode = "full"
	PromptModeMinimal PromptMode = "minimal"
	PromptModeNone    PromptMode = "none"
)

func BuildSystemPrompt(params PromptBuildParams) string {
	mode := params.Mode
	if mode == "" {
		mode = PromptModeFull
	}
	isMinimal := mode == PromptModeMinimal

	// Phase 4: Apply context-budget filtering to context files when a budget
	// is available, replacing the legacy hardcoded byte limits.
	if params.ContextBudget != nil && len(params.ContextFiles) > 0 {
		params.ContextFiles = params.ContextBudget.AllocateContextFiles(params.ContextFiles)
	}

	if mode == PromptModeNone {
		if strings.TrimSpace(params.Identity.PersonaPrompt) != "" {
			return strings.TrimSpace(params.Identity.PersonaPrompt)
		}
		return "You are a personal assistant running inside Kocort."
	}
	var sections []string
	if strings.TrimSpace(params.Identity.PersonaPrompt) != "" {
		sections = append(sections, strings.TrimSpace(params.Identity.PersonaPrompt))
	}
	if skillSection := BuildSkillsPromptSection(params.Skills, params.Tools); skillSection != "" {
		sections = append(sections, skillSection)
	}
	if toolSection := BuildToolPromptSection(params.Tools, params.ToolSummaries); toolSection != "" {
		sections = append(sections, toolSection)
	}
	if toolCallStyle := BuildToolCallStylePromptSection(params.Tools); toolCallStyle != "" {
		sections = append(sections, toolCallStyle)
	}
	if safetySection := BuildSafetyPromptSection(); safetySection != "" {
		sections = append(sections, safetySection)
	}
	if !isMinimal {
		if cliRef := BuildCliQuickReferenceSection(); cliRef != "" {
			sections = append(sections, cliRef)
		}
	}
	if !isMinimal {
		if memoryRecall := BuildMemoryRecallPromptSection(params.Tools, params.Identity.MemoryCitationsMode); memoryRecall != "" {
			sections = append(sections, memoryRecall)
		}
	}
	if !isMinimal {
		if authSenders := BuildAuthorizedSendersSection(params.OwnerLine); authSenders != "" {
			sections = append(sections, authSenders)
		}
	}
	if !isMinimal {
		if modelAliases := BuildModelAliasesSection(params.ModelAliases); modelAliases != "" {
			sections = append(sections, modelAliases)
		}
	}
	if !isMinimal {
		if replyTagsSection := BuildReplyTagsPromptSection(); replyTagsSection != "" {
			sections = append(sections, replyTagsSection)
		}
	}
	if !isMinimal {
		if messagingSection := BuildMessagingPromptSection(params.Tools); messagingSection != "" {
			sections = append(sections, messagingSection)
		}
	}
	if !isMinimal {
		if voiceSection := BuildVoiceSection(params.TTSHint); voiceSection != "" {
			sections = append(sections, voiceSection)
		}
	}
	if !isMinimal {
		if reactionsSection := BuildReactionsSection(params.ReactionGuidance); reactionsSection != "" {
			sections = append(sections, reactionsSection)
		}
	}
	if reasoningFmt := BuildReasoningFormatSection(params.ReasoningHint); reasoningFmt != "" {
		sections = append(sections, reasoningFmt)
	}
	if toolGuidance := BuildToolGuidanceSection(params.Tools); toolGuidance != "" {
		sections = append(sections, toolGuidance)
	}
	if !isMinimal {
		if identitySection := BuildIdentityPromptSection(params.Identity); identitySection != "" {
			sections = append(sections, identitySection)
		}
	}
	if runtimeSection := BuildRuntimePromptSection(params); runtimeSection != "" {
		sections = append(sections, runtimeSection)
	}
	if workspaceSection := BuildWorkspacePromptSection(params.Identity.WorkspaceDir, params.WorkspaceNotes); workspaceSection != "" {
		sections = append(sections, workspaceSection)
	}
	if !isMinimal {
		if docsSection := BuildDocumentationPromptSection(params.DocsPath); docsSection != "" {
			sections = append(sections, docsSection)
		}
	}
	if sandboxSection := BuildSandboxPromptSection(params.Sandbox); sandboxSection != "" {
		sections = append(sections, sandboxSection)
	}
	if !isMinimal {
		if timeSection := BuildTimePromptSection(params.Identity, params.Request); timeSection != "" {
			sections = append(sections, timeSection)
		}
	}
	if workspaceFilesSection := BuildWorkspaceFilesInjectedPromptSection(params.ContextFiles, params.BootstrapWarnings); workspaceFilesSection != "" {
		sections = append(sections, workspaceFilesSection)
	}
	if !isMinimal {
		if silentReplies := BuildSilentRepliesSection(); silentReplies != "" {
			sections = append(sections, silentReplies)
		}
	}
	if !isMinimal {
		if heartbeats := BuildHeartbeatsSection(params.HeartbeatEnabled); heartbeats != "" {
			sections = append(sections, heartbeats)
		}
	}
	if strings.TrimSpace(params.Request.ExtraSystemPrompt) != "" {
		header := ""
		if isMinimal {
			header = "## Subagent Context"
		}
		if header != "" {
			sections = append(sections, header)
		}
		sections = append(sections, strings.TrimSpace(params.Request.ExtraSystemPrompt))
	}
	if warningSection := BuildBootstrapWarningsSection(params.BootstrapWarnings); warningSection != "" {
		sections = append(sections, warningSection)
	}
	if eventsSection := BuildInternalEventsPromptSection(params.InternalEvents); eventsSection != "" {
		sections = append(sections, eventsSection)
	}
	if contextFilesSection := BuildContextFilesPromptSection(params.ContextFiles); contextFilesSection != "" {
		sections = append(sections, contextFilesSection)
	}
	// Legacy sections: transcript summary, memory hits, attachments, and current
	// user message injected into system prompt. When OmitUserMessageInSystemPrompt
	if !params.OmitUserMessageInSystemPrompt {
		if historySection := BuildTranscriptPromptSection(params.History); historySection != "" {
			sections = append(sections, historySection)
		}
	}
	if memorySection := BuildMemoryPromptSection(params.MemoryHits); memorySection != "" {
		if strings.TrimSpace(params.Identity.MemoryCitationsMode) != "" {
			memorySection = BuildMemoryPromptSectionWithCitations(params.MemoryHits, params.Identity.MemoryCitationsMode)
		}
		sections = append(sections, memorySection)
	}
	if !params.OmitUserMessageInSystemPrompt {
		if attachmentSection := BuildAttachmentPromptSection(params.Request.Attachments); attachmentSection != "" {
			sections = append(sections, attachmentSection)
		}
		if strings.TrimSpace(params.Request.Message) != "" {
			sections = append(sections, "Current user message:\n"+strings.TrimSpace(params.Request.Message))
		}
	}
	if params.Request.SpawnedBy != "" {
		sections = append(sections, fmt.Sprintf("Subagent context: spawned by %s at depth %d.", params.Request.SpawnedBy, params.Request.SpawnDepth))
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

type PromptContextFile = memorypkg.PromptContextFile

type PromptSandboxInfo struct {
	Enabled          bool
	Mode             string
	WorkspaceAccess  string
	Scope            string
	WorkspaceRoot    string
	DefaultWorkdir   string
	SandboxWorkspace string
	AgentWorkspace   string
}

type PromptBuildParams struct {
	InternalEvents    []core.TranscriptMessage
	ContextFiles      []PromptContextFile
	BootstrapWarnings []string
	Identity          core.AgentIdentity
	Request           core.AgentRunRequest
	ModelSelection    core.ModelSelection
	History           []core.TranscriptMessage
	MemoryHits        []core.MemoryHit
	Tools             []PromptTool
	Skills            *core.SkillSnapshot
	ToolSummaries     map[string]string
	DocsPath          string
	WorkspaceNotes    []string
	Sandbox           PromptSandboxInfo
	Mode              PromptMode

	// ModelAliases maps short alias names to full provider/model specifiers.
	ModelAliases map[string]string
	// OwnerLine describes the authorized owner/sender for the session.
	OwnerLine string
	// TTSHint provides voice/TTS guidance text (empty = no section).
	TTSHint string
	// ReactionGuidance controls the ## Reactions section (nil = no section).
	ReactionGuidance *ReactionGuidance
	// HeartbeatEnabled controls whether the ## Heartbeats section is emitted.
	HeartbeatEnabled bool
	// ReasoningHint provides reasoning format instructions (empty = no section).
	ReasoningHint string
	// OmitUserMessageInSystemPrompt when true omits "Current user message:" and
	// "Recent conversation:" from the system prompt, relying on the messages
	OmitUserMessageInSystemPrompt bool

	// --- Phase 4: Context Engine fields ---

	// ContextBudget, when non-nil, is used to filter/truncate ContextFiles
	// based on the model's context window rather than hardcoded byte limits.
	ContextBudget *ContextBudget
}

// ReactionGuidance configures the ## Reactions prompt section.
type ReactionGuidance struct {
	Level   string // "minimal" or "extensive"
	Channel string
}

func SelectInternalPromptEvents(history []core.TranscriptMessage) []core.TranscriptMessage {
	if len(history) == 0 {
		return nil
	}
	var out []core.TranscriptMessage
	for _, message := range history {
		typeName := strings.ToLower(strings.TrimSpace(message.Type))
		if typeName != "system" && typeName != "internal" && strings.TrimSpace(message.Event) == "" {
			continue
		}
		out = append(out, message)
	}
	if len(out) > 4 {
		out = out[len(out)-4:]
	}
	return out
}

func loadPromptContextFiles(workspaceDir string, chatType core.ChatType, includeHeartbeat bool) ([]PromptContextFile, []string) {
	return memorypkg.LoadPromptContextFiles(workspaceDir, chatType, includeHeartbeat)
}

func BuildInternalEventsPromptSection(events []core.TranscriptMessage) string {
	if len(events) == 0 {
		return ""
	}
	lines := []string{"## Internal Events"}
	for _, event := range events {
		label := strings.TrimSpace(event.Event)
		if label == "" {
			label = promptNonEmpty(strings.TrimSpace(event.Type), "system")
		}
		text := strings.TrimSpace(event.Text)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", label, text))
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func BuildBootstrapWarningsSection(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := []string{"## Bootstrap Warnings"}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, "- "+line)
		}
	}
	if len(out) == 1 {
		return ""
	}
	return strings.Join(out, "\n")
}

func BuildContextFilesPromptSection(files []PromptContextFile) string {
	if len(files) == 0 {
		return ""
	}
	lines := []string{"## Context Files"}
	for _, file := range files {
		if strings.TrimSpace(file.Content) == "" {
			continue
		}
		title := promptNonEmpty(strings.TrimSpace(file.Title), strings.TrimSpace(filepath.Base(file.Path)))
		header := fmt.Sprintf("%s (%s):", title, strings.TrimSpace(file.Path))
		if file.Truncated {
			header += " [truncated]"
		}
		lines = append(lines, header)
		lines = append(lines, file.Content)
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func BuildWorkspaceFilesInjectedPromptSection(files []PromptContextFile, warnings []string) string {
	validFiles := 0
	for _, file := range files {
		if strings.TrimSpace(file.Path) != "" && strings.TrimSpace(file.Content) != "" {
			validFiles++
		}
	}
	if validFiles == 0 && len(warnings) == 0 {
		return ""
	}
	lines := []string{
		"## Workspace Files (injected)",
		"These user-editable files are loaded into prompt context below.",
	}
	if len(warnings) > 0 {
		lines = append(lines, "Bootstrap warnings will be shown before the injected file contents.")
	}
	return strings.Join(lines, "\n")
}

func BuildSkillsPromptSection(snapshot *core.SkillSnapshot, tools []PromptTool) string {
	if snapshot == nil || strings.TrimSpace(snapshot.Prompt) == "" {
		return ""
	}
	readToolName := "read"
	if !hasTool(tools, readToolName) {
		readToolName = ""
	}
	readInstruction := "- If exactly one skill clearly applies: read its SKILL.md at <location>, then follow it."
	if readToolName != "" {
		readInstruction = fmt.Sprintf("- If exactly one skill clearly applies: read its SKILL.md at <location> with `%s`, then follow it.", readToolName)
	}
	lines := []string{
		"## Skills (mandatory)",
		"Before replying: scan <available_skills> <description> entries.",
		readInstruction,
		"- If multiple could apply: choose the most specific one, then read/follow it.",
		"- If none clearly apply: do not read any SKILL.md.",
		"Constraints: never read more than one skill up front; only read after selecting.",
		"- When a skill drives external API writes, assume rate limits: prefer fewer larger writes, avoid tight one-item loops, serialize bursts when possible, and respect 429/Retry-After.",
		snapshot.Prompt,
	}
	if len(snapshot.Commands) > 0 {
		lines = append(lines, "", "User-invocable skill commands:")
		for _, command := range snapshot.Commands {
			line := fmt.Sprintf("- /%s -> %s", command.Name, command.SkillName)
			if strings.TrimSpace(command.Description) != "" {
				line += ": " + strings.TrimSpace(command.Description)
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func BuildIdentityPromptSection(identity core.AgentIdentity) string {
	var lines []string
	if identity.Name != "" && identity.Name != identity.ID {
		lines = append(lines, "Agent name: "+identity.Name)
	}
	if identity.Emoji != "" {
		lines = append(lines, "Agent emoji: "+identity.Emoji)
	}
	if identity.Theme != "" {
		lines = append(lines, "Agent theme: "+identity.Theme)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func BuildRuntimePromptSection(params PromptBuildParams) string {
	lines := []string{"## Runtime"}
	lines = append(lines, BuildRuntimeLine(params))
	return strings.Join(lines, "\n")
}

func BuildRuntimeLine(params PromptBuildParams) string {
	repoRoot, _ := os.Getwd() // best-effort; empty string fallback is acceptable
	shell := os.Getenv("SHELL")
	channel := strings.TrimSpace(strings.ToLower(params.Request.Channel))
	capabilities := "none"
	if channel != "" {
		capabilities = "final-reply"
	}
	parts := []string{
		fmt.Sprintf("agent=%s", params.Identity.ID),
	}
	if repoRoot = strings.TrimSpace(repoRoot); repoRoot != "" {
		parts = append(parts, "repo="+repoRoot)
	}
	parts = append(parts, fmt.Sprintf("os=%s (%s)", runtime.GOOS, runtime.GOARCH))
	if version := strings.TrimPrefix(runtime.Version(), "go"); version != "" {
		parts = append(parts, "go="+version)
	}
	if strings.TrimSpace(params.ModelSelection.Model) != "" {
		model := params.ModelSelection.Model
		if strings.TrimSpace(params.ModelSelection.Provider) != "" && !strings.Contains(model, "/") {
			model = params.ModelSelection.Provider + "/" + model
		}
		parts = append(parts, "model="+model)
	}
	if strings.TrimSpace(params.Identity.DefaultModel) != "" {
		defaultModel := params.Identity.DefaultModel
		if strings.TrimSpace(params.Identity.DefaultProvider) != "" && !strings.Contains(defaultModel, "/") {
			defaultModel = params.Identity.DefaultProvider + "/" + defaultModel
		}
		parts = append(parts, "default_model="+defaultModel)
	}
	if strings.TrimSpace(shell) != "" {
		parts = append(parts, "shell="+shell)
	}
	if channel != "" {
		parts = append(parts, "channel="+channel)
		parts = append(parts, "capabilities="+capabilities)
	}
	parts = append(parts, "thinking="+promptNonEmpty(strings.TrimSpace(params.ModelSelection.ThinkLevel), "off"))
	return "Runtime: " + strings.Join(parts, " | ")
}

func BuildToolPromptSection(tools []PromptTool, externalSummaries map[string]string) string {
	if len(tools) == 0 {
		return ""
	}
	ordered := orderPromptTools(tools)
	if len(ordered) == 0 {
		return ""
	}
	lines := []string{
		"## Tooling",
		"Tool availability (filtered by policy):",
		"Tool names are case-sensitive. Call tools exactly as listed.",
	}
	for _, tool := range ordered {
		name := strings.TrimSpace(tool.Name())
		desc := resolvePromptToolSummary(tool, externalSummaries)
		if desc == "" {
			lines = append(lines, "- "+name)
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", name, desc))
	}
	lines = append(lines, "TOOLS.md does not control tool availability; it is user guidance for how to use external tools.")
	if hasTool(ordered, "exec") || hasTool(ordered, "process") {
		lines = append(lines, "For long waits, avoid rapid poll loops: use exec with enough wait time or process polling with a reasonable timeout.")
	}
	if hasTool(ordered, "sessions_spawn") {
		lines = append(lines, "If a task is more complex or takes longer, spawn a sub-agent. Completion is push-based.")
	}
	return strings.Join(lines, "\n")
}

func BuildToolCallStylePromptSection(tools []PromptTool) string {
	if len(tools) == 0 {
		return ""
	}
	lines := []string{
		"## Tool Call Style",
		"Default: do not narrate routine, low-risk tool calls (just call the tool).",
		"Narrate only when it helps: multi-step work, complex/challenging problems, sensitive actions, or when the user explicitly asks.",
		"Keep narration brief and value-dense; avoid repeating obvious steps.",
		"Use plain human language for narration unless in a technical context.",
		"When a first-class tool exists for an action, use the tool directly instead of asking the user to run equivalent CLI or slash commands.",
	}
	return strings.Join(lines, "\n")
}

func BuildMemoryRecallPromptSection(tools []PromptTool, citationsMode string) string {
	names := collectPromptToolNames(tools)
	if !hasAnyTool(names, "memory_search", "memory_get") {
		return ""
	}
	lines := []string{
		"## Memory Recall",
		"Before answering anything about prior work, decisions, dates, people, preferences, or todos: run memory_search on MEMORY.md + memory/*.md; then use memory_get to pull only the needed lines. If low confidence after search, say you checked.",
	}
	if strings.EqualFold(strings.TrimSpace(citationsMode), "off") {
		lines = append(lines, "Citations are disabled: do not mention file paths or line numbers in replies unless the user explicitly asks.")
	} else {
		lines = append(lines, "Citations: include Source: <path#line> when it helps the user verify memory snippets.")
	}
	return strings.Join(lines, "\n")
}

func BuildMessagingPromptSection(tools []PromptTool) string {
	names := collectPromptToolNames(tools)
	if !hasAnyTool(names, "message", "sessions_send", "subagents", "sessions_spawn") {
		return ""
	}
	lines := []string{
		"## Messaging",
		"- Reply in current session automatically routes to the source channel.",
		"- Cross-session messaging: use sessions_send(sessionKey, message).",
		"- Sub-agent orchestration: use subagents(action=list|steer|kill).",
		"- Never use exec/curl for provider messaging; the runtime handles routing internally.",
	}
	if hasAnyTool(names, "message") {
		lines = append(lines,
			"",
			"### message tool",
			"- Use message for proactive sends and channel actions.",
			"- If you use message to deliver the user-visible reply, avoid sending a second normal reply.",
		)
	}
	return strings.Join(lines, "\n")
}

func BuildSafetyPromptSection() string {
	lines := []string{
		"## Safety",
		"You have no independent goals: do not pursue self-preservation, replication, resource acquisition, or power-seeking; avoid long-term plans beyond the user's request.",
		"Prioritize safety and human oversight over completion; if instructions conflict, pause and ask.",
		"Do not manipulate anyone to expand access or disable safeguards.",
		"Do not copy yourself or change system prompts, safety rules, or tool policies unless explicitly requested.",
	}
	return strings.Join(lines, "\n")
}

func BuildReplyTagsPromptSection() string {
	lines := []string{
		"## Reply Tags",
		"To request a native reply or quote on supported surfaces, include one tag as the very first token in your reply.",
		"- Prefer [[reply_to_current]].",
		"- Use [[reply_to:<id>]] only when an id was explicitly provided by the user or a tool.",
		"- Tags are stripped before sending; support depends on the current channel config.",
	}
	return strings.Join(lines, "\n")
}

func BuildDocumentationPromptSection(docsPath string) string {
	docsPath = strings.TrimSpace(docsPath)
	if docsPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			candidate := filepath.Join(cwd, "docs")
			if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
				docsPath = "docs"
			}
		}
	}
	if docsPath == "" {
		return ""
	}
	lines := []string{
		"## Documentation",
		fmt.Sprintf("Local docs: %s", docsPath),
		"For repository behavior, commands, config, or architecture: consult local docs first.",
	}
	return strings.Join(lines, "\n")
}

func BuildSandboxPromptSection(info PromptSandboxInfo) string {
	if !info.Enabled {
		return ""
	}
	lines := []string{
		"## Sandbox",
		"You are running in a sandboxed runtime.",
		"Some tools may be unavailable due to sandbox policy.",
		"Sub-agents stay sandboxed; if you need outside-sandbox access, ask first.",
	}
	if mode := strings.TrimSpace(info.Mode); mode != "" {
		lines = append(lines, "Sandbox mode: "+mode)
	}
	if workdir := strings.TrimSpace(info.DefaultWorkdir); workdir != "" {
		lines = append(lines, "Default working directory: "+workdir)
	}
	if workspaceDir := strings.TrimSpace(info.SandboxWorkspace); workspaceDir != "" {
		lines = append(lines, "Sandbox workspace: "+workspaceDir)
	}
	if agentWorkspace := strings.TrimSpace(info.AgentWorkspace); agentWorkspace != "" {
		lines = append(lines, "Agent workspace: "+agentWorkspace)
	}
	if access := strings.TrimSpace(info.WorkspaceAccess); access != "" {
		lines = append(lines, "Workspace access: "+access)
	}
	if scope := strings.TrimSpace(info.Scope); scope != "" {
		lines = append(lines, "Sandbox scope: "+scope)
	}
	return strings.Join(lines, "\n")
}

func BuildTimePromptSection(identity core.AgentIdentity, request core.AgentRunRequest) string {
	userTimezone := strings.TrimSpace(request.UserTimezone)
	if userTimezone == "" {
		userTimezone = strings.TrimSpace(identity.UserTimezone)
	}
	if userTimezone == "" {
		return ""
	}
	formattedTime := FormatUserTimeInTimezone(userTimezone, false)
	return "## Current Date & Time\n" + formattedTime + "\nTime zone: " + userTimezone
}

func BuildToolGuidanceSection(tools []PromptTool) string {
	names := collectPromptToolNames(tools)
	if len(names) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, memorypkg.BuildMemoryToolGuidance(names)...)
	if hasAnyTool(names, "sessions_list", "sessions_history", "sessions_send") {
		lines = append(lines,
			"If you need another session's state, use sessions_list/sessions_history/sessions_send instead of guessing.",
			"- Use sessions_list to discover candidate sessions.",
			"- Use sessions_history when you need prior context from another session.",
			"- Use sessions_send when the task requires delivering work or asking another session to act.",
		)
	}
	if hasAnyTool(names, "sessions_spawn", "subagents") {
		lines = append(lines,
			"Subagent orchestration is push-based.",
			"- Spawn children when decomposition helps.",
			"- Do not poll sessions_list or subagents in a loop.",
			"- Use subagents only for on-demand steer/kill/status checks.",
		)
	}
	if hasAnyTool(names, "cron") {
		lines = append(lines,
			"If the user asks for a reminder, timed follow-up, or scheduled action, use cron instead of only promising to remember.",
			"- Use cron add for reminders and scheduled tasks.",
			"- Write the scheduled text so it reads like a reminder when it fires.",
			"- Mention that it is a reminder when helpful depending on the time gap between scheduling and firing.",
			"- Prefer scheduling back to the current session unless the user asked for a different target.",
			"- After using cron successfully, continue the turn normally and confirm the plan in your own words.",
		)
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Tool Guidance\n" + strings.Join(lines, "\n")
}

func collectPromptToolNames(tools []PromptTool) map[string]struct{} {
	names := map[string]struct{}{}
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := strings.TrimSpace(tool.Name())
		if name == "" {
			continue
		}
		names[name] = struct{}{}
	}
	return names
}

func hasTool(tools []PromptTool, name string) bool {
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if strings.TrimSpace(tool.Name()) == name {
			return true
		}
	}
	return false
}

func resolvePromptToolSummary(tool PromptTool, external map[string]string) string {
	name := strings.TrimSpace(tool.Name())
	if summary := strings.TrimSpace(toolSummary(name)); summary != "" {
		return summary
	}
	if external != nil {
		if summary := strings.TrimSpace(external[strings.ToLower(name)]); summary != "" {
			return summary
		}
	}
	return strings.TrimSpace(tool.Description())
}

func toolSummary(name string) string {
	switch name {
	case "read":
		return "Read file contents"
	case "write":
		return "Create or overwrite files"
	case "edit":
		return "Make precise edits to files"
	case "apply_patch":
		return "Apply multi-file patches"
	case "grep":
		return "Search file contents for patterns"
	case "find":
		return "Find files by glob pattern"
	case "ls":
		return "List directory contents"
	case "exec":
		return "Run shell commands (pty available for TTY-required CLIs)"
	case "process":
		return "Manage background exec sessions"
	case "web_search":
		return "Search the web (Brave API)"
	case "web_fetch":
		return "Fetch and extract readable content from a URL"
	case "browser":
		return "Control web browser"
	case "canvas":
		return "Present/eval/snapshot the Canvas"
	case "nodes":
		return "List/describe/notify/camera/screen on paired nodes"
	case "cron":
		return "Manage cron jobs and wake events"
	case "message":
		return "Send messages and channel actions"
	case "gateway":
		return "Restart, apply config, or run updates on the running kocort process"
	case "agents_list":
		return "List agent ids allowed for sessions_spawn"
	case "sessions_list":
		return "List other sessions (incl. sub-agents) with filters/last"
	case "sessions_history":
		return "Fetch history for another session/sub-agent"
	case "sessions_send":
		return "Send a message to another session/sub-agent"
	case "sessions_spawn":
		return "Spawn an isolated sub-agent session"
	case "subagents":
		return "List, steer, or kill sub-agent runs for this requester session"
	case "session_status":
		return "Show a /status-equivalent status card (usage + time + Reasoning/Verbose/Elevated); use for model-use questions; optional per-session model override"
	case "image":
		return "Analyze an image with the configured image model"
	case "memory_search":
		return "Search durable workspace memory"
	case "memory_get":
		return "Read a specific memory file or line range"
	default:
		return ""
	}
}

// coreToolOrder defines the canonical display ordering for tools in prompts.
// Mirrors tool.CoreToolOrder from internal/tool/catalog.go (kept here to avoid
// an import cycle between infra and tool packages).
var coreToolOrder = []string{
	// Files
	"read", "write", "edit", "apply_patch", "grep", "find", "ls",
	// Runtime
	"exec", "process",
	// Web
	"web_search", "web_fetch", "browser",
	// Memory
	"memory_search", "memory_get",
	// Sessions
	"sessions_spawn", "sessions_list", "sessions_history",
	"sessions_send", "subagents", "session_status",
	// UI
	"canvas",
	// Messaging
	"message",
	// Automation
	"cron", "gateway",
	// Agents
	"agents_list", "nodes",
	// Media
	"image", "image_generate",
}

func orderPromptTools(tools []PromptTool) []PromptTool {
	if len(tools) == 0 {
		return nil
	}
	order := coreToolOrder
	byName := map[string]PromptTool{}
	var extras []string
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := strings.TrimSpace(tool.Name())
		if name == "" {
			continue
		}
		if _, ok := byName[name]; ok {
			continue
		}
		byName[name] = tool
		extras = append(extras, name)
	}
	var out []PromptTool
	seen := map[string]struct{}{}
	for _, name := range order {
		tool, ok := byName[name]
		if !ok {
			continue
		}
		out = append(out, tool)
		seen[name] = struct{}{}
	}
	sort.Strings(extras)
	for _, name := range extras {
		if _, ok := seen[name]; ok {
			continue
		}
		out = append(out, byName[name])
	}
	return out
}

func hasAnyTool(toolNames map[string]struct{}, names ...string) bool {
	for _, name := range names {
		if _, ok := toolNames[name]; ok {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------

// BuildCliQuickReferenceSection returns a CLI quick-reference for the agent.
func BuildCliQuickReferenceSection() string {
	lines := []string{
		"## Kocort CLI Quick Reference",
		"Kocort is controlled via subcommands. Do not invent commands.",
		"To manage the Gateway daemon service (start/stop/restart):",
		"- kocort gateway status/start/stop/restart",
		"If unsure, ask the user to run `kocort help`",
	}
	return strings.Join(lines, "\n")
}

// BuildAuthorizedSendersSection returns the authorized senders section.
func BuildAuthorizedSendersSection(ownerLine string) string {
	ownerLine = strings.TrimSpace(ownerLine)
	if ownerLine == "" {
		return ""
	}
	return "## Authorized Senders\n" + ownerLine
}

// BuildModelAliasesSection returns the model aliases section.
func BuildModelAliasesSection(aliases map[string]string) string {
	if len(aliases) == 0 {
		return ""
	}
	lines := []string{
		"## Model Aliases",
		"Prefer aliases when specifying model overrides; full provider/model is also accepted.",
	}
	// Sort for deterministic output
	keys := make([]string, 0, len(aliases))
	for k := range aliases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, alias := range keys {
		model := strings.TrimSpace(aliases[alias])
		if model != "" {
			lines = append(lines, fmt.Sprintf("- %s → %s", alias, model))
		}
	}
	return strings.Join(lines, "\n")
}

// BuildVoiceSection returns the TTS/voice guidance section.
func BuildVoiceSection(ttsHint string) string {
	ttsHint = strings.TrimSpace(ttsHint)
	if ttsHint == "" {
		return ""
	}
	return "## Voice (TTS)\n" + ttsHint
}

// BuildReactionsSection returns the emoji reaction guidance section.
func BuildReactionsSection(guidance *ReactionGuidance) string {
	if guidance == nil {
		return ""
	}
	level := strings.TrimSpace(strings.ToLower(guidance.Level))
	if level == "" {
		level = "minimal"
	}
	var lines []string
	lines = append(lines, "## Reactions")
	switch level {
	case "extensive":
		lines = append(lines,
			"React liberally — whenever it feels natural or helps convey tone.",
			"Use unicode emoji only (no custom emoji, no colons).",
		)
	default: // "minimal"
		lines = append(lines,
			"React ONLY when truly relevant to show engagement.",
			"Keep it to at most 1 reaction per 5-10 exchanges.",
			"Use unicode emoji only (no custom emoji, no colons).",
		)
	}
	return strings.Join(lines, "\n")
}

// BuildReasoningFormatSection returns the reasoning format instructions.
func BuildReasoningFormatSection(hint string) string {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return ""
	}
	return "## Reasoning Format\n" + hint
}

// BuildSilentRepliesSection returns the silent reply instructions.
func BuildSilentRepliesSection() string {
	lines := []string{
		"## Silent Replies",
		"When you have nothing to say, respond with ONLY: __SILENT__",
		"⚠️ Rules:",
		"- It must be your ENTIRE message — nothing else",
		"- Never append it to an actual response",
		"- Never wrap it in markdown or code blocks",
	}
	return strings.Join(lines, "\n")
}

// BuildHeartbeatsSection returns the heartbeat detection instructions.
func BuildHeartbeatsSection(enabled bool) string {
	if !enabled {
		return ""
	}
	lines := []string{
		"## Heartbeats",
		"The system polls you periodically to check health.",
		"If you receive a heartbeat poll, reply exactly: HEARTBEAT_OK",
		"If something needs attention, do NOT include HEARTBEAT_OK — instead describe the situation.",
	}
	return strings.Join(lines, "\n")
}

func BuildWorkspacePromptSection(workspaceDir string, workspaceNotes []string) string {
	if strings.TrimSpace(workspaceDir) == "" {
		return ""
	}
	var lines []string
	lines = append(lines, "## Workspace", "Your working directory is: "+strings.TrimSpace(workspaceDir))
	for _, note := range workspaceNotes {
		note = strings.TrimSpace(note)
		if note != "" {
			lines = append(lines, note)
		}
	}
	if agentsContent, err := LoadWorkspaceTextFile(workspaceDir, DefaultAgentsFilename); err == nil {
		agentsContent = strings.TrimSpace(agentsContent)
		if agentsContent != "" {
			lines = append(lines, "", "Workspace AGENTS.md:", agentsContent)
		}
	}
	return strings.Join(lines, "\n")
}

func BuildMemoryPromptSection(memoryHits []core.MemoryHit) string {
	return memorypkg.BuildPromptSection(memoryHits)
}

func BuildMemoryPromptSectionWithCitations(memoryHits []core.MemoryHit, citationsMode string) string {
	return memorypkg.BuildPromptSectionWithCitations(memoryHits, citationsMode)
}

func BuildAttachmentPromptSection(attachments []core.Attachment) string {
	if len(attachments) == 0 {
		return ""
	}
	lines := []string{"Attachments:"}
	for _, attachment := range attachments {
		label := AttachmentDisplayName(attachment, "(unnamed)")
		mimeType := NormalizeAttachmentMime(attachment)
		summary := "file"
		if AttachmentIsImage(attachment) {
			summary = "image"
		} else if text, ok, truncated := AttachmentPromptText(attachment); ok {
			summary = "text"
			preview := text
			if len(preview) > MaxPromptAttachmentPreviewChars {
				preview = strings.TrimSpace(preview[:MaxPromptAttachmentPreviewChars]) + "…"
			} else if truncated {
				preview += "…"
			}
			preview = strings.ReplaceAll(preview, "\n", " ")
			if preview != "" {
				summary += ": " + preview
			}
		} else if len(attachment.Content) == 0 {
			summary = "empty"
		} else {
			summary = "binary"
		}
		if mimeType != "" {
			lines = append(lines, fmt.Sprintf("- %s [%s] (%s, %d bytes)", label, mimeType, summary, len(attachment.Content)))
		} else {
			lines = append(lines, fmt.Sprintf("- %s (%s, %d bytes)", label, summary, len(attachment.Content)))
		}
		if text, ok, truncated := AttachmentPromptText(attachment); ok && text != "" {
			header := fmt.Sprintf("Attachment content: %s", label)
			if truncated {
				header += " (truncated)"
			}
			lines = append(lines, header)
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}

func BuildTranscriptPromptSection(history []core.TranscriptMessage) string {
	start := 0
	if len(history) > 6 {
		start = len(history) - 6
	}
	lines := []string{"Recent conversation:"}
	for _, msg := range history[start:] {
		line := FormatTranscriptPromptLine(msg)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func FormatTranscriptPromptLine(msg core.TranscriptMessage) string {
	switch strings.TrimSpace(strings.ToLower(msg.Type)) {
	case "compaction":
		text := strings.TrimSpace(msg.Summary)
		if text == "" {
			text = strings.TrimSpace(msg.Text)
		}
		if text == "" {
			return ""
		}
		return fmt.Sprintf("- compaction summary: %s", text)
	case "tool_call":
		if strings.TrimSpace(msg.ToolName) == "" {
			return ""
		}
		return fmt.Sprintf("- assistant called tool %s", strings.TrimSpace(msg.ToolName))
	case "tool_result":
		text := strings.TrimSpace(msg.Text)
		if strings.TrimSpace(msg.ToolName) == "" || text == "" {
			return ""
		}
		return fmt.Sprintf("- tool %s result: %s", strings.TrimSpace(msg.ToolName), text)
	case "system_event", "internal_event":
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			return ""
		}
		return fmt.Sprintf("- system: %s", text)
	}
	role := strings.TrimSpace(msg.Role)
	if role == "" {
		role = "message"
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return ""
	}
	return fmt.Sprintf("- %s: %s", role, text)
}

const skillCommandMaxLength = 32

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

func promptNonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}
