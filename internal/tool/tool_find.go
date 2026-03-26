package tool

import (
	"context"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// FindTool — find files by glob pattern.
//
// When fd is available (bin/tools/ or PATH) it is used for speed and
// automatic .gitignore support.  Otherwise a pure-Go fallback runs with
// doublestar (**) support.
// ---------------------------------------------------------------------------

type FindTool struct{}

func NewFindTool() *FindTool { return &FindTool{} }

func (t *FindTool) Name() string { return "find" }

func (t *FindTool) Description() string {
	return "Find files by glob pattern. Respects .gitignore when fd is available."
}

func (t *FindTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":       map[string]any{"type": "string", "description": "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'."},
				"path":          map[string]any{"type": "string", "description": "Directory to search in (default: working directory)."},
				"includeHidden": map[string]any{"type": "boolean", "description": "Include hidden files and directories."},
				"limit":         map[string]any{"type": "number", "description": "Maximum number of results (default: 1000)."},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		},
	}
}

const defaultFindResultLimit = 1000

func (t *FindTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	// Accept both "pattern" and legacy "glob".
	pattern, _ := ReadStringParam(args, "pattern", false)
	if pattern == "" {
		pattern, _ = ReadStringParam(args, "glob", false)
	}
	if strings.TrimSpace(pattern) == "" {
		return core.ToolResult{}, ToolInputError{Message: "pattern is required"}
	}
	rootArg, _ := ReadStringParam(args, "path", false)
	if strings.TrimSpace(rootArg) == "" {
		rootArg = "."
	}
	includeHidden, _ := ReadBoolParam(args, "includeHidden")
	limit, _ := ReadOptionalIntParam(args, "limit")
	if limit <= 0 {
		limit = defaultFindResultLimit
	}

	_, relRoot, absRoot, err := resolveWorkspaceToolPath(toolCtx, rootArg)
	if err != nil {
		return core.ToolResult{}, err
	}

	var results []string
	var limitHit bool
	if fdPath := ResolveToolBin("fd"); fdPath != "" {
		results, limitHit, err = findWithFd(ctx, fdPath, pattern, absRoot, includeHidden, limit)
		if err != nil {
			return core.ToolResult{}, err
		}
	} else {
		results, limitHit, err = findFallback(pattern, absRoot, includeHidden, limit)
		if err != nil {
			return core.ToolResult{}, err
		}
	}

	// Prefix results with relRoot for display.
	display := make([]string, len(results))
	for i, r := range results {
		display[i] = filepath.ToSlash(filepath.Join(relRoot, r))
	}

	// Apply output byte truncation.
	joined := strings.Join(display, "\n")
	tr := truncateOutputHead(joined, 0, defaultMaxOutputBytes)

	resp := map[string]any{
		"path":    relRoot,
		"pattern": pattern,
		"matches": display,
		"count":   len(display),
	}
	if limitHit || tr.Truncated {
		resp["limitReached"] = true
		if tr.Truncated {
			lines := strings.Split(tr.Content, "\n")
			resp["matches"] = lines
			resp["count"] = len(lines)
		}
	}
	return JSONResult(resp)
}

// ── fd path ──────────────────────────────────────────────────────────────────

func findWithFd(ctx context.Context, fdPath, pattern, absRoot string, includeHidden bool, limit int) ([]string, bool, error) {
	args := []string{"--glob", "--color=never"}
	if includeHidden {
		args = append(args, "--hidden")
	}
	args = append(args, "--max-results", strconv.Itoa(limit))
	args = append(args, pattern, absRoot)

	cmd := exec.CommandContext(ctx, fdPath, args...)
	out, err := cmd.Output()
	if err != nil {
		// fd exits 1 when no matches found.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, false, nil
		}
		return nil, false, err
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, false, nil
	}
	var results []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rel, relErr := filepath.Rel(absRoot, line)
		if relErr != nil {
			rel = line
		}
		results = append(results, filepath.ToSlash(rel))
	}
	return results, len(results) >= limit, nil
}

// ── pure-Go fallback ─────────────────────────────────────────────────────────

func findFallback(pattern, absRoot string, includeHidden bool, limit int) ([]string, bool, error) {
	var results []string
	limitHit := false

	err := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if path != absRoot && defaultIgnoreDirs[name] {
				return filepath.SkipDir
			}
			if !includeHidden && strings.HasPrefix(name, ".") && path != absRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if !includeHidden && strings.HasPrefix(name, ".") {
			return nil
		}
		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		norm := filepath.ToSlash(rel)
		if !matchGlobPattern(pattern, norm) {
			return nil
		}
		results = append(results, norm)
		if len(results) >= limit {
			limitHit = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, false, err
	}
	return results, limitHit, nil
}
