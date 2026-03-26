package tool

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/task"
)

type subagentTarget struct {
	Index                  int
	Kind                   string
	RunID                  string
	ChildSessionKey        string
	SessionID              string
	RequesterDisplayKey    string
	Label                  string
	Task                   string
	Model                  string
	RuntimeBackend         string
	RuntimeState           string
	RuntimeMode            string
	RuntimeStatusSummary   string
	SteeredFromRunID       string
	ReplacementRunID       string
	Mode                   string
	ThreadID               string
	Status                 string
	EndedReason            string
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Requester              string
	SpawnDepth             int
	Result                 string
	SuppressAnnounceReason string
	AnnounceDeliveryPath   string
	RegistryRecord         *task.SubagentRunRecord
}

func listSubagentTargets(toolCtx ToolContext, recentMinutes int) []subagentTarget {
	requester := resolveRequesterForSubagents(toolCtx.Run)
	targets := make([]subagentTarget, 0)
	seenBySession := map[string]struct{}{}
	cutoff := time.Time{}
	if recentMinutes > 0 {
		cutoff = time.Now().UTC().Add(-time.Duration(recentMinutes) * time.Minute)
	}

	if runtime := toolCtx.Runtime; runtime != nil && runtime.GetSubagents() != nil {
		runs := runtime.GetSubagents().ListByRequester(requester)
		sort.SliceStable(runs, func(i, j int) bool {
			return runs[i].CreatedAt.After(runs[j].CreatedAt)
		})
		for _, run := range runs {
			status := "running"
			if !run.EndedAt.IsZero() && run.Outcome != nil {
				status = string(run.Outcome.Status)
			}
			kind := strings.TrimSpace(run.ChildKind)
			if kind == "" {
				kind = "subagent"
			}
			candidate := run
			targets = append(targets, subagentTarget{
				Kind:                   kind,
				RunID:                  run.RunID,
				ChildSessionKey:        run.ChildSessionKey,
				RequesterDisplayKey:    strings.TrimSpace(run.RequesterDisplayKey),
				Label:                  strings.TrimSpace(run.Label),
				Task:                   run.Task,
				Model:                  strings.TrimSpace(run.Model),
				RuntimeBackend:         strings.TrimSpace(run.RuntimeBackend),
				RuntimeState:           strings.TrimSpace(run.RuntimeState),
				RuntimeMode:            strings.TrimSpace(run.RuntimeMode),
				RuntimeStatusSummary:   strings.TrimSpace(run.RuntimeStatusSummary),
				SteeredFromRunID:       strings.TrimSpace(run.SteeredFromRunID),
				ReplacementRunID:       strings.TrimSpace(run.ReplacementRunID),
				Mode:                   strings.TrimSpace(run.SpawnMode),
				ThreadID:               strings.TrimSpace(run.RouteThreadID),
				Status:                 status,
				EndedReason:            strings.TrimSpace(run.EndedReason),
				CreatedAt:              run.CreatedAt,
				UpdatedAt:              latestTime(run.CreatedAt, run.StartedAt, run.EndedAt),
				Requester:              run.RequesterSessionKey,
				SpawnDepth:             run.SpawnDepth,
				Result:                 strings.TrimSpace(run.FrozenResultText),
				SuppressAnnounceReason: strings.TrimSpace(run.SuppressAnnounceReason),
				AnnounceDeliveryPath:   strings.TrimSpace(run.AnnounceDeliveryPath),
				RegistryRecord:         &candidate,
			})
			seenBySession[run.ChildSessionKey] = struct{}{}
		}
	}

	if runtime := toolCtx.Runtime; runtime != nil && runtime.GetSessions() != nil {
		children := runtime.GetSessions().ListPersistentSpawnedChildren(requester)
		for _, child := range children {
			if _, exists := seenBySession[child.SessionKey]; exists {
				continue
			}
			status := "idle"
			if strings.TrimSpace(child.ACPState) != "" {
				status = strings.TrimSpace(child.ACPState)
			}
			targets = append(targets, subagentTarget{
				Kind:            child.Kind,
				ChildSessionKey: child.SessionKey,
				SessionID:       child.SessionID,
				Label:           child.Label,
				Mode:            child.Mode,
				ThreadID:        child.ThreadID,
				Status:          status,
				CreatedAt:       child.UpdatedAt,
				UpdatedAt:       child.UpdatedAt,
				Requester:       child.RequesterSessionKey,
			})
		}
	}

	sort.SliceStable(targets, func(i, j int) bool {
		return targets[i].UpdatedAt.After(targets[j].UpdatedAt)
	})
	if !cutoff.IsZero() {
		filtered := targets[:0]
		for _, target := range targets {
			if !target.UpdatedAt.IsZero() && target.UpdatedAt.Before(cutoff) {
				continue
			}
			filtered = append(filtered, target)
		}
		targets = filtered
	}
	for i := range targets {
		targets[i].Index = i + 1
	}
	return targets
}

func resolveSubagentTarget(targets []subagentTarget, token string) (*subagentTarget, error) {
	target := strings.TrimSpace(token)
	if target == "" {
		return nil, nil
	}
	for _, entry := range targets {
		if entry.RunID == target || entry.ChildSessionKey == target || strings.TrimSpace(entry.Label) == target {
			candidate := entry
			return &candidate, nil
		}
	}
	matches := make([]subagentTarget, 0, 4)
	for _, entry := range targets {
		if strings.HasPrefix(entry.RunID, target) ||
			strings.HasPrefix(entry.ChildSessionKey, target) ||
			strings.HasPrefix(strings.TrimSpace(entry.Label), target) {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 1 {
		candidate := matches[0]
		return &candidate, nil
	}
	if len(matches) > 1 {
		labels := make([]string, 0, len(matches))
		for _, match := range matches {
			label := strings.TrimSpace(match.Label)
			if label == "" {
				label = match.ChildSessionKey
			}
			labels = append(labels, label)
		}
		sort.Strings(labels)
		return nil, fmt.Errorf("ambiguous subagent target %q (matches: %s)", target, strings.Join(labels, ", "))
	}
	if idx, ok := parseSubagentIndex(target); ok && idx >= 1 && idx <= len(targets) {
		candidate := targets[idx-1]
		return &candidate, nil
	}
	return nil, nil
}

func resolveSubagentTargetState(target subagentTarget) string {
	if strings.TrimSpace(target.Status) != "" {
		return target.Status
	}
	if target.Kind == "acp" {
		return "idle"
	}
	return "running"
}

func latestTime(values ...time.Time) time.Time {
	best := time.Time{}
	for _, value := range values {
		if value.After(best) {
			best = value
		}
	}
	return best
}
