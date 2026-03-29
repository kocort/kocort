package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// GrepTool — search file contents for patterns.
//
// When ripgrep (rg) is available (bin/tools/ or PATH) it is used for speed
// and automatic .gitignore support.  Otherwise a pure-Go fallback runs.
// ---------------------------------------------------------------------------

type GrepTool struct{}

func NewGrepTool() *GrepTool { return &GrepTool{} }

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return "Search file contents for patterns. Respects .gitignore when ripgrep is available."
}

func (t *GrepTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":       map[string]any{"type": "string", "description": "Search pattern (regex or literal string)."},
				"path":          map[string]any{"type": "string", "description": "Directory or file to search (default: working directory)."},
				"glob":          map[string]any{"type": "string", "description": "Filter files by glob, e.g. '*.ts' or '**/*.spec.ts'."},
				"includeHidden": map[string]any{"type": "boolean", "description": "Include hidden files and directories."},
				"ignoreCase":    map[string]any{"type": "boolean", "description": "Case-insensitive search."},
				"literal":       map[string]any{"type": "boolean", "description": "Treat pattern as a literal string instead of regex."},
				"context":       map[string]any{"type": "number", "description": "Number of context lines before and after each match."},
				"limit":         map[string]any{"type": "number", "description": "Maximum number of matches to return (default: 100)."},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		},
	}
}

// ── options ──────────────────────────────────────────────────────────────────

const defaultGrepMatchLimit = 100

type grepOpts struct {
	pattern       string
	absRoot       string
	relRoot       string
	glob          string
	includeHidden bool
	ignoreCase    bool
	literal       bool
	contextLines  int
	limit         int
}

// ── raw match ────────────────────────────────────────────────────────────────

type grepRawMatch struct {
	absFile string
	line    int
	text    string
}

// ── Execute ──────────────────────────────────────────────────────────────────

func (t *GrepTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	pattern, err := ReadStringParam(args, "pattern", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	rootArg, _ := ReadStringParam(args, "path", false)
	if strings.TrimSpace(rootArg) == "" {
		rootArg = "."
	}
	glob, _ := ReadStringParam(args, "glob", false)
	includeHidden, _ := ReadBoolParam(args, "includeHidden")
	ignoreCase, _ := ReadBoolParam(args, "ignoreCase")
	literal, _ := ReadBoolParam(args, "literal")
	ctxLines, _ := ReadOptionalIntParam(args, "context")
	limit, _ := ReadOptionalIntParam(args, "limit")
	if limit <= 0 {
		limit = defaultGrepMatchLimit
	}

	_, relRoot, absRoot, err := resolveWorkspaceToolPath(toolCtx, rootArg)
	if err != nil {
		return core.ToolResult{}, err
	}

	opts := grepOpts{
		pattern: pattern, absRoot: absRoot, relRoot: relRoot,
		glob: glob, includeHidden: includeHidden,
		ignoreCase: ignoreCase, literal: literal,
		contextLines: ctxLines, limit: limit,
	}

	var matches []grepRawMatch
	var limitHit bool

	if rgPath := ResolveToolBin("rg"); rgPath != "" {
		matches, limitHit, err = grepWithRipgrep(ctx, rgPath, opts)
		if err != nil {
			matches, limitHit, err = grepFallback(opts)
			if err != nil {
				return core.ToolResult{}, err
			}
		}
	} else {
		matches, limitHit, err = grepFallback(opts)
		if err != nil {
			return core.ToolResult{}, err
		}
	}

	return grepBuildResult(opts, matches, limitHit)
}

// ── ripgrep path ─────────────────────────────────────────────────────────────

type rgJSONEvent struct {
	Type string          `json:"type"`
	Data rgJSONMatchData `json:"data"`
}
type rgJSONMatchData struct {
	Path       rgJSONTextField `json:"path"`
	LineNumber int             `json:"line_number"`
	Lines      rgJSONTextField `json:"lines"`
}
type rgJSONTextField struct {
	Text string `json:"text"`
}

func grepWithRipgrep(ctx context.Context, rgPath string, o grepOpts) ([]grepRawMatch, bool, error) {
	args := []string{"--json", "--line-number", "--color=never"}
	if o.includeHidden {
		args = append(args, "--hidden")
	}
	if o.ignoreCase {
		args = append(args, "--ignore-case")
	}
	if o.literal {
		args = append(args, "--fixed-strings")
	}
	if o.glob != "" {
		args = append(args, "--glob", o.glob)
	}
	args = append(args, "--", o.pattern, o.absRoot)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}

	var matches []grepRawMatch
	limitHit := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for sc.Scan() {
		if len(matches) >= o.limit {
			limitHit = true
			break
		}
		var ev rgJSONEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != "match" {
			continue
		}
		matches = append(matches, grepRawMatch{
			absFile: ev.Data.Path.Text,
			line:    ev.Data.LineNumber,
			text:    strings.TrimRight(ev.Data.Lines.Text, "\r\n"),
		})
	}
	// Kill the process early if we hit the limit (it may still be running).
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait() // rg exits 1 on no-match; ignore
	return matches, limitHit, nil
}

// ── pure-Go fallback ─────────────────────────────────────────────────────────

func grepFallback(o grepOpts) ([]grepRawMatch, bool, error) {
	pat := o.pattern
	if o.literal {
		pat = regexp.QuoteMeta(pat)
	}
	if o.ignoreCase {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, false, ToolInputError{Message: "invalid pattern: " + err.Error()}
	}

	var matches []grepRawMatch
	limitHit := false
	walkErr := filepath.WalkDir(o.absRoot, func(path string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			return wErr
		}
		name := d.Name()
		if d.IsDir() {
			if path != o.absRoot && defaultIgnoreDirs[name] {
				return filepath.SkipDir
			}
			if !o.includeHidden && strings.HasPrefix(name, ".") && path != o.absRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if !o.includeHidden && strings.HasPrefix(name, ".") {
			return nil
		}
		rel, err := filepath.Rel(o.absRoot, path)
		if err != nil {
			return nil
		}
		norm := filepath.ToSlash(rel)
		if o.glob != "" && !matchGlobPattern(o.glob, norm) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinaryContent(data) {
			return nil
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for idx, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			matches = append(matches, grepRawMatch{absFile: path, line: idx + 1, text: line})
			if len(matches) >= o.limit {
				limitHit = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != fs.SkipAll {
		return nil, false, walkErr
	}
	return matches, limitHit, nil
}

// ── result formatting ────────────────────────────────────────────────────────

func grepBuildResult(o grepOpts, raw []grepRawMatch, limitHit bool) (core.ToolResult, error) {
	if len(raw) == 0 {
		return JSONResult(map[string]any{
			"path": o.relRoot, "pattern": o.pattern,
			"matches": []any{}, "count": 0,
		})
	}

	type matchRow struct {
		Path          string   `json:"path"`
		Line          int      `json:"line"`
		Snippet       string   `json:"snippet"`
		ContextBefore []string `json:"contextBefore,omitempty"`
		ContextAfter  []string `json:"contextAfter,omitempty"`
	}

	// File line cache for reading context.
	fileCache := map[string][]string{}
	fileLines := func(absPath string) []string {
		if c, ok := fileCache[absPath]; ok {
			return c
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		fileCache[absPath] = lines
		return lines
	}

	rows := make([]matchRow, 0, len(raw))
	linesTruncated := false
	outBytes := 0

	for _, m := range raw {
		rel, err := filepath.Rel(o.absRoot, m.absFile)
		if err != nil {
			rel = m.absFile
		}
		display := filepath.ToSlash(filepath.Join(o.relRoot, rel))

		snippet, wasTrunc := truncateLineContent(m.text, defaultGrepMaxLineLength)
		if wasTrunc {
			linesTruncated = true
		}

		row := matchRow{Path: display, Line: m.line, Snippet: snippet}

		// Attach context lines when requested.
		if o.contextLines > 0 {
			fl := fileLines(m.absFile)
			if fl != nil {
				start := max(0, m.line-1-o.contextLines)
				end := min(len(fl), m.line+o.contextLines)
				for i := start; i < m.line-1 && i < len(fl); i++ {
					t, tr := truncateLineContent(fl[i], defaultGrepMaxLineLength)
					if tr {
						linesTruncated = true
					}
					row.ContextBefore = append(row.ContextBefore, t)
				}
				for i := m.line; i < end && i < len(fl); i++ {
					t, tr := truncateLineContent(fl[i], defaultGrepMaxLineLength)
					if tr {
						linesTruncated = true
					}
					row.ContextAfter = append(row.ContextAfter, t)
				}
			}
		}

		rows = append(rows, row)
		outBytes += len(display) + len(snippet) + 60
		if outBytes > defaultMaxOutputBytes {
			limitHit = true
			break
		}
	}

	resp := map[string]any{
		"path": o.relRoot, "pattern": o.pattern,
		"matches": rows, "count": len(rows),
	}
	if limitHit {
		resp["limitReached"] = true
	}
	if linesTruncated {
		resp["linesTruncated"] = true
	}
	return JSONResult(resp)
}
