// pipeline_validate.go — Stage 1: Validate inputs and populate defaults.
//
// Corresponds to the original Run() lines ~38–56.
// Checks runtime readiness, validates the message, and populates
// default values for AgentID and RunID.
package runtime

import (
	"fmt"
	"strings"

	sessionpkg "github.com/kocort/kocort/internal/session"
)

// validate checks that the runtime is ready and that the request has
// the minimum required fields. It populates default AgentID and RunID
// when they are missing.
func (p *AgentPipeline) validate(state *PipelineState) error {
	r := p.runtime

	// Runtime readiness check
	if r.Sessions == nil || r.Identities == nil || r.Memory == nil ||
		r.Backend == nil || r.Deliverer == nil || r.Subagents == nil ||
		r.Queue == nil || r.ActiveRuns == nil {
		return fmt.Errorf("runtime is not fully configured")
	}

	// Message must not be blank
	if strings.TrimSpace(state.Request.Message) == "" {
		return fmt.Errorf("message is required")
	}

	// Default AgentID from session key, then fall back to default
	if state.Request.AgentID == "" {
		state.Request.AgentID = sessionpkg.ResolveAgentIDFromSessionKey(state.Request.SessionKey)
	}
	if state.Request.AgentID == "" {
		state.Request.AgentID = sessionpkg.DefaultAgentID
	}

	// Generate RunID if not provided
	if state.Request.RunID == "" {
		runID, err := sessionpkg.RandomToken(8)
		if err != nil {
			return err
		}
		state.Request.RunID = runID
	}

	return nil
}
