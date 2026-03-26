package spawnpolicy

import (
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

func NormalizeSpawnSandboxMode(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "require":
		return "require"
	default:
		return "inherit"
	}
}

func ValidateSpawnRuntimePolicy(requester core.AgentIdentity, target core.AgentIdentity, mode string) error {
	mode = NormalizeSpawnSandboxMode(mode)
	requesterSandboxed := identitySandboxEnabled(requester)
	targetSandboxed := identitySandboxEnabled(target)

	if requesterSandboxed && !targetSandboxed {
		return fmt.Errorf("sandboxed requester cannot spawn an unsandboxed child agent")
	}
	if mode == "require" && !targetSandboxed {
		return fmt.Errorf(`sandbox="require" requires a sandbox-enabled target agent`)
	}
	return nil
}

func identitySandboxEnabled(identity core.AgentIdentity) bool {
	mode := strings.TrimSpace(strings.ToLower(identity.SandboxMode))
	return mode != "" && mode != "off"
}
