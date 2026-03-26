// Tool approval request builder — canonical types in kocort/internal/tool.
package tool

import (
	"strings"

	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/internal/session"
)

func BuildToolApprovalRequest(runCtx AgentRunContext, meta core.ToolRegistrationMeta, toolName string, sandbox *SandboxContext) ToolApprovalRequest {
	req := ToolApprovalRequest{
		ToolName:          NormalizeToolPolicyName(toolName),
		PluginID:          NormalizeToolPolicyName(meta.PluginID),
		AgentID:           session.NormalizeAgentID(runCtx.Identity.ID),
		SessionKey:        strings.TrimSpace(runCtx.Session.SessionKey),
		Channel:           strings.TrimSpace(runCtx.Request.Channel),
		To:                strings.TrimSpace(runCtx.Request.To),
		Elevated:          meta.Elevated,
		SessionVisibility: strings.TrimSpace(runCtx.Identity.SandboxSessionVisibility),
	}
	if sandbox != nil {
		req.SandboxEnabled = sandbox.Enabled
		req.SandboxMode = strings.TrimSpace(sandbox.Mode)
		req.WorkspaceAccess = strings.TrimSpace(sandbox.WorkspaceAccess)
	}
	return req
}
