package handlers

// Workspace HTTP handlers for chat and task operations.

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kocort/kocort/api/service"
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/runtime"
	"github.com/kocort/kocort/utils"
)

// Workspace holds dependencies for workspace handlers.
type Workspace struct {
	Runtime *runtime.Runtime
}

// ChatBootstrap handles GET /api/workspace/chat/bootstrap.
func (h *Workspace) ChatBootstrap(c *gin.Context) {
	sessionKey := strings.TrimSpace(c.Query("sessionKey"))
	if sessionKey == "" {
		sessionKey = session.BuildMainSessionKeyWithMain(
			config.ResolveDefaultConfiguredAgentID(h.Runtime.Config),
			config.ResolveSessionMainKeyForAPI(h.Runtime.Config),
		)
	}
	limit, before, err := service.ParseHistoryWindow(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	history := service.NewChatGateway(h.Runtime).LoadHistory(sessionKey, limit, before)
	c.JSON(http.StatusOK, types.ChatBootstrapResponse{
		SessionKey: sessionKey,
		History:    history,
	})
}

// ChatSend handles POST /api/workspace/chat/send.
func (h *Workspace) ChatSend(c *gin.Context) {
	var req types.ChatSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	attachments, err := normalizeAttachments(req.Attachments)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := service.NewChatGateway(h.Runtime).Send(c.Request.Context(), core.ChatSendRequest{
		SessionKey:  req.SessionKey,
		RunID:       req.RunID,
		Message:     req.Message,
		Channel:     utils.NonEmpty(req.Channel, "webchat"),
		To:          utils.NonEmpty(req.To, "webchat-user"),
		TimeoutMs:   req.TimeoutMs,
		Stop:        req.Stop,
		Attachments: attachments,
	})
	if err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ChatCancel handles POST /api/workspace/chat/cancel.
func (h *Workspace) ChatCancel(c *gin.Context) {
	var req types.ChatCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := service.NewChatGateway(h.Runtime).Cancel(c.Request.Context(), core.ChatCancelRequest{
		SessionKey: req.SessionKey,
		RunID:      req.RunID,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ChatHistory handles GET /api/workspace/chat/history.
func (h *Workspace) ChatHistory(c *gin.Context) {
	sessionKey := strings.TrimSpace(c.Query("sessionKey"))
	if sessionKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing sessionKey"})
		return
	}
	limit, before, err := service.ParseHistoryWindow(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp := service.NewChatGateway(h.Runtime).LoadHistory(sessionKey, limit, before)
	c.JSON(http.StatusOK, resp)
}

// TasksGet handles GET /api/workspace/tasks.
func (h *Workspace) TasksGet(c *gin.Context) {
	items := append([]core.TaskRecord{}, h.Runtime.ListTasks(c.Request.Context())...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	c.JSON(http.StatusOK, types.TasksResponse{Tasks: items})
}

// TasksCreate handles POST /api/workspace/tasks.
func (h *Workspace) TasksCreate(c *gin.Context) {
	var req types.TaskCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	record, err := h.Runtime.ScheduleTask(c.Request.Context(), service.TaskScheduleRequestFromCreate(req))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, record)
}

// TaskUpdate handles POST /api/workspace/tasks/update.
func (h *Workspace) TaskUpdate(c *gin.Context) {
	var req types.TaskUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.Runtime.Tasks == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "task scheduler not configured"})
		return
	}
	updated, ok, err := h.Runtime.Tasks.Update(req.ID, service.TaskScheduleRequestFromUpdate(req))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// TaskDelete handles POST /api/workspace/tasks/delete.
func (h *Workspace) TaskDelete(c *gin.Context) {
	var req types.TaskActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	record, err := h.Runtime.DeleteTask(c.Request.Context(), req.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if record == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, record)
}

// TaskCancel handles POST /api/workspace/tasks/cancel.
func (h *Workspace) TaskCancel(c *gin.Context) {
	var req types.TaskActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	record, err := h.Runtime.CancelTask(c.Request.Context(), req.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if record == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, record)
}

// Media handles GET /api/workspace/media.
// It serves local files for webchat display.
func (h *Workspace) Media(c *gin.Context) {
	path := strings.TrimSpace(c.Query("path"))
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing path parameter"})
		return
	}

	// Parse file URI to OS-native path using standard library.
	path = utils.FileURIToPath(path)

	// Resolve the default workspace through the runtime identity so implicit
	// state-dir workspaces are treated the same as explicit config values.
	if h.Runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}
	identity, err := service.ResolveDefaultIdentityPublic(c.Request.Context(), h.Runtime)
	workspaceDir := ""
	if err == nil {
		workspaceDir = strings.TrimSpace(identity.WorkspaceDir)
	}
	if workspaceDir == "" && h.Runtime.Sessions != nil {
		workspaceDir = infra.ResolveDefaultAgentWorkspaceDirForState(
			h.Runtime.Sessions.BaseDir(),
			config.ResolveDefaultConfiguredAgentID(h.Runtime.Config),
		)
	}
	workspaceDir, err = infra.EnsureWorkspaceDir(workspaceDir)
	if err != nil || workspaceDir == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}

	// Check file exists and is a regular file
	info, err := os.Stat(absPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory"})
		return
	}

	// Set content type based on file extension
	ext := strings.ToLower(filepath.Ext(absPath))
	contentType := "application/octet-stream"
	switch ext {
	case ".jpg", ".jpeg":
		contentType = "image/jpeg"
	case ".png":
		contentType = "image/png"
	case ".gif":
		contentType = "image/gif"
	case ".webp":
		contentType = "image/webp"
	case ".svg":
		contentType = "image/svg+xml"
	case ".pdf":
		contentType = "application/pdf"
	}

	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filepath.Base(absPath)))
	c.File(absPath)
}

// Helpers

func normalizeAttachments(attachments []types.ChatAttachmentRequest) ([]core.Attachment, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	out := make([]core.Attachment, 0, len(attachments))
	for idx, att := range attachments {
		label := strings.TrimSpace(att.FileName)
		if label == "" {
			label = strings.TrimSpace(att.Type)
		}
		if label == "" {
			label = "attachment-" + string(rune('1'+idx))
		}
		content := strings.TrimSpace(att.Content)
		if content == "" {
			return nil, fmt.Errorf("attachment %s: content must not be empty", label)
		}
		if match := dataURLBase64(content); match != "" {
			content = match
		}
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("attachment %s: invalid base64 content: %w", label, err)
		}
		if len(decoded) == 0 {
			return nil, fmt.Errorf("attachment %s: decoded content is empty", label)
		}
		if len(decoded) > infra.MaxChatAttachmentBytes() {
			return nil, fmt.Errorf("attachment %s: exceeds size limit (%d > %d bytes)", label, len(decoded), infra.MaxChatAttachmentBytes())
		}
		out = append(out, core.Attachment{
			Type:     strings.TrimSpace(att.Type),
			Name:     strings.TrimSpace(att.FileName),
			MIMEType: strings.TrimSpace(att.MIMEType),
			Content:  decoded,
		})
	}
	return out, nil
}

func dataURLBase64(value string) string {
	if idx := strings.Index(value, ","); strings.HasPrefix(strings.ToLower(value), "data:") && idx > 0 {
		return strings.TrimSpace(value[idx+1:])
	}
	return ""
}
