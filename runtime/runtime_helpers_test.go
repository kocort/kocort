package runtime

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/tool"
)

func newLoopbackHTTPServer(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listen unavailable in this environment: %v", err)
	}
	srv := &http.Server{Handler: handler}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(listener)
	}()
	baseURL := (&url.URL{Scheme: "http", Host: listener.Addr().String()}).String()
	return baseURL, func() {
		_ = srv.Close()
		_ = listener.Close()
		wg.Wait()
	}
}

type fakeBackend struct {
	onRun func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error)
}

func (b fakeBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	return b.onRun(ctx, runCtx)
}

type fakeToolPlanner struct {
	next func(ctx context.Context, runCtx rtypes.AgentRunContext, state core.ToolPlannerState) (core.ToolPlan, error)
}

func (p fakeToolPlanner) Next(ctx context.Context, runCtx rtypes.AgentRunContext, state core.ToolPlannerState) (core.ToolPlan, error) {
	return p.next(ctx, runCtx, state)
}

type memoryProviderFunc func(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, message string) ([]core.MemoryHit, error)

func (f memoryProviderFunc) Recall(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, message string) ([]core.MemoryHit, error) {
	return f(ctx, identity, session, message)
}

type testTool struct {
	name       string
	resultText string
}

type stubTool struct {
	name        string
	description string
	meta        core.ToolRegistrationMeta
	execute     func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error)
}

type fakeAcpRuntime struct {
	ensureSession func(ctx context.Context, input core.AcpEnsureSessionInput) (core.AcpRuntimeHandle, error)
	runTurn       func(ctx context.Context, input core.AcpRunTurnInput) error
	getCaps       func(ctx context.Context, handle *core.AcpRuntimeHandle) (core.AcpRuntimeCapabilities, error)
	getStatus     func(ctx context.Context, handle core.AcpRuntimeHandle) (core.AcpRuntimeStatus, error)
	setMode       func(ctx context.Context, input core.AcpSetModeInput) error
	setConfig     func(ctx context.Context, input core.AcpSetConfigOptionInput) error
	cancel        func(ctx context.Context, input core.AcpCancelInput) error
	close         func(ctx context.Context, input core.AcpCloseInput) error
}

type stubToolApprovalRunner struct {
	approve func(ctx context.Context, req tool.ToolApprovalRequest) (tool.ToolApprovalDecision, error)
}

func (s stubToolApprovalRunner) ApproveToolExecution(ctx context.Context, req tool.ToolApprovalRequest) (tool.ToolApprovalDecision, error) {
	if s.approve != nil {
		return s.approve(ctx, req)
	}
	return tool.ToolApprovalDecision{Allowed: true}, nil
}

func (f fakeAcpRuntime) EnsureSession(ctx context.Context, input core.AcpEnsureSessionInput) (core.AcpRuntimeHandle, error) {
	return f.ensureSession(ctx, input)
}

func (f fakeAcpRuntime) RunTurn(ctx context.Context, input core.AcpRunTurnInput) error {
	return f.runTurn(ctx, input)
}

func (f fakeAcpRuntime) GetCapabilities(ctx context.Context, handle *core.AcpRuntimeHandle) (core.AcpRuntimeCapabilities, error) {
	if f.getCaps != nil {
		return f.getCaps(ctx, handle)
	}
	return core.AcpRuntimeCapabilities{}, nil
}

func (f fakeAcpRuntime) GetStatus(ctx context.Context, handle core.AcpRuntimeHandle) (core.AcpRuntimeStatus, error) {
	if f.getStatus != nil {
		return f.getStatus(ctx, handle)
	}
	return core.AcpRuntimeStatus{}, nil
}

func (f fakeAcpRuntime) SetMode(ctx context.Context, input core.AcpSetModeInput) error {
	if f.setMode != nil {
		return f.setMode(ctx, input)
	}
	return nil
}

func (f fakeAcpRuntime) SetConfigOption(ctx context.Context, input core.AcpSetConfigOptionInput) error {
	if f.setConfig != nil {
		return f.setConfig(ctx, input)
	}
	return nil
}

func (f fakeAcpRuntime) Cancel(ctx context.Context, input core.AcpCancelInput) error {
	if f.cancel != nil {
		return f.cancel(ctx, input)
	}
	return nil
}

func (f fakeAcpRuntime) Close(ctx context.Context, input core.AcpCloseInput) error {
	if f.close != nil {
		return f.close(ctx, input)
	}
	return nil
}

func (t *testTool) Name() string {
	return t.name
}

func (t *testTool) Description() string {
	return t.name
}

func (t *testTool) Execute(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
	return core.ToolResult{Text: t.resultText}, nil
}

func (t *stubTool) Name() string {
	return t.name
}

func (t *stubTool) Description() string {
	if strings.TrimSpace(t.description) != "" {
		return t.description
	}
	return t.name
}

func (t *stubTool) Execute(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
	if t.execute != nil {
		return t.execute(ctx, toolCtx, args)
	}
	return core.ToolResult{}, nil
}

func (t *stubTool) ToolRegistrationMeta() core.ToolRegistrationMeta {
	return t.meta
}

type stubRuntimePlugin struct {
	id    string
	tools []tool.Tool
	err   error
}

func (p stubRuntimePlugin) ID() string {
	return p.id
}

func (p stubRuntimePlugin) Tools(ctx context.Context, pluginCtx RuntimePluginToolContext) ([]tool.Tool, error) {
	if p.err != nil {
		return nil, p.err
	}
	return append([]tool.Tool{}, p.tools...), nil
}

func waitForCondition(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func storeForTests(t *testing.T) *session.SessionStore {
	t.Helper()
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	return store
}
