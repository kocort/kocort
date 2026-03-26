package tool

// ---------------------------------------------------------------------------

//
// Defines tool sections (categories), core tool summaries, and canonical

// ---------------------------------------------------------------------------

// ToolSection defines a logical grouping for tools in the prompt.
type ToolSection struct {
	ID    string
	Label string
	Order int
}

// CoreToolSections defines the standard tool category ordering.
var CoreToolSections = []ToolSection{
	{ID: "fs", Label: "Files", Order: 0},
	{ID: "runtime", Label: "Runtime", Order: 1},
	{ID: "web", Label: "Web", Order: 2},
	{ID: "memory", Label: "Memory", Order: 3},
	{ID: "sessions", Label: "Sessions", Order: 4},
	{ID: "ui", Label: "UI", Order: 5},
	{ID: "messaging", Label: "Messaging", Order: 6},
	{ID: "automation", Label: "Automation", Order: 7},
	{ID: "agents", Label: "Agents", Order: 8},
	{ID: "media", Label: "Media", Order: 9},
}

// CoreToolSectionMap maps tool names to their section ID.
var CoreToolSectionMap = map[string]string{
	"read":             "fs",
	"write":            "fs",
	"edit":             "fs",
	"apply_patch":      "fs",
	"grep":             "fs",
	"find":             "fs",
	"ls":               "fs",
	"exec":             "runtime",
	"process":          "runtime",
	"web_search":       "web",
	"web_fetch":        "web",
	"browser":          "web",
	"memory_search":    "memory",
	"memory_get":       "memory",
	"sessions_spawn":   "sessions",
	"sessions_list":    "sessions",
	"sessions_history": "sessions",
	"sessions_send":    "sessions",
	"sessions_yield":   "sessions",
	"subagents":        "sessions",
	"session_status":   "sessions",
	"canvas":           "ui",
	"message":          "messaging",
	"cron":             "automation",
	"gateway":          "automation",
	"agents_list":      "agents",
	"nodes":            "agents",
	"image":            "media",
	"image_generate":   "media",
}

// CoreToolSummaries provides concise one-line descriptions for core tools.

var CoreToolSummaries = map[string]string{
	"read":             "Read file contents",
	"write":            "Create or overwrite files",
	"edit":             "Make precise edits to files",
	"apply_patch":      "Apply multi-file patches",
	"grep":             "Search file contents for patterns",
	"find":             "Find files by glob pattern",
	"ls":               "List directory contents",
	"exec":             "Run shell commands (pty available for TTY-required CLIs)",
	"process":          "Manage background exec sessions",
	"web_search":       "Search the web (Brave API)",
	"web_fetch":        "Fetch and extract readable content from a URL",
	"browser":          "Control web browser",
	"canvas":           "Present/eval/snapshot the Canvas",
	"nodes":            "List/describe/notify/camera/screen on paired nodes",
	"cron":             "Manage cron jobs and wake events",
	"message":          "Send messages and channel actions",
	"gateway":          "Restart, apply config, or run updates on the running process",
	"agents_list":      "List agent ids allowed for sessions_spawn",
	"sessions_list":    "List other sessions (incl. sub-agents) with filters/last",
	"sessions_history": "Fetch history for another session/sub-agent",
	"sessions_send":    "Send a message to another session/sub-agent",
	"sessions_spawn":   "Spawn an isolated sub-agent session",
	"sessions_yield":   "End your current turn to receive sub-agent results",
	"subagents":        "List, steer, or kill sub-agent runs for this requester session",
	"session_status":   "Show status card (usage + time + Reasoning/Verbose/Elevated)",
	"memory_search":    "Search durable workspace memory",
	"memory_get":       "Read a specific memory file or line range",
	"image":            "Analyze an image with the configured image model",
	"image_generate":   "Generate images",
}

// CoreToolOrder defines the canonical display order for tools in prompts.
// Core tools appear in this order; non-core tools are appended alphabetically.
var CoreToolOrder = []string{
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
	"sessions_send", "sessions_yield", "subagents", "session_status",
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

// IsKnownCoreTool returns true if the name is a recognized core tool.
func IsKnownCoreTool(name string) bool {
	_, ok := CoreToolSummaries[name]
	return ok
}
