package backend

import (
	"context"
	"fmt"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"strings"
	"time"
)

const (
	cliWatchdogMinTimeoutMs = 1_000
)

// CLIBackend wraps a CommandBackend with CLI-specific session resume logic.
type CLIBackend struct {
	Config   config.AppConfig
	Env      *infra.EnvironmentRuntime
	Provider string
	Command  core.CommandBackendConfig
}

func (b *CLIBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	runCtx.Runtime = ensureRuntime(runCtx)
	commandCfg := b.Command
	sessionID := GetCLISessionID(runCtx.Session.Entry, b.Provider)
	useResume := sessionID != "" && len(commandCfg.ResumeArgs) > 0
	if useResume {
		commandCfg.Args = resolveResumeArgs(commandCfg.ResumeArgs, sessionID)
	}
	if commandCfg.NoOutputTimeout <= 0 {
		commandCfg.NoOutputTimeout = resolveCliNoOutputTimeout(runCtx.Request.Timeout, useResume)
	}
	result, err := (&CommandBackend{Config: commandCfg, Env: b.Env}).Run(ctx, runCtx)
	if err == nil {
		result = ensureCLIResultMeta(result, b.Provider)
		result.Meta["resumeUsed"] = useResume
		result.Meta["watchdogMs"] = int(commandCfg.NoOutputTimeout / time.Millisecond)
		if useResume && sessionID != "" {
			result.Meta["resumedSessionId"] = sessionID
		}
		return result, nil
	}
	if !isCLISessionExpiredError(err, commandCfg) || !useResume {
		return core.AgentRunResult{}, err
	}
	ClearCLISessionID(runCtx.Session.Entry, b.Provider)
	commandCfg.Args = append([]string{}, b.Command.Args...)
	commandCfg.NoOutputTimeout = resolveCliNoOutputTimeout(runCtx.Request.Timeout, false)
	retried, retryErr := (&CommandBackend{Config: commandCfg, Env: b.Env}).Run(ctx, runCtx)
	if retryErr != nil {
		return core.AgentRunResult{}, retryErr
	}
	retried = ensureCLIResultMeta(retried, b.Provider)
	retried.Meta["sessionRetry"] = true
	retried.Meta["resumeUsed"] = false
	retried.Meta["watchdogMs"] = int(commandCfg.NoOutputTimeout / time.Millisecond)
	return retried, nil
}

// GetCLISessionID returns the CLI session ID for a provider from a session entry.
func GetCLISessionID(entry *core.SessionEntry, provider string) string {
	if entry == nil {
		return ""
	}
	normalized := NormalizeProviderID(provider)
	if entry.CLISessionIDs != nil {
		if value := strings.TrimSpace(entry.CLISessionIDs[normalized]); value != "" {
			return value
		}
	}
	if normalized == "claude-cli" {
		return strings.TrimSpace(entry.ClaudeCLISessionID)
	}
	return ""
}

// SetCLISessionID stores a CLI session ID for a provider in a session entry.
func SetCLISessionID(entry *core.SessionEntry, provider string, sessionID string) {
	if entry == nil {
		return
	}
	normalized := NormalizeProviderID(provider)
	if entry.CLISessionIDs == nil {
		entry.CLISessionIDs = map[string]string{}
	}
	if trimmed := strings.TrimSpace(sessionID); trimmed != "" {
		entry.CLISessionIDs[normalized] = trimmed
		if normalized == "claude-cli" {
			entry.ClaudeCLISessionID = trimmed
		}
	}
}

// ClearCLISessionID removes the CLI session ID for a provider from a session entry.
func ClearCLISessionID(entry *core.SessionEntry, provider string) {
	if entry == nil {
		return
	}
	normalized := NormalizeProviderID(provider)
	if entry.CLISessionIDs != nil {
		delete(entry.CLISessionIDs, normalized)
		if len(entry.CLISessionIDs) == 0 {
			entry.CLISessionIDs = nil
		}
	}
	if normalized == "claude-cli" {
		entry.ClaudeCLISessionID = ""
	}
}

func resolveResumeArgs(args []string, sessionID string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, strings.ReplaceAll(arg, "{sessionId}", sessionID))
	}
	return out
}

func resolveCliNoOutputTimeout(timeout time.Duration, useResume bool) time.Duration {
	timeoutMs := int(timeout / time.Millisecond)
	if timeoutMs <= 0 {
		timeoutMs = 10 * 60 * 1000
	}
	var ratio float64
	var minMs int
	var maxMs int
	if useResume {
		ratio = 0.3
		minMs = 60_000
		maxMs = 180_000
	} else {
		ratio = 0.8
		minMs = 180_000
		maxMs = 600_000
	}
	capMs := max(cliWatchdogMinTimeoutMs, timeoutMs-1_000)
	computed := int(float64(timeoutMs) * ratio)
	if computed < minMs {
		computed = minMs
	}
	if computed > maxMs {
		computed = maxMs
	}
	if computed > capMs {
		computed = capMs
	}
	return time.Duration(computed) * time.Millisecond
}

func isCLISessionExpiredError(err error, cfg core.CommandBackendConfig) bool {
	if err == nil {
		return false
	}
	raw := strings.ToLower(err.Error())
	if raw == "" {
		return false
	}
	terms := cfg.SessionExpiredText
	if len(terms) == 0 {
		terms = []string{
			"session not found",
			"session does not exist",
			"session expired",
			"session invalid",
			"conversation not found",
			"conversation does not exist",
			"conversation expired",
			"conversation invalid",
			"no such session",
			"invalid session",
			"session id not found",
			"conversation id not found",
		}
	}
	for _, term := range terms {
		if strings.Contains(raw, strings.ToLower(strings.TrimSpace(term))) {
			return true
		}
	}
	return false
}

func ensureCLIResultMeta(result core.AgentRunResult, provider string) core.AgentRunResult {
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	result.Meta["backendKind"] = "cli"
	result.Meta["provider"] = NormalizeProviderID(provider)
	return result
}

// RequireCommandConfig validates that a command backend config is present and usable.
func RequireCommandConfig(cfg *core.CommandBackendConfig, provider string) error {
	if cfg == nil || strings.TrimSpace(cfg.Command) == "" {
		return fmt.Errorf("provider %q is missing command backend config", provider)
	}
	return nil
}
