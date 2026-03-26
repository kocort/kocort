package tool

import "context"

// ToolApprovalRequest describes a tool execution that requires approval.
type ToolApprovalRequest struct {
	ToolName          string
	PluginID          string
	AgentID           string
	SessionKey        string
	Channel           string
	To                string
	Elevated          bool
	SandboxEnabled    bool
	SandboxMode       string
	WorkspaceAccess   string
	SessionVisibility string
}

// ToolApprovalDecision is the result of an approval check.
type ToolApprovalDecision struct {
	Allowed bool
	Reason  string
}

// ToolApprovalRunner is the interface for tool approval backends.
type ToolApprovalRunner interface {
	ApproveToolExecution(ctx context.Context, req ToolApprovalRequest) (ToolApprovalDecision, error)
}
