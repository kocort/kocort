package backend

import (
	"errors"
	"fmt"
	"testing"
)

func TestBackendErrorWithMessage(t *testing.T) {
	err := &BackendError{Reason: BackendFailureContextOverflow, Message: "too many tokens"}
	if err.Error() != "too many tokens" {
		t.Errorf("got %q", err.Error())
	}
}

func TestBackendErrorWithoutMessage(t *testing.T) {
	err := &BackendError{Reason: BackendFailureSessionExpired}
	if err.Error() != "session_expired" {
		t.Errorf("got %q", err.Error())
	}
}

func TestBackendErrorImplementsError(t *testing.T) {
	var err error = &BackendError{Reason: BackendFailureTransientHTTP, Message: "503"}
	if err == nil {
		t.Fatal("expected non-nil error")
	}
}

func TestBackendErrorUnwrap(t *testing.T) {
	be := &BackendError{Reason: BackendFailureRoleOrdering, Message: "bad roles"}
	wrapped := fmt.Errorf("wrapped: %w", be)
	var target *BackendError
	if !errors.As(wrapped, &target) {
		t.Error("expected errors.As to find BackendError")
	}
	if target.Reason != BackendFailureRoleOrdering {
		t.Errorf("got reason=%q", target.Reason)
	}
}

func TestErrorReasonFromBackendError(t *testing.T) {
	err := &BackendError{Reason: BackendFailureContextOverflow, Message: "overflow"}
	if got := ErrorReason(err); got != BackendFailureContextOverflow {
		t.Errorf("got %q", got)
	}
}

func TestErrorReasonFromWrappedBackendError(t *testing.T) {
	be := &BackendError{Reason: BackendFailureSessionCorrupt, Message: "corrupt"}
	err := fmt.Errorf("layer: %w", be)
	if got := ErrorReason(err); got != BackendFailureSessionCorrupt {
		t.Errorf("got %q", got)
	}
}

func TestErrorReasonFromHeuristicMessages(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want BackendFailureReason
	}{
		{"session_expired", "session expired please re-authenticate", BackendFailureSessionExpired},
		{"context_overflow", "context overflow detected", BackendFailureContextOverflow},
		{"context_limit", "context limit exceeded for model", BackendFailureContextOverflow},
		{"prompt_too_large", "prompt too large", BackendFailureContextOverflow},
		{"role_ordering", "roles must alternate between user and assistant", BackendFailureRoleOrdering},
		{"incorrect_role", "incorrect role information", BackendFailureRoleOrdering},
		{"message_ordering", "message ordering conflict", BackendFailureRoleOrdering},
		{"session_corrupt", "function call turn comes immediately after another", BackendFailureSessionCorrupt},
		{"502_gateway", "502 bad gateway", BackendFailureTransientHTTP},
		{"503_unavailable", "503 service unavailable", BackendFailureTransientHTTP},
		{"504_timeout", "504 gateway timeout", BackendFailureTransientHTTP},
		{"timed_out", "request timed out", BackendFailureTransientHTTP},
		{"temporary", "temporary failure", BackendFailureTransientHTTP},
		{"transient", "transient error", BackendFailureTransientHTTP},
		{"unknown", "something random happened", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.msg)
			if got := ErrorReason(err); got != tt.want {
				t.Errorf("ErrorReason(%q) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}
