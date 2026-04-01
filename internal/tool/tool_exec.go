package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/internal/infra"
)

const (
	defaultExecBackgroundMs = 10_000
	defaultExecTimeoutSec   = 1800
)

type ExecTool struct {
	defaults config.ToolExecConfig
}

func NewExecTool(configs ...*config.ToolExecConfig) *ExecTool {
	var cfg config.ToolExecConfig
	if len(configs) > 0 && configs[0] != nil {
		cfg = *configs[0]
	}
	return &ExecTool{defaults: cfg}
}

func (t *ExecTool) Name() string {
	return "exec"
}

func (t *ExecTool) Description() string {
	return "Run shell commands (pty available for TTY-required CLIs)."
}

func (t *ExecTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute.",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Working directory (defaults to current workspace).",
				},
				"env": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "Additional environment variables.",
				},
				"yieldMs": map[string]any{
					"type":        "number",
					"description": "Milliseconds to wait before backgrounding the process.",
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "Run in background immediately and return a process session.",
				},
				"timeout": map[string]any{
					"type":        "number",
					"description": "Timeout in seconds (optional, kills the process on expiry).",
				},
				"pty": map[string]any{
					"type":        "boolean",
					"description": "Run under a PTY for TTY-required commands.",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *ExecTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	command, err := ReadStringParam(args, "command", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	workdirArg, err := ReadStringParam(args, "workdir", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	envOverrides, err := ReadOptionalStringMapParam(args, "env")
	if err != nil {
		return core.ToolResult{}, err
	}
	yieldDelay, err := ReadOptionalPositiveDurationParam(args, "yieldMs", time.Millisecond)
	if err != nil {
		return core.ToolResult{}, err
	}
	background, err := ReadBoolParam(args, "background")
	if err != nil {
		return core.ToolResult{}, err
	}
	timeout, err := ReadOptionalPositiveDurationParam(args, "timeout", time.Second)
	if err != nil {
		return core.ToolResult{}, err
	}
	ptyEnabled, err := ReadBoolParam(args, "pty")
	if err != nil {
		return core.ToolResult{}, err
	}
	allowBackground := t.allowBackground()
	defaultBackgroundWindow := t.defaultBackgroundWindow()
	defaultTimeout := t.defaultTimeout()
	defaultNoOutputTimeout := t.defaultNoOutputTimeout()
	backgroundRequested := background
	yieldRequested := yieldDelay > 0
	canBackground := allowBackground && toolCtx.Runtime != nil && toolCtx.Runtime.GetProcesses() != nil
	if timeout <= 0 && defaultTimeout > 0 && !(canBackground && (backgroundRequested || yieldRequested)) {
		timeout = defaultTimeout
	}
	if canBackground && !backgroundRequested && !yieldRequested && defaultBackgroundWindow > 0 {
		yieldDelay = defaultBackgroundWindow
	}
	workdir, err := resolveToolDefaultWorkingDir(toolCtx)
	if err != nil {
		return core.ToolResult{}, err
	}
	if strings.TrimSpace(workdirArg) != "" {
		workdir, err = normalizeToolInputPath(workdir, workdirArg)
		if err != nil {
			return core.ToolResult{}, err
		}
	}
	if err := ensurePathWithinToolSandbox(toolCtx, workdir); err != nil {
		return core.ToolResult{}, err
	}
	baseEnv, err := infra.AppendAgentRuntimeEnv(os.Environ(), toolCtx.Run.Identity, toolCtx.Runtime.GetEnvironment(), envOverrides)
	if err != nil {
		return core.ToolResult{}, err
	}
	if toolCtx.Sandbox != nil && toolCtx.Sandbox.Enabled {
		sandboxDirs, err := resolveToolAccessBoundaryDirs(toolCtx)
		if err != nil {
			return core.ToolResult{}, err
		}
		baseEnv = append(baseEnv,
			"KOCORT_SANDBOX=1",
			"KOCORT_SANDBOX_SCOPE="+toolCtx.Sandbox.Scope,
			"KOCORT_SANDBOX_DIRS="+strings.Join(sandboxDirs, string(os.PathListSeparator)),
			"KOCORT_SANDBOX_WORKSPACE="+toolCtx.Sandbox.WorkspaceDir,
			"KOCORT_SANDBOX_AGENT_WORKSPACE="+toolCtx.Sandbox.AgentWorkspace,
			"KOCORT_SANDBOX_WORKSPACE_ACCESS="+toolCtx.Sandbox.WorkspaceAccess,
		)
	}
	if canBackground && (background || yieldDelay > 0) {
		record, err := t.startManagedProcess(toolCtx, command, workdir, baseEnv, timeout, defaultNoOutputTimeout, background, ptyEnabled)
		if err != nil {
			return core.ToolResult{}, err
		}
		if background {
			return JSONResult(map[string]any{
				"sessionId":    record.ID,
				"status":       record.Status,
				"backgrounded": true,
				"startedAt":    record.StartedAt,
				"workdir":      record.Workdir,
				"command":      record.Command,
				"mode":         record.Mode,
			})
		}
		record, finished, err := t.waitForForegroundYield(toolCtx, record.ID, yieldDelay)
		if err != nil {
			return core.ToolResult{}, err
		}
		if !finished {
			return JSONResult(map[string]any{
				"sessionId":    record.ID,
				"status":       record.Status,
				"backgrounded": true,
				"startedAt":    record.StartedAt,
				"workdir":      record.Workdir,
				"command":      record.Command,
				"mode":         record.Mode,
				"tail":         record.Tail,
			})
		}
		text := strings.TrimSpace(record.Output)
		if text == "" {
			text = strings.TrimSpace(record.Tail)
		}
		if text == "" {
			text = record.Status
		}
		return core.ToolResult{Text: text}, nil
	}
	if ptyEnabled && canBackground {
		record, err := t.startManagedProcess(toolCtx, command, workdir, baseEnv, timeout, defaultNoOutputTimeout, false, true)
		if err != nil {
			return core.ToolResult{}, err
		}
		record, finished, err := t.waitForForegroundYield(toolCtx, record.ID, timeoutOrFallback(timeout, defaultTimeout))
		if err != nil {
			return core.ToolResult{}, err
		}
		if !finished {
			return core.ToolResult{}, fmt.Errorf("pty exec session %q did not finish in foreground window", record.ID)
		}
		text := strings.TrimSpace(record.Output)
		if text == "" {
			text = strings.TrimSpace(record.Tail)
		}
		if text == "" {
			text = record.Status
		}
		return core.ToolResult{Text: text}, nil
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd, err := infra.CommandShellContext(ctx, command)
	if err != nil {
		return core.ToolResult{}, err
	}
	if workdir != "" {
		cmd.Dir = workdir
	}
	cmd.Env = append([]string{}, baseEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return core.ToolResult{}, context.DeadlineExceeded
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return core.ToolResult{}, context.Canceled
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return core.ToolResult{}, fmt.Errorf("exec failed: %w", err)
		}
		return core.ToolResult{}, fmt.Errorf("%s: %w", message, err)
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		text = strings.TrimSpace(stderr.String())
	}
	return core.ToolResult{Text: text}, nil
}

func (t *ExecTool) startManagedProcess(toolCtx ToolContext, command string, workdir string, env []string, timeout time.Duration, noOutputTimeout time.Duration, background bool, pty bool) (ProcessSessionRecord, error) {
	if toolCtx.Runtime == nil || toolCtx.Runtime.GetProcesses() == nil {
		return ProcessSessionRecord{}, core.ErrProcessNotConfigured
	}
	return toolCtx.Runtime.GetProcesses().Start(context.Background(), ProcessStartOptions{
		SessionKey:      toolCtx.Run.Session.SessionKey,
		RunID:           toolCtx.Run.Request.RunID,
		Command:         command,
		Workdir:         workdir,
		Env:             env,
		Timeout:         timeout,
		NoOutputTimeout: noOutputTimeout,
		Backgrounded:    background,
		PTY:             pty,
	})
}

func (t *ExecTool) waitForForegroundYield(toolCtx ToolContext, sessionID string, yield time.Duration) (ProcessSessionRecord, bool, error) {
	record, ok := toolCtx.Runtime.GetProcesses().Poll(sessionID, yield)
	if !ok {
		return ProcessSessionRecord{}, false, fmt.Errorf("process session %q disappeared", sessionID)
	}
	if record.Status == "running" {
		return record, false, nil
	}
	return record, true, nil
}

func (t *ExecTool) ToolRegistrationMeta() core.ToolRegistrationMeta {
	return core.ToolRegistrationMeta{}
}

func (t *ExecTool) allowBackground() bool {
	if t.defaults.AllowBackground == nil {
		return true
	}
	return *t.defaults.AllowBackground
}

func (t *ExecTool) defaultBackgroundWindow() time.Duration {
	if t.defaults.BackgroundMs > 0 {
		return time.Duration(t.defaults.BackgroundMs) * time.Millisecond
	}
	return defaultExecBackgroundMs * time.Millisecond
}

func (t *ExecTool) defaultTimeout() time.Duration {
	if t.defaults.TimeoutSec > 0 {
		return time.Duration(t.defaults.TimeoutSec) * time.Second
	}
	return defaultExecTimeoutSec * time.Second
}

func (t *ExecTool) defaultNoOutputTimeout() time.Duration {
	if t.defaults.NoOutputTimeoutMs > 0 {
		return time.Duration(t.defaults.NoOutputTimeoutMs) * time.Millisecond
	}
	return 0
}

func timeoutOrFallback(timeout time.Duration, fallback time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return fallback
}

func ParseExecToolResultSessionID(result core.ToolResult) string {
	if len(result.JSON) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(result.JSON, &payload); err != nil {
		return ""
	}
	if value, ok := payload["sessionId"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}
