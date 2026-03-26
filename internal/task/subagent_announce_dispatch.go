package task

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------

// SubagentDeliveryPath enumerates the possible routes for an announcement.
type SubagentDeliveryPath = string

const (
	DeliveryPathSteered SubagentDeliveryPath = "steered"
	DeliveryPathQueued  SubagentDeliveryPath = "queued"
	DeliveryPathDirect  SubagentDeliveryPath = "direct"
	DeliveryPathNone    SubagentDeliveryPath = "none"
)

// SubagentAnnounceDispatchPhase names a single attempt within the dispatch.
type SubagentAnnounceDispatchPhase string

const (
	DispatchPhaseQueuePrimary  SubagentAnnounceDispatchPhase = "queue-primary"
	DispatchPhaseDirectPrimary SubagentAnnounceDispatchPhase = "direct-primary"
	DispatchPhaseQueueFallback SubagentAnnounceDispatchPhase = "queue-fallback"
)

// SubagentAnnounceDispatchPhaseResult captures one phase attempt.
type SubagentAnnounceDispatchPhaseResult struct {
	Phase     SubagentAnnounceDispatchPhase
	Delivered bool
	Path      SubagentDeliveryPath
	Error     error
}

// SubagentAnnounceDeliveryResult is the final result of the dispatch.
type SubagentAnnounceDeliveryResult struct {
	Delivered bool
	Path      SubagentDeliveryPath
	Error     error
	Phases    []SubagentAnnounceDispatchPhaseResult
}

// SubagentAnnounceDispatchParams packages everything needed to run the
// three-path dispatch: steer → queue → direct (or direct → queue, depending
// on expectsCompletionMessage).
type SubagentAnnounceDispatchParams struct {
	RunID                    string
	RequesterSessionKey      string
	ExpectsCompletionMessage bool
	Announcement             SubagentAnnouncement
	// Steer injects the message into the requester's currently active run.
	// Returns true if the steer succeeded (requester was running & accepted it).
	Steer func(ctx context.Context, sessionKey string, req core.AgentRunRequest) bool
	// Queue enqueues the message into the follow-up queue for the requester.
	// Returns ("steered"|"queued"|"none", error).
	Queue func(ctx context.Context, req core.AgentRunRequest) (string, error)
	// Direct sends a new agent run as a direct message.
	// Returns error if the run fails.
	Direct func(ctx context.Context, req core.AgentRunRequest) error
}

// dispatch strategy:
//
//   - If !expectsCompletionMessage: try steer → queue-primary → direct-primary
//   - If  expectsCompletionMessage: try steer → direct-primary → queue-fallback
//
// Steer is always attempted first as a zero-cost injection into an active run.
func RunSubagentAnnounceDispatch(ctx context.Context, params SubagentAnnounceDispatchParams) SubagentAnnounceDeliveryResult {
	phases := make([]SubagentAnnounceDispatchPhaseResult, 0, 3)

	// Phase 0 (implicit): try steer — inject into active run if possible.
	if params.Steer != nil {
		steerReq := params.Announcement.PrimaryRequest
		steerReq.QueueMode = core.QueueModeSteer
		if params.Steer(ctx, params.RequesterSessionKey, steerReq) {
			return SubagentAnnounceDeliveryResult{
				Delivered: true,
				Path:      DeliveryPathSteered,
				Phases:    phases,
			}
		}
	}

	if ctx.Err() != nil {
		return SubagentAnnounceDeliveryResult{Error: ctx.Err(), Phases: phases}
	}

	if !params.ExpectsCompletionMessage {
		// Strategy: queue-primary → direct-primary
		if params.Queue != nil {
			outcome, err := params.Queue(ctx, params.Announcement.PrimaryRequest)
			phase := SubagentAnnounceDispatchPhaseResult{Phase: DispatchPhaseQueuePrimary}
			if err == nil && (outcome == DeliveryPathQueued || outcome == DeliveryPathSteered) {
				phase.Delivered = true
				phase.Path = outcome
				phases = append(phases, phase)
				return SubagentAnnounceDeliveryResult{
					Delivered: true,
					Path:      outcome,
					Phases:    phases,
				}
			}
			phase.Error = err
			phases = append(phases, phase)
		}
		// Fallback: direct
		if params.Direct != nil {
			req := buildDirectAnnouncementRequest(buildRecordFromAnnouncement(params.Announcement))
			phase := SubagentAnnounceDispatchPhaseResult{Phase: DispatchPhaseDirectPrimary}
			if err := params.Direct(ctx, req); err == nil {
				phase.Delivered = true
				phase.Path = DeliveryPathDirect
				phases = append(phases, phase)
				return SubagentAnnounceDeliveryResult{
					Delivered: true,
					Path:      DeliveryPathDirect,
					Phases:    phases,
				}
			} else {
				phase.Error = err
				phases = append(phases, phase)
				return SubagentAnnounceDeliveryResult{
					Error:  err,
					Phases: phases,
				}
			}
		}
		return SubagentAnnounceDeliveryResult{Phases: phases}
	}

	// expectsCompletionMessage = true → direct-primary → queue-fallback
	if params.Direct != nil {
		phase := SubagentAnnounceDispatchPhaseResult{Phase: DispatchPhaseDirectPrimary}
		if err := params.Direct(ctx, params.Announcement.PrimaryRequest); err == nil {
			phase.Delivered = true
			phase.Path = DeliveryPathDirect
			phases = append(phases, phase)
			return SubagentAnnounceDeliveryResult{
				Delivered: true,
				Path:      DeliveryPathDirect,
				Phases:    phases,
			}
		} else {
			phase.Error = err
			phases = append(phases, phase)
		}
	}

	if ctx.Err() != nil {
		return SubagentAnnounceDeliveryResult{Error: ctx.Err(), Phases: phases}
	}

	// Fallback: queue
	if params.Queue != nil {
		queueReq := params.Announcement.PrimaryRequest
		queueReq.ShouldFollowup = true
		queueReq.QueueMode = core.QueueModeFollowup
		outcome, err := params.Queue(ctx, queueReq)
		phase := SubagentAnnounceDispatchPhaseResult{Phase: DispatchPhaseQueueFallback}
		if err == nil && (outcome == DeliveryPathQueued || outcome == DeliveryPathSteered) {
			phase.Delivered = true
			phase.Path = outcome
			phases = append(phases, phase)
			return SubagentAnnounceDeliveryResult{
				Delivered: true,
				Path:      outcome,
				Phases:    phases,
			}
		}
		phase.Error = err
		phases = append(phases, phase)
	}

	// All paths exhausted — return the primary direct error if available.
	var primaryErr error
	for _, p := range phases {
		if p.Phase == DispatchPhaseDirectPrimary && p.Error != nil {
			primaryErr = p.Error
			break
		}
	}
	return SubagentAnnounceDeliveryResult{
		Error:  primaryErr,
		Phases: phases,
	}
}

// buildRecordFromAnnouncement is a shim that reconstructs just enough of a
// SubagentRunRecord for the existing buildDirectAnnouncementRequest helper.
func buildRecordFromAnnouncement(a SubagentAnnouncement) SubagentRunRecord {
	rec := SubagentRunRecord{
		RunID:               a.RunID,
		RequesterSessionKey: a.RequesterSessionKey,
	}
	// Extract route info from the primary request if available.
	if strings.TrimSpace(a.PrimaryRequest.Channel) != "" {
		rec.RequesterOrigin = &core.DeliveryContext{
			Channel:   a.PrimaryRequest.Channel,
			To:        a.PrimaryRequest.To,
			AccountID: a.PrimaryRequest.AccountID,
			ThreadID:  a.PrimaryRequest.ThreadID,
		}
	}
	return rec
}
