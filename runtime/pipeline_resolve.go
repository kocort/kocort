// pipeline_resolve.go — Stage 2: Resolve identity, session, and handle
// session commands (reset / compact).
//
// Corresponds to the original Run() lines ~57–107.
// Resolves the agent identity and session, handles session reset/compaction
// commands that short-circuit the run, injects the timestamp, and appends
// the incoming user transcript.
package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	sessionpkg "github.com/kocort/kocort/internal/session"
)

// pipelineResolveResult wraps the result when resolve short-circuits.
type pipelineResolveResult struct {
	Result core.AgentRunResult
	Err    error
}

// resolve resolves the agent identity and session, then handles any
// session commands (reset, compaction) that may short-circuit the pipeline.
// If a short-circuit occurs, the returned *pipelineResolveResult is non-nil.
func (p *AgentPipeline) resolve(ctx context.Context, state *PipelineState) (*pipelineResolveResult, error) {
	r := p.runtime
	req := &state.Request

	// ---- Identity resolution ----
	identity, err := r.Identities.Resolve(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	state.Identity = identity

	// Default timezone / timeout from identity
	if req.UserTimezone == "" {
		req.UserTimezone = identity.UserTimezone
	}
	if req.Timeout <= 0 && identity.TimeoutSeconds > 0 {
		req.Timeout = time.Duration(identity.TimeoutSeconds) * time.Second
	}

	// Preserve the raw message before timestamp injection
	state.RawMessage = strings.TrimSpace(req.Message)

	// ---- Session resolution ----
	sess, err := r.Sessions.ResolveForRequest(ctx, sessionpkg.SessionResolveOptions{
		AgentID:             req.AgentID,
		SessionKey:          req.SessionKey,
		SessionID:           req.SessionID,
		To:                  req.To,
		Channel:             req.Channel,
		ThreadID:            req.ThreadID,
		ChatType:            req.ChatType,
		MainKey:             config.ResolveSessionMainKey(r.Config),
		DMScope:             config.ResolveSessionDMScope(r.Config),
		ParentForkMaxTokens: config.ResolveSessionParentForkMaxTokens(r.Config),
		Now:                 time.Now().UTC(),
		ResetPolicy:         sessionpkg.ResolveFreshnessPolicyForSession(r.Config, req.SessionKey, req.ChatType, req.Channel, req.ThreadID),
	})
	if err != nil {
		return nil, err
	}
	state.Session = sess
	req.SessionKey = sess.SessionKey
	req.SessionID = sess.SessionID

	// ---- Record events ----
	event.RecordRuntimeEvent(ctx, r.Audit, r.Logger,
		identity.ID, sess.SessionKey, req.RunID,
		"session_resolved", "info", "runtime session resolved", map[string]any{
			"sessionId": sess.SessionID,
			"isNew":     sess.IsNew,
			"fresh":     sess.Fresh,
			"channel":   req.Channel,
			"to":        req.To,
			"threadId":  req.ThreadID,
			"lane":      req.Lane,
		})
	if r.Tasks != nil {
		_ = r.Tasks.MarkRunStarted(req.TaskID, req.RunID, sess.SessionKey) // best-effort; failure is non-critical
	}
	event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "lifecycle", map[string]any{
		"type":      "session_resolved",
		"sessionId": sess.SessionID,
		"isNew":     sess.IsNew,
		"fresh":     sess.Fresh,
		"channel":   req.Channel,
		"to":        req.To,
		"threadId":  req.ThreadID,
		"lane":      req.Lane,
	})

	// ---- Handle session command ----
	if command, ok := sessionpkg.ParseSessionCommandForChatType(config.ResolveSessionResetTriggers(r.Config), state.RawMessage, req.ChatType); ok {
		switch command.Kind {
		case sessionpkg.SessionCommandReset:
			if handled, result, herr := r.handleSessionResetCommand(ctx, *req, *command.Reset, sess, identity); handled {
				return &pipelineResolveResult{Result: result, Err: herr}, nil
			}
		case sessionpkg.SessionCommandCompact:
			if handled, result, herr := r.handleSessionCompactionCommand(ctx, *req, *command.Compact, sess, identity); handled {
				return &pipelineResolveResult{Result: result, Err: herr}, nil
			}
		}
	}

	// ---- Inject timestamp and append user transcript ----
	req.Message = infra.InjectTimestamp(req.Message, req.UserTimezone, time.Now().UTC())
	// Compute the effective workspace dir for persisting user attachment media.
	workspaceDir := strings.TrimSpace(identity.WorkspaceDir)
	if workspaceDir == "" {
		workspaceDir = infra.ResolveDefaultAgentWorkspaceDirForState(r.Sessions.BaseDir(), identity.ID)
	}
	if err := sessionpkg.AppendIncomingUserTranscript(r.Sessions, sess, req, time.Now().UTC(), workspaceDir); err != nil {
		return nil, err
	}

	return nil, nil
}
