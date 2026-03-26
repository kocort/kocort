// Pure HTTP gateway helper utilities.
//
// These functions have no dependency on *Runtime and can be used by
// any package that needs JSON HTTP responses or query-parameter parsing.
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kocort/kocort/utils"
)

// writeJSON writes a JSON-encoded response with the given status code.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value) // best-effort; response write failure is non-critical
}

// ParseChatHistoryLimit parses the "limit" query parameter from the request.
func ParseChatHistoryLimit(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid limit %q: %w", raw, err)
	}
	if limit < 0 {
		return 0, fmt.Errorf("invalid limit: must be non-negative")
	}
	return limit, nil
}

// ParseChatHistoryBefore parses the "before" query parameter from the request.
func ParseChatHistoryBefore(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("before"))
	if raw == "" {
		return 0, nil
	}
	before, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid before %q: %w", raw, err)
	}
	if before < 0 {
		return 0, fmt.Errorf("invalid before: must be non-negative")
	}
	return before, nil
}

// boolPtr returns a pointer to v.
func boolPtr(v bool) *bool { return utils.BoolPtr(v) }
