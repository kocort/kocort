package session

import (
	"fmt"
	"strings"
	"time"
)

type ThreadBindingIntroParams struct {
	AgentID       string
	Label         string
	IdleTimeoutMs int64
	MaxAgeMs      int64
}

type ThreadBindingFarewellParams struct {
	Reason        string
	IdleTimeoutMs int64
	MaxAgeMs      int64
}

func formatThreadBindingDurationLabel(durationMs int64) string {
	if durationMs <= 0 {
		return "disabled"
	}
	if durationMs < int64(time.Minute/time.Millisecond) {
		return "<1m"
	}
	totalMinutes := durationMs / int64(time.Minute/time.Millisecond)
	if totalMinutes%60 == 0 {
		return fmt.Sprintf("%dh", totalMinutes/60)
	}
	return fmt.Sprintf("%dm", totalMinutes)
}

func ResolveThreadBindingIntroText(params ThreadBindingIntroParams) string {
	base := strings.TrimSpace(params.Label)
	if base == "" {
		base = strings.TrimSpace(params.AgentID)
	}
	if base == "" {
		base = "agent"
	}
	lifecycle := make([]string, 0, 2)
	if params.IdleTimeoutMs > 0 {
		lifecycle = append(lifecycle, "idle auto-unfocus after "+formatThreadBindingDurationLabel(params.IdleTimeoutMs)+" inactivity")
	}
	if params.MaxAgeMs > 0 {
		lifecycle = append(lifecycle, "max age "+formatThreadBindingDurationLabel(params.MaxAgeMs))
	}
	if len(lifecycle) == 0 {
		return base + " session active. Messages here go directly to this session."
	}
	return base + " session active (" + strings.Join(lifecycle, "; ") + "). Messages here go directly to this session."
}

func ResolveThreadBindingFarewellText(params ThreadBindingFarewellParams) string {
	switch strings.TrimSpace(strings.ToLower(params.Reason)) {
	case "idle-expired":
		return "Session ended automatically after " + formatThreadBindingDurationLabel(params.IdleTimeoutMs) + " of inactivity. Messages here will no longer be routed."
	case "max-age-expired":
		return "Session ended automatically at max age of " + formatThreadBindingDurationLabel(params.MaxAgeMs) + ". Messages here will no longer be routed."
	default:
		return "Session ended. Messages here will no longer be routed."
	}
}
