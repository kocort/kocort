package task

import "github.com/kocort/kocort/internal/acp"

// ValidateACPSpawnRequest keeps sessions_spawn ACP validation callable from the
// spawn façade without pushing ACP rules back into the tool layer.
func ValidateACPSpawnRequest(req acp.SessionSpawnRequest) error {
	return acp.ValidateSessionSpawnRequest(req)
}
