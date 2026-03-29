package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Value Objects — ACP (Agent Communication Protocol)
// ---------------------------------------------------------------------------

type AcpRuntimeHandle struct {
	SessionKey         string
	Backend            string
	RuntimeSessionName string
	Cwd                string
	BackendSessionID   string
	AgentSessionID     string
}

type AcpSessionRuntimeOptions struct {
	RuntimeMode       string            `json:"runtimeMode,omitempty"`
	Model             string            `json:"model,omitempty"`
	Cwd               string            `json:"cwd,omitempty"`
	PermissionProfile string            `json:"permissionProfile,omitempty"`
	TimeoutSeconds    int               `json:"timeoutSeconds,omitempty"`
	BackendExtras     map[string]string `json:"backendExtras,omitempty"`
}

type AcpRuntimeCapabilities struct {
	Controls         []AcpRuntimeControl
	ConfigOptionKeys []string
}

type AcpRuntimeStatus struct {
	Summary          string
	BackendSessionID string
	AgentSessionID   string
	Details          map[string]any
}

type AcpRuntimeEvent struct {
	Type       string
	Text       string
	Stream     string
	Tag        string
	ToolCallID string
	Status     string
	Title      string
	Used       int
	Size       int
	StopReason string
	Code       string
	Retryable  bool
}

type AcpEnsureSessionInput struct {
	SessionKey      string
	Agent           string
	Mode            AcpRuntimeSessionMode
	ResumeSessionID string
	Cwd             string
	Env             map[string]string
}

type AcpRunTurnInput struct {
	Handle    AcpRuntimeHandle
	Text      string
	Mode      AcpRuntimePromptMode
	RequestID string
	Signal    context.Context
	OnEvent   func(AcpRuntimeEvent) error
}

type AcpSetModeInput struct {
	Handle AcpRuntimeHandle
	Mode   string
}

type AcpSetConfigOptionInput struct {
	Handle AcpRuntimeHandle
	Key    string
	Value  string
}

type AcpCancelInput struct {
	Handle AcpRuntimeHandle
	Reason string
}

type AcpCloseInput struct {
	Handle AcpRuntimeHandle
	Reason string
}

type AcpSessionMeta struct {
	Backend            string                    `json:"backend,omitempty"`
	Agent              string                    `json:"agent,omitempty"`
	IdentityName       string                    `json:"identityName,omitempty"`
	RuntimeSessionName string                    `json:"runtimeSessionName,omitempty"`
	BackendSessionID   string                    `json:"backendSessionId,omitempty"`
	AgentSessionID     string                    `json:"agentSessionId,omitempty"`
	Cwd                string                    `json:"cwd,omitempty"`
	State              string                    `json:"state,omitempty"`
	Mode               AcpRuntimeSessionMode     `json:"mode,omitempty"`
	LastActivityAt     int64                     `json:"lastActivityAt,omitempty"`
	LastError          string                    `json:"lastError,omitempty"`
	RuntimeOptions     *AcpSessionRuntimeOptions `json:"runtimeOptions,omitempty"`
	Capabilities       *AcpRuntimeCapabilities   `json:"capabilities,omitempty"`
	RuntimeStatus      *AcpRuntimeStatus         `json:"runtimeStatus,omitempty"`
	UnsupportedOptions []string                  `json:"unsupportedOptions,omitempty"`
	Observability      map[string]any            `json:"observability,omitempty"`
}

// ---------------------------------------------------------------------------
// Value Objects — Command Backend Config
// ---------------------------------------------------------------------------

type CommandBackendConfig struct {
	Command            string                   `json:"command,omitempty"`
	Args               []string                 `json:"args,omitempty"`
	ResumeArgs         []string                 `json:"resumeArgs,omitempty"`
	InputMode          CommandBackendInputMode  `json:"input,omitempty"`
	OutputMode         CommandBackendOutputMode `json:"output,omitempty"`
	PromptArg          string                   `json:"promptArg,omitempty"`
	SystemPromptArg    string                   `json:"systemPromptArg,omitempty"`
	ModelArg           string                   `json:"modelArg,omitempty"`
	SessionArg         string                   `json:"sessionArg,omitempty"`
	SessionIDFields    []string                 `json:"sessionIdFields,omitempty"`
	SystemPromptMode   string                   `json:"systemPromptMode,omitempty"`
	Env                map[string]string        `json:"env,omitempty"`
	WorkingDir         string                   `json:"workingDir,omitempty"`
	OverallTimeout     time.Duration            `json:"-"`
	NoOutputTimeout    time.Duration            `json:"-"`
	StreamText         bool                     `json:"streamText,omitempty"`
	SessionExpiredText []string                 `json:"sessionExpiredText,omitempty"`
}

func (c *CommandBackendConfig) UnmarshalJSON(data []byte) error {
	type rawCommandBackendConfig struct {
		Command            string            `json:"command,omitempty"`
		Args               []string          `json:"args,omitempty"`
		ResumeArgs         []string          `json:"resumeArgs,omitempty"`
		InputMode          string            `json:"input,omitempty"`
		OutputMode         string            `json:"output,omitempty"`
		PromptArg          string            `json:"promptArg,omitempty"`
		SystemPromptArg    string            `json:"systemPromptArg,omitempty"`
		ModelArg           string            `json:"modelArg,omitempty"`
		SessionArg         string            `json:"sessionArg,omitempty"`
		SessionIDFields    []string          `json:"sessionIdFields,omitempty"`
		SystemPromptMode   string            `json:"systemPromptMode,omitempty"`
		Env                map[string]string `json:"env,omitempty"`
		WorkingDir         string            `json:"workingDir,omitempty"`
		OverallTimeoutMs   int               `json:"overallTimeoutMs,omitempty"`
		NoOutputTimeoutMs  int               `json:"noOutputTimeoutMs,omitempty"`
		OverallTimeout     string            `json:"overallTimeout,omitempty"`
		NoOutputTimeout    string            `json:"noOutputTimeout,omitempty"`
		StreamText         bool              `json:"streamText,omitempty"`
		SessionExpiredText []string          `json:"sessionExpiredText,omitempty"`
	}
	var raw rawCommandBackendConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = CommandBackendConfig{
		Command:            strings.TrimSpace(raw.Command),
		Args:               append([]string{}, raw.Args...),
		ResumeArgs:         append([]string{}, raw.ResumeArgs...),
		InputMode:          CommandBackendInputMode(strings.TrimSpace(raw.InputMode)),
		OutputMode:         CommandBackendOutputMode(strings.TrimSpace(raw.OutputMode)),
		PromptArg:          strings.TrimSpace(raw.PromptArg),
		SystemPromptArg:    strings.TrimSpace(raw.SystemPromptArg),
		ModelArg:           strings.TrimSpace(raw.ModelArg),
		SessionArg:         strings.TrimSpace(raw.SessionArg),
		SessionIDFields:    append([]string{}, raw.SessionIDFields...),
		SystemPromptMode:   strings.TrimSpace(raw.SystemPromptMode),
		Env:                raw.Env,
		WorkingDir:         strings.TrimSpace(raw.WorkingDir),
		StreamText:         raw.StreamText,
		SessionExpiredText: append([]string{}, raw.SessionExpiredText...),
	}
	if raw.OverallTimeoutMs > 0 {
		c.OverallTimeout = time.Duration(raw.OverallTimeoutMs) * time.Millisecond
	} else if strings.TrimSpace(raw.OverallTimeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(raw.OverallTimeout))
		if err != nil {
			return err
		}
		c.OverallTimeout = d
	}
	if raw.NoOutputTimeoutMs > 0 {
		c.NoOutputTimeout = time.Duration(raw.NoOutputTimeoutMs) * time.Millisecond
	} else if strings.TrimSpace(raw.NoOutputTimeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(raw.NoOutputTimeout))
		if err != nil {
			return err
		}
		c.NoOutputTimeout = d
	}
	return nil
}
