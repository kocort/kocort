package backend

import (
	"errors"
	"strings"
)

// BackendFailureReason classifies the failure mode of a backend call.
type BackendFailureReason string

const (
	BackendFailureSessionExpired  BackendFailureReason = "session_expired"
	BackendFailureTransientHTTP   BackendFailureReason = "transient_http"
	BackendFailureContextOverflow BackendFailureReason = "context_overflow"
	BackendFailureRoleOrdering    BackendFailureReason = "role_ordering"
	BackendFailureSessionCorrupt  BackendFailureReason = "session_corruption"
	BackendFailureAuth            BackendFailureReason = "auth"
	BackendFailureBilling         BackendFailureReason = "billing"
	BackendFailureRateLimit       BackendFailureReason = "rate_limit"
	BackendFailureOverloaded      BackendFailureReason = "overloaded"
)

// BackendError represents a structured error from a backend call.
type BackendError struct {
	Reason  BackendFailureReason
	Message string
}

func (e *BackendError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return string(e.Reason)
}

// ErrorReason extracts the BackendFailureReason from an error, either by
// unwrapping a *BackendError or by heuristic message matching.
func ErrorReason(err error) BackendFailureReason {
	var be *BackendError
	if errors.As(err, &be) {
		return be.Reason
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "session expired"):
		return BackendFailureSessionExpired
	case strings.Contains(message, "context overflow"), strings.Contains(message, "context limit exceeded"), strings.Contains(message, "prompt too large"), strings.Contains(message, "prompt too long"):
		return BackendFailureContextOverflow
	case strings.Contains(message, "roles must alternate"), strings.Contains(message, "incorrect role information"), strings.Contains(message, "message ordering conflict"):
		return BackendFailureRoleOrdering
	case strings.Contains(message, "function call turn comes immediately after"):
		return BackendFailureSessionCorrupt
	case strings.Contains(message, "401"), strings.Contains(message, "403"), strings.Contains(message, "invalid api key"), strings.Contains(message, "invalid_api_key"), strings.Contains(message, "authentication"), strings.Contains(message, "unauthorized"):
		return BackendFailureAuth
	case strings.Contains(message, "billing"), strings.Contains(message, "quota exceeded"), strings.Contains(message, "insufficient_quota"), strings.Contains(message, "payment required"), strings.Contains(message, "402"):
		return BackendFailureBilling
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"), strings.Contains(message, "rate_limit"), strings.Contains(message, "too many requests"):
		return BackendFailureRateLimit
	case strings.Contains(message, "529"), strings.Contains(message, "overloaded"), strings.Contains(message, "server overloaded"), strings.Contains(message, "capacity"):
		return BackendFailureOverloaded
	case strings.Contains(message, "502"), strings.Contains(message, "503"), strings.Contains(message, "504"), strings.Contains(message, "521"), strings.Contains(message, "522"), strings.Contains(message, "timed out"), strings.Contains(message, "temporary"), strings.Contains(message, "transient"):
		return BackendFailureTransientHTTP
	default:
		return ""
	}
}
