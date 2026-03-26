package tool

import (
	"context"
	"os"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// EditTool — precise text replacement with fuzzy matching, BOM handling, and
// line-ending preservation.
//
// Matches pi-coding-agent edit.ts + edit-diff.ts behaviour:
//  1. Tries exact match first.
//  2. Falls back to fuzzy match (trailing whitespace + Unicode normalisation).
//  3. Preserves UTF-8 BOM if present.
//  4. Preserves original line endings (CRLF / LF).
// ---------------------------------------------------------------------------

type EditTool struct{}

func NewEditTool() *EditTool { return &EditTool{} }

func (t *EditTool) Name() string { return "edit" }

func (t *EditTool) Description() string {
	return "Make precise edits to files. Supports fuzzy matching for trailing whitespace and smart-quote differences."
}

func (t *EditTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "File to edit (relative or absolute)."},
				"oldText":    map[string]any{"type": "string", "description": "Exact text to find and replace (must match exactly or via fuzzy normalisation)."},
				"newText":    map[string]any{"type": "string", "description": "Replacement text."},
				"replaceAll": map[string]any{"type": "boolean", "description": "Replace all matches instead of exactly one."},
			},
			"required":             []string{"path", "oldText", "newText"},
			"additionalProperties": false,
		},
	}
}

func (t *EditTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	pathArg, err := ReadStringParam(args, "path", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	oldText, err := ReadStringParam(args, "oldText", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	newText, _ := ReadStringParam(args, "newText", false) // allow empty (deletion)
	replaceAll, _ := ReadBoolParam(args, "replaceAll")

	_, relPath, absPath, err := resolveWorkspaceToolPath(toolCtx, pathArg)
	if err != nil {
		return core.ToolResult{}, err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return core.ToolResult{}, err
	}
	rawContent := string(data)

	// ── BOM handling ─────────────────────────────────────────────────────
	bom, content := stripBom(rawContent)

	// ── Line-ending normalisation ────────────────────────────────────────
	originalEnding := detectLineEnding(content)
	normalizedContent := normalizeToLF(content)
	normalizedOld := normalizeToLF(oldText)
	normalizedNew := normalizeToLF(newText)

	// ── Fuzzy matching (exact first, then normalised) ────────────────────
	match := fuzzyFindText(normalizedContent, normalizedOld)
	if !match.Found {
		return core.ToolResult{}, ToolInputError{Message: "oldText not found"}
	}

	// Count occurrences (on the same "matching content").
	var occurrences int
	if match.UsedFuzzyMatch {
		fuzzyOld := normalizeForFuzzyMatch(normalizedOld)
		occurrences = strings.Count(match.ContentForReplacement, fuzzyOld)
	} else {
		occurrences = strings.Count(normalizedContent, normalizedOld)
	}

	if !replaceAll && occurrences > 1 {
		return core.ToolResult{}, ToolInputError{
			Message: "oldText matched multiple locations; pass replaceAll=true or provide more specific text",
		}
	}

	// ── Perform replacement ──────────────────────────────────────────────
	baseContent := match.ContentForReplacement
	var updated string
	applied := 1

	if match.UsedFuzzyMatch {
		fuzzyOld := normalizeForFuzzyMatch(normalizedOld)
		if replaceAll {
			applied = occurrences
			updated = strings.ReplaceAll(baseContent, fuzzyOld, normalizedNew)
		} else {
			updated = baseContent[:match.Index] + normalizedNew + baseContent[match.Index+match.MatchLength:]
		}
	} else {
		if replaceAll {
			applied = occurrences
			updated = strings.ReplaceAll(baseContent, normalizedOld, normalizedNew)
		} else {
			updated = baseContent[:match.Index] + normalizedNew + baseContent[match.Index+match.MatchLength:]
		}
	}

	// ── Restore BOM + original line endings ──────────────────────────────
	finalContent := bom + restoreLineEndings(updated, originalEnding)

	if err := os.WriteFile(absPath, []byte(finalContent), 0o644); err != nil {
		return core.ToolResult{}, err
	}

	resp := map[string]any{
		"status":       "ok",
		"path":         relPath,
		"replacements": applied,
	}
	if match.UsedFuzzyMatch {
		resp["fuzzyMatch"] = true
	}
	return JSONResult(resp)
}
