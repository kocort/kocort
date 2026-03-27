package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

type stubPromptTool struct {
	name string
	desc string
}

func (t stubPromptTool) Name() string { return t.name }

func (t stubPromptTool) Description() string { return t.desc }

func TestBuildInternalEventsPromptSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildInternalEventsPromptSection(nil) != "" {
			t.Error("expected empty for nil events")
		}
	})

	t.Run("with_events", func(t *testing.T) {
		events := []core.TranscriptMessage{
			{Type: "system", Event: "boot", Text: "System started"},
			{Type: "internal", Event: "config", Text: "Config loaded"},
		}
		result := BuildInternalEventsPromptSection(events)
		if !strings.Contains(result, "Internal Events") {
			t.Error("should contain header")
		}
		if !strings.Contains(result, "boot: System started") {
			t.Error("should contain event text")
		}
	})

	t.Run("skips_empty_text", func(t *testing.T) {
		events := []core.TranscriptMessage{
			{Type: "system", Event: "boot", Text: ""},
		}
		result := BuildInternalEventsPromptSection(events)
		if result != "" {
			t.Error("should be empty when all texts are empty")
		}
	})
}

func TestBuildBootstrapWarningsSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildBootstrapWarningsSection(nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("with_warnings", func(t *testing.T) {
		result := BuildBootstrapWarningsSection([]string{"warning 1", "warning 2"})
		if !strings.Contains(result, "Bootstrap Warnings") {
			t.Error("should contain header")
		}
		if !strings.Contains(result, "- warning 1") {
			t.Error("should contain warning")
		}
	})

	t.Run("blank_lines_excluded", func(t *testing.T) {
		result := BuildBootstrapWarningsSection([]string{"", "  ", "real warning"})
		if !strings.Contains(result, "real warning") {
			t.Error("should contain real warning")
		}
	})
}

func TestBuildContextFilesPromptSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildContextFilesPromptSection(nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("with_files", func(t *testing.T) {
		files := []PromptContextFile{
			{Path: "README.md", Title: "README", Content: "Hello World"},
		}
		result := BuildContextFilesPromptSection(files)
		if !strings.Contains(result, "Context Files") {
			t.Error("should contain header")
		}
		if !strings.Contains(result, "README") {
			t.Error("should contain title")
		}
		if !strings.Contains(result, "Hello World") {
			t.Error("should contain content")
		}
	})

	t.Run("truncated_marker", func(t *testing.T) {
		files := []PromptContextFile{
			{Path: "big.md", Title: "Big File", Content: "content", Truncated: true},
		}
		result := BuildContextFilesPromptSection(files)
		if !strings.Contains(result, "[truncated]") {
			t.Error("should have truncated marker")
		}
	})

	t.Run("skips_empty_content", func(t *testing.T) {
		files := []PromptContextFile{
			{Path: "empty.md", Title: "Empty", Content: ""},
		}
		result := BuildContextFilesPromptSection(files)
		if result != "" {
			t.Error("should be empty when all content is empty")
		}
	})
}

func TestBuildIdentityPromptSection(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		result := BuildIdentityPromptSection(core.AgentIdentity{
			ID:    "bot",
			Name:  "MyBot",
			Emoji: "🤖",
			Theme: "dark",
		})
		if !strings.Contains(result, "Agent name: MyBot") {
			t.Error("should contain name")
		}
		if !strings.Contains(result, "Agent emoji: 🤖") {
			t.Error("should contain emoji")
		}
	})

	t.Run("name_equals_id", func(t *testing.T) {
		result := BuildIdentityPromptSection(core.AgentIdentity{
			ID:   "bot",
			Name: "bot",
		})
		if strings.Contains(result, "Agent name") {
			t.Error("should not include name when it equals ID")
		}
	})

	t.Run("empty", func(t *testing.T) {
		if BuildIdentityPromptSection(core.AgentIdentity{}) != "" {
			t.Error("expected empty for empty identity")
		}
	})
}

func TestBuildToolPromptSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildToolPromptSection(nil, nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("with_tools", func(t *testing.T) {
		tools := []PromptTool{
			&mockTool{name: "shell", desc: "Run commands"},
			&mockTool{name: "read_file", desc: "Read files"},
		}
		result := BuildToolPromptSection(tools, nil)
		if !strings.Contains(result, "## Tooling") {
			t.Error("should have header")
		}
		if !strings.Contains(result, "shell: Run commands") {
			t.Error("should list tool with description")
		}
		if !strings.Contains(result, "Tool names are case-sensitive") {
			t.Error("should contain case-sensitive guidance")
		}
	})

	t.Run("nil_tool_skipped", func(t *testing.T) {
		tools := []PromptTool{nil, &mockTool{name: "shell", desc: "Run"}}
		result := BuildToolPromptSection(tools, nil)
		if !strings.Contains(result, "shell") {
			t.Error("should contain non-nil tool")
		}
	})

	t.Run("empty_name_skipped", func(t *testing.T) {
		tools := []PromptTool{&mockTool{name: "", desc: "No name"}}
		result := BuildToolPromptSection(tools, nil)
		if result != "" {
			t.Error("tool with empty name should be skipped")
		}
	})

	t.Run("no_description", func(t *testing.T) {
		tools := []PromptTool{&mockTool{name: "basic", desc: ""}}
		result := BuildToolPromptSection(tools, nil)
		if !strings.Contains(result, "- basic") {
			t.Error("should list tool without description")
		}
	})

	t.Run("external_summary", func(t *testing.T) {
		tools := []PromptTool{&mockTool{name: "custom_tool", desc: "Local desc"}}
		result := BuildToolPromptSection(tools, map[string]string{"custom_tool": "External summary"})
		if !strings.Contains(result, "custom_tool: External summary") {
			t.Error("should prefer external summary")
		}
	})

	t.Run("kocort_core_order", func(t *testing.T) {
		tools := []PromptTool{
			&mockTool{name: "subagents", desc: "Subagents"},
			&mockTool{name: "sessions_spawn", desc: "Spawn"},
			&mockTool{name: "canvas", desc: "Canvas"},
			&mockTool{name: "nodes", desc: "Nodes"},
		}
		result := BuildToolPromptSection(tools, nil)
		canvasIdx := strings.Index(result, "canvas: Present/eval/snapshot the Canvas")
		nodesIdx := strings.Index(result, "nodes: List/describe/notify/camera/screen on paired nodes")
		spawnIdx := strings.Index(result, "sessions_spawn: Spawn an isolated sub-agent session")
		subagentsIdx := strings.Index(result, "subagents: List, steer, or kill sub-agent runs for this requester session")
		if canvasIdx < 0 || nodesIdx < 0 || spawnIdx < 0 || subagentsIdx < 0 {
			t.Fatalf("expected summaries in result, got %q", result)
		}
		// Canonical section order: Sessions → UI → Agents
		// sessions_spawn < subagents < canvas < nodes
		if !(spawnIdx < subagentsIdx && subagentsIdx < canvasIdx && canvasIdx < nodesIdx) {
			t.Fatalf("expected tool ordering (sessions→UI→agents), got %q", result)
		}
	})
}

func TestBuildToolCallStylePromptSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildToolCallStylePromptSection(nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("with_tools", func(t *testing.T) {
		result := BuildToolCallStylePromptSection([]PromptTool{&mockTool{name: "exec", desc: "Run commands"}})
		if !strings.Contains(result, "## Tool Call Style") {
			t.Error("should have header")
		}
		if !strings.Contains(result, "do not narrate routine, low-risk tool calls") {
			t.Error("should contain narration guidance")
		}
	})
}

func TestBuildMemoryRecallPromptSection(t *testing.T) {
	t.Run("no_memory_tools", func(t *testing.T) {
		if BuildMemoryRecallPromptSection([]PromptTool{&mockTool{name: "exec", desc: "Run"}}, "") != "" {
			t.Error("expected empty")
		}
	})

	t.Run("with_memory_tools", func(t *testing.T) {
		result := BuildMemoryRecallPromptSection([]PromptTool{&mockTool{name: "memory_search", desc: "Search"}}, "auto")
		if !strings.Contains(result, "## Memory Recall") {
			t.Error("should have header")
		}
		if !strings.Contains(result, "Before answering anything about prior work") {
			t.Error("should contain recall rule")
		}
		if !strings.Contains(result, "Source: <path#line>") {
			t.Error("should contain citation guidance")
		}
	})

	t.Run("citations_off", func(t *testing.T) {
		result := BuildMemoryRecallPromptSection([]PromptTool{&mockTool{name: "memory_get", desc: "Get"}}, "off")
		if !strings.Contains(result, "Citations are disabled") {
			t.Error("should mention citations off")
		}
	})
}

func TestBuildMessagingPromptSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildMessagingPromptSection(nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("with_session_tools", func(t *testing.T) {
		result := BuildMessagingPromptSection([]PromptTool{
			&mockTool{name: "sessions_send", desc: "Send"},
			&mockTool{name: "subagents", desc: "Manage"},
		})
		if !strings.Contains(result, "## Messaging") {
			t.Error("should have header")
		}
		if !strings.Contains(result, "Cross-session messaging") {
			t.Error("should mention sessions_send")
		}
		if !strings.Contains(result, "Never use exec/curl for provider messaging") {
			t.Error("should contain routing guidance")
		}
	})
}

func TestBuildToolGuidanceSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildToolGuidanceSection(nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("memory_tools", func(t *testing.T) {
		tools := []PromptTool{&mockTool{name: "memory_search", desc: "Search"}}
		result := BuildToolGuidanceSection(tools)
		if !strings.Contains(result, "memory") {
			t.Error("should contain memory guidance")
		}
		if !strings.Contains(result, "explicitly asks you to remember something") {
			t.Error("should contain durable memory write guidance")
		}
		if !strings.Contains(result, "Do not claim you will remember later") {
			t.Error("should contain anti-false-memory guidance")
		}
	})

	t.Run("cron_tool", func(t *testing.T) {
		tools := []PromptTool{&mockTool{name: "cron", desc: "Schedule"}}
		result := BuildToolGuidanceSection(tools)
		if !strings.Contains(result, "reminder") {
			t.Error("should contain cron guidance")
		}
	})

	t.Run("session_tools", func(t *testing.T) {
		tools := []PromptTool{&mockTool{name: "sessions_list", desc: "List"}}
		result := BuildToolGuidanceSection(tools)
		if !strings.Contains(result, "session") {
			t.Error("should contain session guidance")
		}
		if !strings.Contains(result, "Use sessions_list to discover candidate sessions") {
			t.Error("should contain explicit sessions_list guidance")
		}
	})

	t.Run("subagent_tools", func(t *testing.T) {
		tools := []PromptTool{&mockTool{name: "sessions_spawn", desc: "Spawn"}}
		result := BuildToolGuidanceSection(tools)
		if !strings.Contains(result, "Subagent") {
			t.Error("should contain subagent guidance")
		}
	})
}

func TestBuildMemoryPromptSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildMemoryPromptSection(nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("with_hits", func(t *testing.T) {
		hits := []core.MemoryHit{
			{Source: "notes.md", FromLine: 1, ToLine: 5, Snippet: "Important note"},
		}
		result := BuildMemoryPromptSection(hits)
		if !strings.Contains(result, "Recalled memory") {
			t.Error("should have header")
		}
		if !strings.Contains(result, "Important note") {
			t.Error("should contain snippet")
		}
	})

	t.Run("with_citations_off", func(t *testing.T) {
		hits := []core.MemoryHit{
			{Source: "notes.md", FromLine: 1, ToLine: 5, Snippet: "note"},
		}
		result := BuildMemoryPromptSectionWithCitations(hits, "off")
		if strings.Contains(result, "notes.md") {
			t.Error("citations off should not include source")
		}
		if !strings.Contains(result, "- note") {
			t.Error("should contain snippet")
		}
	})
}

func TestBuildTranscriptPromptSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		result := BuildTranscriptPromptSection(nil)
		if !strings.Contains(result, "Recent conversation") {
			t.Error("should have header even with empty history")
		}
	})

	t.Run("caps_at_6", func(t *testing.T) {
		var history []core.TranscriptMessage
		for i := 0; i < 10; i++ {
			history = append(history, core.TranscriptMessage{Role: "user", Text: "msg"})
		}
		result := BuildTranscriptPromptSection(history)
		count := strings.Count(result, "- user:")
		if count > 6 {
			t.Errorf("expected max 6 entries, got %d", count)
		}
	})
}

func TestFormatTranscriptPromptLine(t *testing.T) {
	tests := []struct {
		name string
		msg  core.TranscriptMessage
		want string
	}{
		{"user", core.TranscriptMessage{Role: "user", Text: "hello"}, "- user: hello"},
		{"assistant", core.TranscriptMessage{Role: "assistant", Text: "hi"}, "- assistant: hi"},
		{"empty_text", core.TranscriptMessage{Role: "user", Text: ""}, ""},
		{"tool_call", core.TranscriptMessage{Type: "tool_call", ToolName: "shell"}, "- assistant called tool shell"},
		{"tool_result", core.TranscriptMessage{Type: "tool_result", ToolName: "shell", Text: "output"}, "- tool shell result: output"},
		{"compaction_summary", core.TranscriptMessage{Type: "compaction", Summary: "summary text"}, "- compaction summary: summary text"},
		{"system_event", core.TranscriptMessage{Type: "system_event", Text: "event"}, "- system: event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatTranscriptPromptLine(tt.msg)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectInternalPromptEvents(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if SelectInternalPromptEvents(nil) != nil {
			t.Error("expected nil")
		}
	})

	t.Run("filters_non_system", func(t *testing.T) {
		history := []core.TranscriptMessage{
			{Type: "user", Text: "hello"},
			{Type: "system", Text: "boot"},
			{Type: "assistant", Text: "response"},
			{Type: "internal", Event: "config", Text: "loaded"},
		}
		events := SelectInternalPromptEvents(history)
		if len(events) != 2 {
			t.Errorf("expected 2 events, got %d", len(events))
		}
	})

	t.Run("caps_at_4", func(t *testing.T) {
		var history []core.TranscriptMessage
		for i := 0; i < 10; i++ {
			history = append(history, core.TranscriptMessage{Type: "system", Text: "event"})
		}
		events := SelectInternalPromptEvents(history)
		if len(events) != 4 {
			t.Errorf("expected max 4, got %d", len(events))
		}
	})
}

func TestLoadPromptContextFiles(t *testing.T) {
	t.Run("empty_workspace", func(t *testing.T) {
		files, warnings := loadPromptContextFiles("", core.ChatTypeDirect, false)
		if files != nil || warnings != nil {
			t.Error("empty workspace should return nil")
		}
	})

	t.Run("loads_agents_and_readme", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents info"), 0o644)
		os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme info"), 0o644)
		files, _ := loadPromptContextFiles(dir, core.ChatTypeDirect, false)
		if len(files) < 2 {
			t.Errorf("expected at least 2 files, got %d", len(files))
		}
	})

	t.Run("loads_long_term_memory_files", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("durable memory"), 0o644)
		os.WriteFile(filepath.Join(dir, "memory.md"), []byte("fallback memory"), 0o644)
		files, _ := loadPromptContextFiles(dir, core.ChatTypeDirect, false)
		foundMemory := false
		foundAltMemory := false
		for _, f := range files {
			if f.Path == "MEMORY.md" {
				foundMemory = true
			}
			if f.Path == "memory.md" {
				foundAltMemory = true
			}
		}
		if !foundMemory || !foundAltMemory {
			t.Fatalf("expected both MEMORY.md and memory.md injected, got %+v", files)
		}
	})

	t.Run("group_context_excludes_long_term_memory_files", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("durable memory"), 0o644)
		os.WriteFile(filepath.Join(dir, "memory.md"), []byte("fallback memory"), 0o644)
		files, _ := loadPromptContextFiles(dir, core.ChatTypeGroup, false)
		for _, f := range files {
			if f.Path == "MEMORY.md" || f.Path == "memory.md" {
				t.Fatalf("expected long-term memory excluded in group context, got %+v", files)
			}
		}
	})

	t.Run("heartbeat_included", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "HEARTBEAT.md"), []byte("heartbeat"), 0o644)
		files, _ := loadPromptContextFiles(dir, core.ChatTypeDirect, true)
		found := false
		for _, f := range files {
			if f.Path == "HEARTBEAT.md" {
				found = true
			}
		}
		if !found {
			t.Error("HEARTBEAT.md should be included when includeHeartbeat=true")
		}
	})

	t.Run("heartbeat_excluded", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "HEARTBEAT.md"), []byte("heartbeat"), 0o644)
		files, _ := loadPromptContextFiles(dir, core.ChatTypeDirect, false)
		for _, f := range files {
			if f.Path == "HEARTBEAT.md" {
				t.Error("HEARTBEAT.md should not be included when includeHeartbeat=false")
			}
		}
	})
}

func TestBuildSkillsPromptSection(t *testing.T) {
	t.Run("nil_snapshot", func(t *testing.T) {
		if BuildSkillsPromptSection(nil, nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("empty_prompt", func(t *testing.T) {
		if BuildSkillsPromptSection(&core.SkillSnapshot{}, nil) != "" {
			t.Error("expected empty for empty prompt")
		}
	})

	t.Run("with_prompt", func(t *testing.T) {
		snapshot := &core.SkillSnapshot{
			Prompt: "<available_skills>\n<skill>test</skill>\n</available_skills>",
			Commands: []core.SkillCommandSpec{
				{Name: "code", SkillName: "coding", Description: "Write code"},
			},
		}
		result := BuildSkillsPromptSection(snapshot, []PromptTool{stubPromptTool{name: "read"}})
		if !strings.Contains(result, "Skills (mandatory)") {
			t.Error("should have header")
		}
		if !strings.Contains(result, "with `read`") {
			t.Error("should mention read tool when available")
		}
		if !strings.Contains(result, "/code -> coding: Write code") {
			t.Error("should list commands")
		}
		if !strings.Contains(result, "respect 429/Retry-After") {
			t.Error("should contain rate limit guidance")
		}
	})
}

func TestBuildSystemPromptIncludesSections(t *testing.T) {
	result := BuildSystemPrompt(PromptBuildParams{
		Identity: core.AgentIdentity{
			ID:                  "bot",
			MemoryCitationsMode: "auto",
		},
		Tools: []PromptTool{
			&mockTool{name: "memory_search", desc: "Search memory"},
			&mockTool{name: "sessions_send", desc: "Send to session"},
			&mockTool{name: "subagents", desc: "Manage subagents"},
		},
	})
	for _, needle := range []string{
		"## Tooling",
		"## Tool Call Style",
		"## Safety",
		"## Memory Recall",
		"## Reply Tags",
		"## Messaging",
		"## Tool Guidance",
		"TOOLS.md does not control tool availability",
	} {
		if !strings.Contains(result, needle) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", needle, result)
		}
	}
}

func TestBuildDocumentationPromptSection(t *testing.T) {
	result := BuildDocumentationPromptSection("docs")
	if !strings.Contains(result, "## Documentation") {
		t.Fatal("expected documentation header")
	}
	if !strings.Contains(result, "Local docs: docs") {
		t.Fatal("expected docs path")
	}
}

func TestBuildSandboxPromptSection(t *testing.T) {
	result := BuildSandboxPromptSection(PromptSandboxInfo{
		Enabled:          true,
		Mode:             "all",
		WorkspaceAccess:  "ro",
		DefaultWorkdir:   "/repo",
		SandboxWorkspace: "/tmp/sandbox",
		AgentWorkspace:   "/repo",
		Scope:            "agent",
	})
	for _, needle := range []string{"## Sandbox", "Sandbox mode: all", "Workspace access: ro", "Sandbox scope: agent", "Default working directory: /repo", "Sandbox workspace: /tmp/sandbox"} {
		if !strings.Contains(result, needle) {
			t.Fatalf("expected sandbox prompt to contain %q, got %q", needle, result)
		}
	}
}

func TestBuildReplyTagsPromptSection(t *testing.T) {
	result := BuildReplyTagsPromptSection()
	if !strings.Contains(result, "## Reply Tags") {
		t.Fatal("expected reply tags header")
	}
	if !strings.Contains(result, "[[reply_to_current]]") {
		t.Fatal("expected reply tag guidance")
	}
}

func TestBuildAttachmentPromptSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if BuildAttachmentPromptSection(nil) != "" {
			t.Error("expected empty")
		}
	})

	t.Run("image_attachment", func(t *testing.T) {
		attachments := []core.Attachment{
			{Name: "photo.png", MIMEType: "image/png", Content: []byte{1}},
		}
		result := BuildAttachmentPromptSection(attachments)
		if !strings.Contains(result, "Attachments:") {
			t.Error("should have header")
		}
		if !strings.Contains(result, "image") {
			t.Error("should identify as image")
		}
	})

	t.Run("text_attachment", func(t *testing.T) {
		attachments := []core.Attachment{
			{Name: "code.go", MIMEType: "text/plain", Content: []byte("package main")},
		}
		result := BuildAttachmentPromptSection(attachments)
		if !strings.Contains(result, "text") {
			t.Error("should identify as text")
		}
	})
}

func TestParseSkillFrontmatter(t *testing.T) {
	t.Run("with_frontmatter", func(t *testing.T) {
		content := "---\nname: test\ndescription: A test skill\n---\nBody content here"
		fm, body := parseSkillFrontmatter(content)
		if fm["name"] != "test" {
			t.Errorf("name = %q", fm["name"])
		}
		if !strings.Contains(body, "Body content here") {
			t.Error("body should contain content after frontmatter")
		}
	})

	t.Run("no_frontmatter", func(t *testing.T) {
		content := "Just body content"
		fm, body := parseSkillFrontmatter(content)
		if len(fm) != 0 {
			t.Error("should have empty frontmatter")
		}
		if body != content {
			t.Error("body should be entire content")
		}
	})
}

func TestSanitizeSkillCommandName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Code Review", "code_review"},
		{"", "skill"},
		{"a/b/c", "a_b_c"},
		{"test_skill", "test_skill"},
		{"   spaces   ", "spaces"},
		{"---dashes---", "dashes"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeSkillCommandName(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripTimestampPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[Mon 2024-01-01 10:00 UTC] hello", "hello"},
		{"no timestamp", "no timestamp"},
		{"[partial", "[partial"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripTimestampPrefix(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPromptNonEmpty(t *testing.T) {
	if promptNonEmpty("value", "fallback") != "value" {
		t.Error("should return primary when non-empty")
	}
	if promptNonEmpty("", "fallback") != "fallback" {
		t.Error("should return fallback when empty")
	}
	if promptNonEmpty("  ", "fallback") != "fallback" {
		t.Error("should return fallback when whitespace")
	}
}

// Mock implementation of PromptTool for testing
type mockTool struct {
	name string
	desc string
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string { return m.desc }
