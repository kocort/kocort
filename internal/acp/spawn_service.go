package acp

import (
	"context"
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/core"
	sessionpkg "github.com/kocort/kocort/internal/session"
	spawnpolicypkg "github.com/kocort/kocort/internal/spawnpolicy"
)

// SpawnCoordinator orchestrates ACP child-session launches while keeping the
// caller thin. Runtime wires concrete dependencies into this control-plane
// helper and only keeps a small wrapper.
type SpawnCoordinator struct {
	Sessions   *sessionpkg.SessionStore
	Identities core.IdentityResolver
	Deliverer  core.Deliverer
}

// SpawnHooks provides the runtime-owned operations needed to launch and
// finalize an ACP child run without coupling this package back to runtime.
type SpawnHooks struct {
	ValidateTarget   func(core.AgentIdentity, string, string) error
	RegisterLaunch   func(SessionSpawnRequest, SessionLaunchPlan)
	Run              func(context.Context, core.AgentRunRequest) (core.AgentRunResult, error)
	FinalizeChildRun func(context.Context, core.AgentRunRequest, core.AgentRunResult, error)
}

// SpawnSession creates and starts an ACP child session.
func (c SpawnCoordinator) SpawnSession(ctx context.Context, req SessionSpawnRequest, hooks SpawnHooks) (SessionSpawnResult, error) {
	if c.Sessions == nil {
		return SessionSpawnResult{}, fmt.Errorf("session store is not configured")
	}
	if hooks.Run == nil {
		return SessionSpawnResult{}, fmt.Errorf("acp spawn run hook is not configured")
	}

	requesterIdentity, err := c.Identities.Resolve(ctx, req.RequesterAgentID)
	if err == nil {
		if hooks.ValidateTarget != nil {
			if policyErr := hooks.ValidateTarget(requesterIdentity, req.RequesterAgentID, req.TargetAgentID); policyErr != nil {
				return SessionSpawnResult{Status: "forbidden"}, policyErr
			}
		}
	}

	resolvedTargetAgentID := sessionpkg.NormalizeAgentID(req.TargetAgentID)
	if resolvedTargetAgentID == "" {
		resolvedTargetAgentID = sessionpkg.NormalizeAgentID(req.RequesterAgentID)
	}
	targetIdentity, targetErr := c.Identities.Resolve(ctx, resolvedTargetAgentID)
	if targetErr != nil {
		return SessionSpawnResult{}, targetErr
	}
	if !strings.EqualFold(strings.TrimSpace(targetIdentity.RuntimeType), "acp") {
		return SessionSpawnResult{Status: "error"}, fmt.Errorf("sessions_spawn runtime=acp requires an ACP-configured target agent")
	}
	if requesterIdentity.ID != "" {
		if policyErr := spawnpolicypkg.ValidateSpawnRuntimePolicy(requesterIdentity, targetIdentity, req.SandboxMode); policyErr != nil {
			return SessionSpawnResult{Status: "forbidden"}, policyErr
		}
	}
	launch, err := PrepareSessionLaunch(req, &targetIdentity, nil)
	if err != nil {
		return SessionSpawnResult{}, err
	}
	if err := c.Sessions.Upsert(launch.Result.ChildSessionKey, launch.SessionEntry); err != nil {
		return SessionSpawnResult{}, err
	}
	if hooks.RegisterLaunch != nil {
		hooks.RegisterLaunch(req, launch)
	}
	if req.SpawnMode == "session" && req.ThreadRequested {
		idleTimeoutMs, maxAgeMs := sessionpkg.DefaultSessionBindingLifecycle()
		_ = sessionpkg.NewThreadBindingService(c.Sessions).BindThreadSession(sessionpkg.BindThreadSessionInput{
			TargetSessionKey:    launch.Result.ChildSessionKey,
			RequesterSessionKey: req.RequesterSessionKey,
			TargetKind:          "session",
			Placement:           sessionpkg.ThreadBindingPlacementChild,
			Channel:             req.RouteChannel,
			To:                  req.RouteTo,
			AccountID:           req.RouteAccountID,
			ThreadID:            req.RouteThreadID,
			IdleTimeoutMs:       idleTimeoutMs,
			MaxAgeMs:            maxAgeMs,
			Label:               req.Label,
			AgentID:             req.TargetAgentID,
		})
	}

	relay := StartParentStreamRelay(c.Deliverer, req, launch.Result)
	go func() {
		result, runErr := hooks.Run(context.Background(), launch.ChildRequest)
		if relay != nil {
			relay.NotifyCompleted(result, runErr)
		}
		if hooks.FinalizeChildRun != nil {
			hooks.FinalizeChildRun(context.Background(), launch.ChildRequest, result, runErr)
		}
	}()

	return launch.Result, nil
}
