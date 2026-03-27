package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// APIError — structured error type for HTTP API responses.
// ---------------------------------------------------------------------------

// APIError represents a structured error returned by the HTTP API.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *APIError) Error() string { return e.Message }

// Predefined API error codes.
var (
	ErrAPIBadRequest       = &APIError{Code: "BAD_REQUEST", Status: http.StatusBadRequest}
	ErrAPINotFound         = &APIError{Code: "NOT_FOUND", Status: http.StatusNotFound}
	ErrAPIConflict         = &APIError{Code: "CONFLICT", Status: http.StatusConflict}
	ErrAPIInternalError    = &APIError{Code: "INTERNAL_ERROR", Status: http.StatusInternalServerError}
	ErrAPIMethodNotAllowed = &APIError{Code: "METHOD_NOT_ALLOWED", Status: http.StatusMethodNotAllowed}
	ErrAPIUnauthorized     = &APIError{Code: "UNAUTHORIZED", Status: http.StatusUnauthorized}
)

// ---------------------------------------------------------------------------
// writeError — unified JSON error response writer.
// ---------------------------------------------------------------------------

// writeError writes a structured JSON error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{ // best-effort; response write failure is non-critical
		"error":   code,
		"message": message,
	})
}

// writeErrorFromErr inspects err against known sentinel errors and writes
// the appropriate HTTP status and error code.
func writeErrorFromErr(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "BAD_REQUEST"

	switch {
	case errors.Is(err, core.ErrAgentNotFound),
		errors.Is(err, core.ErrSessionNotFound),
		errors.Is(err, core.ErrTaskNotFound),
		errors.Is(err, core.ErrToolNotFound),
		errors.Is(err, core.ErrProviderNotFound),
		errors.Is(err, core.ErrModelNotFound),
		errors.Is(err, core.ErrChannelNotRegistered):
		status = http.StatusNotFound
		code = "NOT_FOUND"

	case errors.Is(err, core.ErrUnauthorized):
		status = http.StatusUnauthorized
		code = "UNAUTHORIZED"

	case errors.Is(err, core.ErrTaskAlreadyRunning):
		status = http.StatusConflict
		code = "CONFLICT"

	case errors.Is(err, core.ErrNoDefaultModelConfigured):
		status = http.StatusBadRequest
		code = "NO_DEFAULT_MODEL"

	case errors.Is(err, core.ErrRuntimeNotReady),
		errors.Is(err, core.ErrToolRegistryNotConfigured),
		errors.Is(err, core.ErrTaskSchedulerNotConfigured),
		errors.Is(err, core.ErrChannelRegistryNotConfigured),
		errors.Is(err, core.ErrSubagentRegistryNotConfigured),
		errors.Is(err, core.ErrProcessNotConfigured),
		errors.Is(err, core.ErrDelivererNotConfigured),
		errors.Is(err, core.ErrSystemEventsNotConfigured),
		errors.Is(err, core.ErrACPNotConfigured):
		status = http.StatusInternalServerError
		code = "INTERNAL_ERROR"
	}

	writeError(w, status, code, err.Error())
}
