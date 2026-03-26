package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

type ApplyPatchTool struct{}

func NewApplyPatchTool() *ApplyPatchTool { return &ApplyPatchTool{} }

func (t *ApplyPatchTool) Name() string { return "apply_patch" }

func (t *ApplyPatchTool) Description() string { return "Apply multi-file patches." }

func (t *ApplyPatchTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patch": map[string]any{"type": "string", "description": "Patch text in Codex apply_patch format."},
			},
			"required":             []string{"patch"},
			"additionalProperties": false,
		},
	}
}

func (t *ApplyPatchTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	patchText, err := ReadStringParam(args, "patch", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	if _, err := resolveToolWorkingDir(toolCtx); err != nil {
		return core.ToolResult{}, err
	}
	ops, err := parseCodexPatch(patchText)
	if err != nil {
		return core.ToolResult{}, err
	}
	changed := make([]string, 0, len(ops))
	for _, op := range ops {
		_, relPath, absPath, resolveErr := resolveWorkspaceToolPath(toolCtx, op.Path)
		if resolveErr != nil {
			return core.ToolResult{}, resolveErr
		}
		switch op.Kind {
		case "add":
			if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
				return core.ToolResult{}, err
			}
			if err := os.WriteFile(absPath, []byte(strings.Join(op.AddLines, "\n")), 0o644); err != nil {
				return core.ToolResult{}, err
			}
		case "delete":
			if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
				return core.ToolResult{}, err
			}
		case "update":
			data, err := os.ReadFile(absPath)
			if err != nil {
				return core.ToolResult{}, err
			}
			updated, err := applyUpdatePatch(string(data), op)
			if err != nil {
				return core.ToolResult{}, err
			}
			targetPath := absPath
			if op.MoveTo != "" {
				_, _, targetPath, err = resolveWorkspaceToolPath(toolCtx, op.MoveTo)
				if err != nil {
					return core.ToolResult{}, err
				}
				if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
					return core.ToolResult{}, err
				}
			}
			if err := os.WriteFile(targetPath, []byte(updated), 0o644); err != nil {
				return core.ToolResult{}, err
			}
			if op.MoveTo != "" && targetPath != absPath {
				if err := os.Remove(absPath); err != nil {
					return core.ToolResult{}, err
				}
				_, relPath, _, _ = resolveWorkspaceToolPath(toolCtx, op.MoveTo)
			}
		default:
			return core.ToolResult{}, fmt.Errorf("unsupported patch operation %q", op.Kind)
		}
		changed = append(changed, relPath)
	}
	return JSONResult(map[string]any{
		"status": "ok",
		"files":  changed,
		"count":  len(changed),
	})
}

type patchOp struct {
	Kind     string
	Path     string
	MoveTo   string
	AddLines []string
	Chunks   []patchChunk
}

type patchChunk struct {
	Lines []string
}

func parseCodexPatch(input string) ([]patchOp, error) {
	text := strings.ReplaceAll(input, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, ToolInputError{Message: "patch must start with *** Begin Patch"}
	}
	var ops []patchOp
	i := 1
	for i < len(lines) {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++
		case strings.TrimSpace(line) == "*** End Patch":
			return ops, nil
		case strings.HasPrefix(line, "*** Add File: "):
			op := patchOp{Kind: "add", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				if strings.HasPrefix(lines[i], "+") {
					op.AddLines = append(op.AddLines, strings.TrimPrefix(lines[i], "+"))
				}
				i++
			}
			ops = append(ops, op)
		case strings.HasPrefix(line, "*** Delete File: "):
			ops = append(ops, patchOp{Kind: "delete", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))})
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			op := patchOp{Kind: "update", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
			i++
			done := false
			for i < len(lines) {
				switch {
				case strings.HasPrefix(lines[i], "*** Move to: "):
					op.MoveTo = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to: "))
					i++
				case strings.HasPrefix(lines[i], "*** "):
					done = true
				default:
					chunk := patchChunk{}
					for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
						chunk.Lines = append(chunk.Lines, lines[i])
						i++
					}
					if len(chunk.Lines) > 0 {
						op.Chunks = append(op.Chunks, chunk)
					}
				}
				if done {
					break
				}
			}
			ops = append(ops, op)
		default:
			return nil, ToolInputError{Message: "unsupported patch line: " + line}
		}
	}
	return nil, ToolInputError{Message: "patch missing *** End Patch"}
}

func applyUpdatePatch(content string, op patchOp) (string, error) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := []string{}
	if normalized != "" {
		lines = strings.Split(normalized, "\n")
	}
	for _, chunk := range op.Chunks {
		linesUpdated, err := applyPatchChunk(lines, chunk)
		if err != nil {
			return "", err
		}
		lines = linesUpdated
	}
	return strings.Join(lines, "\n"), nil
}

func applyPatchChunk(lines []string, chunk patchChunk) ([]string, error) {
	oldSeq := make([]string, 0)
	newSeq := make([]string, 0)
	for _, line := range chunk.Lines {
		if strings.HasPrefix(line, "@@") {
			continue
		}
		if strings.HasPrefix(line, "*** End of File") {
			continue
		}
		if line == "" {
			oldSeq = append(oldSeq, "")
			newSeq = append(newSeq, "")
			continue
		}
		switch line[0] {
		case ' ':
			text := line[1:]
			oldSeq = append(oldSeq, text)
			newSeq = append(newSeq, text)
		case '-':
			oldSeq = append(oldSeq, line[1:])
		case '+':
			newSeq = append(newSeq, line[1:])
		default:
			return nil, ToolInputError{Message: "invalid patch line: " + line}
		}
	}
	idx := findSequence(lines, oldSeq)
	if idx < 0 {
		return nil, ToolInputError{Message: "patch context not found"}
	}
	out := append([]string{}, lines[:idx]...)
	out = append(out, newSeq...)
	out = append(out, lines[idx+len(oldSeq):]...)
	return out, nil
}

func findSequence(lines []string, seq []string) int {
	if len(seq) == 0 {
		return len(lines)
	}
	for i := 0; i+len(seq) <= len(lines); i++ {
		match := true
		for j := range seq {
			if lines[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
