package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
)

type ImageTool struct{}

func NewImageTool() *ImageTool { return &ImageTool{} }

func (t *ImageTool) Name() string { return "image" }

func (t *ImageTool) Description() string { return "Analyze an image with the configured image model." }

func (t *ImageTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Optional workspace-relative image path. If omitted, use the first image attachment from the current request."},
				"prompt": map[string]any{"type": "string", "description": "Question or instruction for the image analysis."},
			},
			"additionalProperties": false,
		},
	}
}

type imageRuntime interface {
	AnalyzeImage(context.Context, core.AgentIdentity, core.SessionResolution, core.AgentRunRequest) (core.AgentRunResult, error)
}

func (t *ImageTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	runtime, ok := toolCtx.Runtime.(imageRuntime)
	if !ok {
		return core.ToolResult{}, fmt.Errorf("image analysis is not available in this runtime")
	}
	prompt, _ := ReadStringParam(args, "prompt", false)
	pathArg, _ := ReadStringParam(args, "path", false)
	request := core.AgentRunRequest{
		RunID:      toolCtx.Run.Request.RunID + ":image",
		SessionKey: toolCtx.Run.Session.SessionKey,
		SessionID:  toolCtx.Run.Session.SessionID,
		AgentID:    toolCtx.Run.Identity.ID,
		Message:    strings.TrimSpace(prompt),
		Deliver:    false,
		Channel:    toolCtx.Run.Request.Channel,
		To:         toolCtx.Run.Request.To,
	}
	if strings.TrimSpace(request.Message) == "" {
		request.Message = "Describe the image."
	}
	if strings.TrimSpace(pathArg) != "" {
		attachment, err := loadImageAttachmentFromPath(toolCtx, pathArg)
		if err != nil {
			return core.ToolResult{}, err
		}
		request.Attachments = []core.Attachment{attachment}
	} else {
		for _, attachment := range toolCtx.Run.Request.Attachments {
			if infra.AttachmentIsImage(attachment) {
				request.Attachments = []core.Attachment{attachment}
				break
			}
		}
	}
	if len(request.Attachments) == 0 {
		return core.ToolResult{}, ToolInputError{Message: "image requires either path or an image attachment in the current request"}
	}
	result, err := runtime.AnalyzeImage(ctx, toolCtx.Run.Identity, toolCtx.Run.Session, request)
	if err != nil {
		return core.ToolResult{}, err
	}
	reply := ""
	for i := len(result.Payloads) - 1; i >= 0; i-- {
		if strings.TrimSpace(result.Payloads[i].Text) != "" {
			reply = strings.TrimSpace(result.Payloads[i].Text)
			break
		}
	}
	return JSONResult(map[string]any{
		"status": "ok",
		"reply":  reply,
		"runId":  result.RunID,
	})
}

func loadImageAttachmentFromPath(toolCtx ToolContext, pathArg string) (core.Attachment, error) {
	_, relPath, absPath, err := resolveWorkspaceToolPath(toolCtx, pathArg)
	if err != nil {
		return core.Attachment{}, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return core.Attachment{}, err
	}
	attachment := core.Attachment{
		Type:     "image",
		Name:     filepath.Base(relPath),
		MIMEType: inferImageMimeType(relPath),
		Content:  data,
	}
	if !infra.AttachmentIsImage(attachment) {
		return core.Attachment{}, ToolInputError{Message: "path does not point to a supported image"}
	}
	return attachment, nil
}

func inferImageMimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
