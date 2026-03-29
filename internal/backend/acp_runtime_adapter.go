package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/utils"
)

const acpMethodSessionSetConfigOption = "session/set_config_option"

var (
	safeAutoApproveToolIDs = map[string]struct{}{
		"read":          {},
		"search":        {},
		"web_search":    {},
		"memory_search": {},
	}
	trustedSafeToolAliases = map[string]struct{}{
		"search": {},
	}
	readToolPathKeys = []string{"path", "file_path", "filePath"}
)

type ACPClientRuntime struct {
	config   config.AppConfig
	env      *infra.EnvironmentRuntime
	provider string
	command  core.CommandBackendConfig

	mu       sync.Mutex
	sessions map[string]*acpClientSession
}

type acpClientSession struct {
	runtime *ACPClientRuntime

	mu             sync.Mutex
	sessionKey     string
	mode           core.AcpRuntimeSessionMode
	cwd            string
	runtimeHandle  core.AcpRuntimeHandle
	initResponse   acp.InitializeResponse
	currentMode    string
	currentModel   string
	onEvent        func(core.AcpRuntimeEvent) error
	lastError      string
	processExited  bool
	processExitErr error
	processExitAt  time.Time
	command        *exec.Cmd
	stdin          io.WriteCloser
	stdout         io.ReadCloser
	conn           *acp.Connection
	stderr         strings.Builder
	exitedCh       chan struct{}
}

func NewACPClientRuntime(cfg config.AppConfig, env *infra.EnvironmentRuntime, provider string, command core.CommandBackendConfig) *ACPClientRuntime {
	return &ACPClientRuntime{
		config:   cfg,
		env:      env,
		provider: NormalizeProviderID(provider),
		command:  command,
		sessions: map[string]*acpClientSession{},
	}
}

func (r *ACPClientRuntime) EnsureSession(ctx context.Context, input core.AcpEnsureSessionInput) (core.AcpRuntimeHandle, error) {
	if strings.TrimSpace(input.SessionKey) == "" {
		return core.AcpRuntimeHandle{}, core.ErrACPSessionKeyRequired
	}

	r.mu.Lock()
	existing := r.sessions[input.SessionKey]
	if existing != nil && existing.matches(input) && existing.isAlive() {
		handle := existing.snapshotHandle()
		r.mu.Unlock()
		return handle, nil
	}
	if existing != nil {
		delete(r.sessions, input.SessionKey)
	}
	r.mu.Unlock()

	if existing != nil {
		_ = existing.close(context.Background(), "replaced")
	}

	session, err := r.startSession(ctx, input)
	if err != nil {
		return core.AcpRuntimeHandle{}, err
	}

	r.mu.Lock()
	r.sessions[input.SessionKey] = session
	r.mu.Unlock()
	return session.snapshotHandle(), nil
}

func (r *ACPClientRuntime) RunTurn(ctx context.Context, input core.AcpRunTurnInput) error {
	session, err := r.requireSession(input.Handle.SessionKey)
	if err != nil {
		return err
	}
	if !session.isAlive() {
		return fmt.Errorf("ACP runtime session %q is not alive", input.Handle.SessionKey)
	}

	session.setOnEvent(input.OnEvent)
	defer session.setOnEvent(nil)

	req := acp.PromptRequest{
		SessionId: acp.SessionId(utils.NonEmpty(input.Handle.AgentSessionID, input.Handle.BackendSessionID)),
		Prompt:    []acp.ContentBlock{acp.TextBlock(input.Text)},
	}

	cancelCtx, stopCancel := context.WithCancel(context.Background())
	defer stopCancel()
	go func() {
		select {
		case <-ctx.Done():
			_ = session.cancel(cancelCtx, "context-cancel")
		case <-cancelCtx.Done():
		}
	}()

	resp, err := acp.SendRequest[acp.PromptResponse](session.conn, ctx, acp.AgentMethodSessionPrompt, req)
	if err != nil {
		if ctx.Err() != nil {
			return session.emit(core.AcpRuntimeEvent{Type: "done", StopReason: string(acp.StopReasonCancelled)})
		}
		_ = session.emit(core.AcpRuntimeEvent{
			Type:      "error",
			Text:      err.Error(),
			Code:      string(ErrorReason(err)),
			Retryable: ErrorReason(err) == BackendFailureTransientHTTP,
		})
		return err
	}
	return session.emit(core.AcpRuntimeEvent{Type: "done", StopReason: string(resp.StopReason)})
}

func (r *ACPClientRuntime) GetCapabilities(_ context.Context, handle *core.AcpRuntimeHandle) (core.AcpRuntimeCapabilities, error) {
	session, err := r.requireSessionHandle(handle)
	if err != nil {
		return core.AcpRuntimeCapabilities{}, err
	}
	controls := []core.AcpRuntimeControl{
		core.AcpControlSetConfigOption,
		core.AcpControlStatus,
	}
	if session.supportsModes() {
		controls = append([]core.AcpRuntimeControl{core.AcpControlSetMode}, controls...)
	}
	return core.AcpRuntimeCapabilities{
		Controls:         controls,
		ConfigOptionKeys: []string{},
	}, nil
}

func (r *ACPClientRuntime) GetStatus(_ context.Context, handle core.AcpRuntimeHandle) (core.AcpRuntimeStatus, error) {
	session, err := r.requireSession(handle.SessionKey)
	if err != nil {
		return core.AcpRuntimeStatus{}, err
	}
	return session.status(), nil
}

func (r *ACPClientRuntime) SetMode(ctx context.Context, input core.AcpSetModeInput) error {
	if strings.TrimSpace(input.Mode) == "" {
		return fmt.Errorf("ACP runtime mode is required")
	}
	session, err := r.requireSession(input.Handle.SessionKey)
	if err != nil {
		return err
	}
	_, err = acp.SendRequest[acp.SetSessionModeResponse](session.conn, ctx, acp.AgentMethodSessionSetMode, acp.SetSessionModeRequest{
		SessionId: acp.SessionId(utils.NonEmpty(input.Handle.AgentSessionID, input.Handle.BackendSessionID)),
		ModeId:    acp.SessionModeId(strings.TrimSpace(input.Mode)),
	})
	if err != nil {
		return err
	}
	session.setCurrentMode(input.Mode)
	return nil
}

func (r *ACPClientRuntime) SetConfigOption(ctx context.Context, input core.AcpSetConfigOptionInput) error {
	if strings.TrimSpace(input.Key) == "" {
		return fmt.Errorf("ACP config key is required")
	}
	session, err := r.requireSession(input.Handle.SessionKey)
	if err != nil {
		return err
	}
	sessionID := acp.SessionId(utils.NonEmpty(input.Handle.AgentSessionID, input.Handle.BackendSessionID))
	key := strings.TrimSpace(input.Key)
	value := strings.TrimSpace(input.Value)

	if strings.EqualFold(key, "model") {
		if _, err := acp.SendRequest[acp.SetSessionModelResponse](session.conn, ctx, acp.AgentMethodSessionSetModel, acp.SetSessionModelRequest{
			SessionId: sessionID,
			ModelId:   acp.ModelId(value),
		}); err == nil {
			session.setCurrentModel(value)
			return nil
		}
	}

	if err := session.conn.SendRequestNoResult(ctx, acpMethodSessionSetConfigOption, map[string]any{
		"sessionId": sessionID,
		"key":       key,
		"value":     value,
	}); err != nil {
		return err
	}
	if strings.EqualFold(key, "model") {
		session.setCurrentModel(value)
	}
	return nil
}

func (r *ACPClientRuntime) Cancel(ctx context.Context, input core.AcpCancelInput) error {
	session, err := r.requireSession(input.Handle.SessionKey)
	if err != nil {
		return err
	}
	return session.cancel(ctx, input.Reason)
}

func (r *ACPClientRuntime) Close(ctx context.Context, input core.AcpCloseInput) error {
	r.mu.Lock()
	session := r.sessions[input.Handle.SessionKey]
	delete(r.sessions, input.Handle.SessionKey)
	r.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.close(ctx, input.Reason)
}

func (r *ACPClientRuntime) startSession(ctx context.Context, input core.AcpEnsureSessionInput) (*acpClientSession, error) {
	if err := RequireCommandConfig(&r.command, r.provider); err != nil {
		return nil, err
	}

	command := strings.TrimSpace(r.command.Command)
	args := append([]string{}, r.command.Args...)
	if strings.TrimSpace(input.ResumeSessionID) != "" && len(r.command.ResumeArgs) > 0 {
		args = resolveResumeArgs(r.command.ResumeArgs, strings.TrimSpace(input.ResumeSessionID))
	}

	execCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(execCtx, command, args...)
	workdir, err := resolveACPSessionWorkdir(input, r.command)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Dir = workdir
	env, err := r.resolveEnv(cmd.Environ(), input.Env)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	session := &acpClientSession{
		runtime:    r,
		sessionKey: input.SessionKey,
		mode:       input.Mode,
		cwd:        workdir,
		command:    cmd,
		stdin:      stdin,
		stdout:     stdout,
		runtimeHandle: core.AcpRuntimeHandle{
			SessionKey:         input.SessionKey,
			Backend:            r.provider,
			RuntimeSessionName: input.SessionKey,
			Cwd:                workdir,
		},
		exitedCh: make(chan struct{}),
	}
	cmd.Stderr = &session.stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	session.conn = acp.NewConnection(session.handleInbound, stdin, stdout)
	go session.waitForExit(cancel)

	initResp, err := acp.SendRequest[acp.InitializeResponse](session.conn, ctx, acp.AgentMethodInitialize, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs:       acp.FileSystemCapability{ReadTextFile: true, WriteTextFile: true},
			Terminal: false,
		},
	})
	if err != nil {
		_ = session.close(context.Background(), "initialize-failed")
		return nil, err
	}
	session.initResponse = initResp

	if err := session.openACPConversation(ctx, input.ResumeSessionID); err != nil {
		_ = session.close(context.Background(), "session-open-failed")
		return nil, err
	}
	return session, nil
}

func (r *ACPClientRuntime) resolveEnv(base []string, input map[string]string) ([]string, error) {
	merged := map[string]string{}
	for key, value := range r.command.Env {
		merged[key] = value
	}
	for key, value := range input {
		merged[key] = value
	}
	if r.env == nil {
		out := append([]string{}, base...)
		for key, value := range merged {
			out = append(out, key+"="+value)
		}
		return out, nil
	}
	return r.env.AppendToEnv(base, merged)
}

func (r *ACPClientRuntime) requireSession(sessionKey string) (*acpClientSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[sessionKey]
	if session == nil {
		return nil, fmt.Errorf("ACP runtime session %q is not initialized", sessionKey)
	}
	return session, nil
}

func (r *ACPClientRuntime) requireSessionHandle(handle *core.AcpRuntimeHandle) (*acpClientSession, error) {
	if handle == nil {
		return nil, fmt.Errorf("ACP runtime handle is required")
	}
	return r.requireSession(handle.SessionKey)
}

func resolveACPSessionWorkdir(input core.AcpEnsureSessionInput, command core.CommandBackendConfig) (string, error) {
	if cwd := strings.TrimSpace(input.Cwd); cwd != "" {
		return cwd, nil
	}
	if cwd := strings.TrimSpace(command.WorkingDir); cwd != "" {
		return cwd, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return cwd, nil
}

func (s *acpClientSession) openACPConversation(ctx context.Context, resumeSessionID string) error {
	if resumeSessionID = strings.TrimSpace(resumeSessionID); resumeSessionID != "" && s.initResponse.AgentCapabilities.LoadSession {
		resp, err := acp.SendRequest[acp.LoadSessionResponse](s.conn, ctx, acp.AgentMethodSessionLoad, acp.LoadSessionRequest{
			SessionId:  acp.SessionId(resumeSessionID),
			Cwd:        s.cwd,
			McpServers: []acp.McpServer{},
		})
		if err == nil {
			s.applySessionResponse(string(resumeSessionID), resp.Modes, resp.Models)
			return nil
		}
	}

	resp, err := acp.SendRequest[acp.NewSessionResponse](s.conn, ctx, acp.AgentMethodSessionNew, acp.NewSessionRequest{
		Cwd:        s.cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return err
	}
	s.applySessionResponse(string(resp.SessionId), resp.Modes, resp.Models)
	return nil
}

func (s *acpClientSession) applySessionResponse(sessionID string, modes *acp.SessionModeState, models *acp.SessionModelState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeHandle.BackendSessionID = strings.TrimSpace(sessionID)
	s.runtimeHandle.AgentSessionID = strings.TrimSpace(sessionID)
	if modes != nil {
		s.currentMode = strings.TrimSpace(string(modes.CurrentModeId))
	}
	if models != nil {
		s.currentModel = strings.TrimSpace(string(models.CurrentModelId))
	}
}

func (s *acpClientSession) matches(input core.AcpEnsureSessionInput) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.processExited {
		return false
	}
	if strings.TrimSpace(input.Cwd) != "" && strings.TrimSpace(input.Cwd) != strings.TrimSpace(s.cwd) {
		return false
	}
	return s.mode == input.Mode
}

func (s *acpClientSession) supportsModes() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentMode != ""
}

func (s *acpClientSession) setCurrentMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentMode = strings.TrimSpace(mode)
}

func (s *acpClientSession) setCurrentModel(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentModel = strings.TrimSpace(model)
}

func (s *acpClientSession) setOnEvent(onEvent func(core.AcpRuntimeEvent) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onEvent = onEvent
}

func (s *acpClientSession) snapshotHandle() core.AcpRuntimeHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeHandle
}

func (s *acpClientSession) status() core.AcpRuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	details := map[string]any{
		"cwd":        s.cwd,
		"provider":   s.runtime.provider,
		"command":    strings.TrimSpace(s.runtime.command.Command),
		"sessionKey": s.sessionKey,
	}
	if s.command != nil && s.command.Process != nil {
		details["pid"] = s.command.Process.Pid
	}
	if s.currentMode != "" {
		details["currentMode"] = s.currentMode
	}
	if s.currentModel != "" {
		details["currentModel"] = s.currentModel
	}
	if s.processExited {
		details["status"] = "dead"
		if s.processExitErr != nil {
			details["exitError"] = s.processExitErr.Error()
		}
		if stderr := strings.TrimSpace(s.stderr.String()); stderr != "" {
			details["stderr"] = stderr
		}
		return core.AcpRuntimeStatus{
			Summary:          "status=dead",
			BackendSessionID: s.runtimeHandle.BackendSessionID,
			AgentSessionID:   s.runtimeHandle.AgentSessionID,
			Details:          details,
		}
	}
	details["status"] = "ready"
	return core.AcpRuntimeStatus{
		Summary:          "ready",
		BackendSessionID: s.runtimeHandle.BackendSessionID,
		AgentSessionID:   s.runtimeHandle.AgentSessionID,
		Details:          details,
	}
}

func (s *acpClientSession) isAlive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.processExited
}

func (s *acpClientSession) waitForExit(cancel context.CancelFunc) {
	err := s.command.Wait()
	cancel()
	s.mu.Lock()
	s.processExited = true
	s.processExitErr = err
	s.processExitAt = time.Now().UTC()
	s.mu.Unlock()
	close(s.exitedCh)
}

func (s *acpClientSession) cancel(ctx context.Context, _ string) error {
	return s.conn.SendNotification(ctx, acp.AgentMethodSessionCancel, acp.CancelNotification{
		SessionId: acp.SessionId(utils.NonEmpty(s.snapshotHandle().AgentSessionID, s.snapshotHandle().BackendSessionID)),
	})
}

func (s *acpClientSession) close(ctx context.Context, reason string) error {
	_ = s.cancel(ctx, reason)
	s.mu.Lock()
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.command != nil && s.command.Process != nil && !s.processExited {
		_ = s.command.Process.Kill()
	}
	exitedCh := s.exitedCh
	s.mu.Unlock()
	if exitedCh == nil {
		return nil
	}
	select {
	case <-exitedCh:
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
	}
	return nil
}

func (s *acpClientSession) handleInbound(ctx context.Context, method string, params json.RawMessage) (any, *acp.RequestError) {
	switch method {
	case acp.ClientMethodFsReadTextFile:
		var req acp.ReadTextFileRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		resp, err := s.readTextFile(req)
		if err != nil {
			return nil, acp.NewInternalError(map[string]any{"error": err.Error()})
		}
		return resp, nil
	case acp.ClientMethodFsWriteTextFile:
		var req acp.WriteTextFileRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		resp, err := s.writeTextFile(req)
		if err != nil {
			return nil, acp.NewInternalError(map[string]any{"error": err.Error()})
		}
		return resp, nil
	case acp.ClientMethodSessionRequestPermission:
		var req acp.RequestPermissionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		return choosePermissionOption(req, s.cwd), nil
	case acp.ClientMethodSessionUpdate:
		var req acp.SessionNotification
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		if err := s.handleSessionNotification(ctx, req); err != nil {
			return nil, acp.NewInternalError(map[string]any{"error": err.Error()})
		}
		return nil, nil
	default:
		return nil, acp.NewMethodNotFound(method)
	}
}

func (s *acpClientSession) handleSessionNotification(_ context.Context, notification acp.SessionNotification) error {
	s.mu.Lock()
	sessionID := strings.TrimSpace(string(notification.SessionId))
	if sessionID != "" {
		s.runtimeHandle.BackendSessionID = sessionID
		s.runtimeHandle.AgentSessionID = sessionID
	}
	s.mu.Unlock()

	update := notification.Update
	switch {
	case update.AgentMessageChunk != nil:
		return s.emit(contentBlockToEvent(update.AgentMessageChunk.Content, "output"))
	case update.AgentThoughtChunk != nil:
		return s.emit(contentBlockToEvent(update.AgentThoughtChunk.Content, "thought"))
	case update.ToolCall != nil:
		title := strings.TrimSpace(update.ToolCall.Title)
		return s.emit(core.AcpRuntimeEvent{
			Type:       "tool_call",
			Text:       title,
			Title:      title,
			ToolCallID: string(update.ToolCall.ToolCallId),
			Status:     string(update.ToolCall.Status),
		})
	case update.ToolCallUpdate != nil:
		title := ""
		if update.ToolCallUpdate.Title != nil {
			title = strings.TrimSpace(*update.ToolCallUpdate.Title)
		}
		status := ""
		if update.ToolCallUpdate.Status != nil {
			status = string(*update.ToolCallUpdate.Status)
		}
		return s.emit(core.AcpRuntimeEvent{
			Type:       "tool_call",
			Text:       utils.NonEmpty(title, string(update.ToolCallUpdate.ToolCallId)),
			Title:      title,
			ToolCallID: string(update.ToolCallUpdate.ToolCallId),
			Status:     status,
		})
	case update.CurrentModeUpdate != nil:
		s.setCurrentMode(string(update.CurrentModeUpdate.CurrentModeId))
		return nil
	default:
		return nil
	}
}

func (s *acpClientSession) emit(event core.AcpRuntimeEvent) error {
	s.mu.Lock()
	onEvent := s.onEvent
	s.mu.Unlock()
	if onEvent == nil {
		return nil
	}
	return onEvent(event)
}

func (s *acpClientSession) readTextFile(req acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	if !filepath.IsAbs(req.Path) {
		return acp.ReadTextFileResponse{}, fmt.Errorf("path must be absolute: %s", req.Path)
	}
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	content := string(data)
	if req.Line != nil || req.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if req.Line != nil && *req.Line > 0 {
			start = min(max(*req.Line-1, 0), len(lines))
		}
		end := len(lines)
		if req.Limit != nil && *req.Limit > 0 && start+*req.Limit < end {
			end = start + *req.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

func (s *acpClientSession) writeTextFile(req acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if !filepath.IsAbs(req.Path) {
		return acp.WriteTextFileResponse{}, fmt.Errorf("path must be absolute: %s", req.Path)
	}
	if err := os.MkdirAll(filepath.Dir(req.Path), 0o755); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	if err := os.WriteFile(req.Path, []byte(req.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	return acp.WriteTextFileResponse{}, nil
}

func choosePermissionOption(req acp.RequestPermissionRequest, cwd string) acp.RequestPermissionResponse {
	if len(req.Options) == 0 {
		return cancelledPermission()
	}
	toolName := resolveToolNameForPermission(req)
	toolTitle := strings.TrimSpace(stringPointerValue(req.ToolCall.Title))
	allowOption := pickPermissionOption(req.Options, acp.PermissionOptionKindAllowOnce, acp.PermissionOptionKindAllowAlways)
	rejectOption := pickPermissionOption(req.Options, acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways)

	if shouldAutoApproveToolCall(req, toolName, toolTitle, cwd) {
		if allowOption != nil {
			return selectedPermissionOption(allowOption.OptionId)
		}
		return cancelledPermission()
	}
	if rejectOption != nil {
		return selectedPermissionOption(rejectOption.OptionId)
	}
	return cancelledPermission()
}

func selectedPermissionOption(optionID acp.PermissionOptionId) acp.RequestPermissionResponse {
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Selected: &acp.RequestPermissionOutcomeSelected{OptionId: optionID},
		},
	}
}

func cancelledPermission() acp.RequestPermissionResponse {
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Cancelled: &acp.RequestPermissionOutcomeCancelled{},
		},
	}
}

func pickPermissionOption(options []acp.PermissionOption, kinds ...acp.PermissionOptionKind) *acp.PermissionOption {
	for _, kind := range kinds {
		for i := range options {
			if options[i].Kind == kind {
				return &options[i]
			}
		}
	}
	return nil
}

func shouldAutoApproveToolCall(req acp.RequestPermissionRequest, toolName string, toolTitle string, cwd string) bool {
	normalized := normalizeToolName(toolName)
	if normalized == "" {
		return false
	}
	if _, ok := safeAutoApproveToolIDs[normalized]; !ok {
		if _, ok := trustedSafeToolAliases[normalized]; !ok {
			return false
		}
	}
	if normalized == "read" {
		return isReadToolCallScopedToCwd(req, normalized, toolTitle, cwd)
	}
	return true
}

func isReadToolCallScopedToCwd(req acp.RequestPermissionRequest, toolName string, toolTitle string, cwd string) bool {
	if toolName != "read" {
		return false
	}
	rawPath := resolveToolPathCandidate(req, toolName, toolTitle)
	if rawPath == "" {
		return false
	}
	absolutePath := resolveAbsoluteScopedPath(rawPath, cwd)
	if absolutePath == "" {
		return false
	}
	return isPathWithinRoot(absolutePath, filepath.Clean(cwd))
}

func resolveToolNameForPermission(req acp.RequestPermissionRequest) string {
	metaName := normalizeToolName(readFirstStringValue(asRecord(req.ToolCall.Meta), "toolName", "tool_name", "name"))
	rawInputName := normalizeToolName(readFirstStringValue(asRecord(req.ToolCall.RawInput), "tool", "toolName", "tool_name", "name"))
	titleName := normalizeToolName(parseToolNameFromTitle(stringPointerValue(req.ToolCall.Title)))
	if metaName != "" && titleName != "" && metaName != titleName {
		return ""
	}
	if rawInputName != "" && metaName != "" && rawInputName != metaName {
		return ""
	}
	if rawInputName != "" && titleName != "" && rawInputName != titleName {
		return ""
	}
	return utils.NonEmpty(metaName, utils.NonEmpty(titleName, rawInputName))
}

func parseToolNameFromTitle(title string) string {
	if title == "" {
		return ""
	}
	head := strings.TrimSpace(strings.SplitN(title, ":", 2)[0])
	return head
}

func normalizeToolName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return ""
	}
	return value
}

func resolveToolPathCandidate(req acp.RequestPermissionRequest, toolName string, toolTitle string) string {
	rawInput := asRecord(req.ToolCall.RawInput)
	if path := readFirstStringValue(rawInput, readToolPathKeys...); path != "" {
		return path
	}
	return extractPathFromToolTitle(toolTitle, toolName)
}

func extractPathFromToolTitle(toolTitle string, toolName string) string {
	if toolTitle == "" {
		return ""
	}
	separator := strings.Index(toolTitle, ":")
	if separator < 0 {
		return ""
	}
	tail := strings.TrimSpace(toolTitle[separator+1:])
	if tail == "" {
		return ""
	}
	if toolName == "read" {
		return tail
	}
	return ""
}

func resolveAbsoluteScopedPath(value string, cwd string) string {
	candidate := strings.TrimSpace(value)
	if candidate == "" {
		return ""
	}
	if filepath.IsAbs(candidate) {
		return filepath.Clean(candidate)
	}
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(cwd, candidate))
}

func isPathWithinRoot(candidatePath string, root string) bool {
	relative, err := filepath.Rel(root, candidatePath)
	if err != nil {
		return false
	}
	return relative == "." || (!strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative))
}

func asRecord(value any) map[string]any {
	record, _ := value.(map[string]any)
	return record
}

func readFirstStringValue(source map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func contentBlockToEvent(block acp.ContentBlock, stream string) core.AcpRuntimeEvent {
	text := ""
	switch {
	case block.Text != nil:
		text = block.Text.Text
	case block.ResourceLink != nil:
		text = utils.NonEmpty(block.ResourceLink.Name, block.ResourceLink.Uri)
	case block.Resource != nil && block.Resource.Resource.TextResourceContents != nil:
		text = block.Resource.Resource.TextResourceContents.Text
	}
	return core.AcpRuntimeEvent{
		Type:   "text_delta",
		Text:   strings.TrimSpace(text),
		Stream: stream,
	}
}
