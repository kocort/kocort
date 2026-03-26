package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

type agentsListRuntimeStub struct {
	webRuntimeStub
	identities []core.AgentIdentity
	imageFn    func(context.Context, core.AgentIdentity, core.SessionResolution, core.AgentRunRequest) (core.AgentRunResult, error)
}

func (s *agentsListRuntimeStub) IdentitySnapshot() []core.AgentIdentity {
	return append([]core.AgentIdentity{}, s.identities...)
}

func (s *agentsListRuntimeStub) AnalyzeImage(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, req core.AgentRunRequest) (core.AgentRunResult, error) {
	if s.imageFn != nil {
		return s.imageFn(ctx, identity, session, req)
	}
	return core.AgentRunResult{}, nil
}

func TestAgentsListTool(t *testing.T) {
	tool := NewAgentsListTool()
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: &agentsListRuntimeStub{
			identities: []core.AgentIdentity{
				{ID: "main", Name: "Main"},
				{ID: "worker", Name: "Worker"},
			},
		},
		Run: AgentRunContext{
			Identity: core.AgentIdentity{
				ID:                  "main",
				SubagentAllowAgents: []string{"worker"},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("agents_list: %v", err)
	}
	if !strings.Contains(result.Text, `"id":"worker"`) || strings.Contains(result.Text, `"id":"main"`) {
		t.Fatalf("unexpected agents_list result: %s", result.Text)
	}
}

func TestImageTool(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "photo.png"), []byte("PNGDATA"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	tool := NewImageTool()
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: &agentsListRuntimeStub{
			imageFn: func(_ context.Context, _ core.AgentIdentity, _ core.SessionResolution, req core.AgentRunRequest) (core.AgentRunResult, error) {
				if len(req.Attachments) != 1 || req.Attachments[0].MIMEType != "image/png" {
					t.Fatalf("unexpected image request attachments: %+v", req.Attachments)
				}
				return core.AgentRunResult{
					RunID: "img-run",
					Payloads: []core.ReplyPayload{
						{Text: "Atlas launch pad with BLUE-SPARROW-17 label."},
					},
				}, nil
			},
		},
		Run: AgentRunContext{
			Identity:     core.AgentIdentity{ID: "main", WorkspaceDir: workspace},
			Session:      core.SessionResolution{SessionKey: "agent:main:main"},
			Request:      core.AgentRunRequest{RunID: "run-1"},
			WorkspaceDir: workspace,
		},
	}, map[string]any{"path": "photo.png", "prompt": "describe"})
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	if !strings.Contains(result.Text, `BLUE-SPARROW-17`) {
		t.Fatalf("unexpected image result: %s", result.Text)
	}
}
