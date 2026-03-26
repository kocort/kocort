package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kocort/kocort/internal/acp"
	backendpkg "github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

func TestRuntimeACPControlSurfaceUsesBackendManager(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	manager := acp.NewAcpSessionManager()
	ensureCalls := 0
	setConfigCalls := 0
	runtimeImpl := fakeAcpRuntime{
		ensureSession: func(_ context.Context, input core.AcpEnsureSessionInput) (core.AcpRuntimeHandle, error) {
			ensureCalls++
			return core.AcpRuntimeHandle{
				SessionKey:         input.SessionKey,
				Backend:            "acp-live",
				RuntimeSessionName: "worker-runtime",
				BackendSessionID:   "backend-1",
				AgentSessionID:     "agent-1",
				Cwd:                input.Cwd,
			}, nil
		},
		runTurn: func(_ context.Context, input core.AcpRunTurnInput) error {
			if input.OnEvent != nil {
				_ = input.OnEvent(core.AcpRuntimeEvent{Type: "done", StopReason: "completed"})
			}
			return nil
		},
		getCaps: func(_ context.Context, _ *core.AcpRuntimeHandle) (core.AcpRuntimeCapabilities, error) {
			return core.AcpRuntimeCapabilities{
				Controls:         []core.AcpRuntimeControl{core.AcpControlSetConfigOption, core.AcpControlSetMode, core.AcpControlStatus},
				ConfigOptionKeys: []string{"timeout"},
			}, nil
		},
		getStatus: func(_ context.Context, handle core.AcpRuntimeHandle) (core.AcpRuntimeStatus, error) {
			return core.AcpRuntimeStatus{
				Summary:          "alive",
				BackendSessionID: handle.BackendSessionID,
				AgentSessionID:   handle.AgentSessionID,
			}, nil
		},
		setConfig: func(_ context.Context, input core.AcpSetConfigOptionInput) error {
			if input.Key != "timeout" || input.Value != "30" {
				t.Fatalf("unexpected config update: %+v", input)
			}
			setConfigCalls++
			return nil
		},
	}
	cfg := config.AppConfig{
		ACP: config.AcpConfigLite{
			Enabled: true,
			Backend: "acp-live",
		},
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"acp-live": {API: "acp"},
			},
		},
	}
	acpBackend := &backendpkg.ACPBackend{
		Config:   cfg,
		Provider: "acp-live",
		Mgr:      manager,
		Runtime:  runtimeImpl,
	}
	rt := &Runtime{
		Config:   cfg,
		Sessions: store,
		Backends: staticBackendResolver{backend: acpBackend, kind: "acp"},
	}
	sessionKey := "agent:main:acp:test"
	if _, _, err := manager.InitializeSession(context.Background(), store, runtimeImpl, sessionKey, "main", core.AcpSessionModePersistent, filepath.Join(t.TempDir(), "acp"), "acp-live"); err != nil {
		t.Fatalf("initialize session: %v", err)
	}

	sessions := rt.ListACPSessions()
	if len(sessions) != 1 || sessions[0].SessionKey != sessionKey {
		t.Fatalf("unexpected ACP sessions snapshot: %+v", sessions)
	}

	status, err := rt.GetACPSessionStatus(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("get ACP status: %v", err)
	}
	if status.Backend != "acp-live" || status.RuntimeStatus == nil || status.RuntimeStatus.Summary != "alive" {
		t.Fatalf("unexpected ACP status: %+v", status)
	}

	observability, err := rt.GetACPObservability(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("get ACP observability: %v", err)
	}
	if observability.SessionKey != sessionKey || observability.RuntimeStatus == nil || observability.RuntimeStatus.Summary != "alive" {
		t.Fatalf("unexpected ACP observability: %+v", observability)
	}

	controlResult, err := rt.ControlACPSession(context.Background(), acp.AcpSessionControlInput{
		SessionKey: sessionKey,
		Action:     "set-config",
		Key:        "timeout",
		Value:      "30",
	})
	if err != nil {
		t.Fatalf("control ACP session: %v", err)
	}
	if !controlResult.Success || setConfigCalls != 1 {
		t.Fatalf("unexpected control result: %+v calls=%d", controlResult, setConfigCalls)
	}

	entry := store.Entry(sessionKey)
	entry.ACP.State = "error"
	if err := store.Upsert(sessionKey, *entry); err != nil {
		t.Fatalf("persist errored ACP state: %v", err)
	}
	resumeResults := rt.ResumeAllPersistentACPSessions(context.Background())
	if len(resumeResults) != 1 || !resumeResults[0].Resumed {
		t.Fatalf("unexpected ACP resume results: %+v", resumeResults)
	}
	if ensureCalls < 2 {
		t.Fatalf("expected resume path to re-ensure runtime handle, got %d ensure calls", ensureCalls)
	}
}
