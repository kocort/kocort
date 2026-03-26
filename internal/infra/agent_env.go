package infra

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// EnvAppender is satisfied by EnvironmentRuntime (which remains in runtime/ for now).
type EnvAppender interface {
	AppendToEnv(env []string, overrides map[string]string) ([]string, error)
}

func AppendAgentRuntimeEnv(env []string, identity core.AgentIdentity, environment EnvAppender, overrides map[string]string) ([]string, error) {
	agentDir := strings.TrimSpace(identity.AgentDir)
	if agentDir != "" {
		if overrides == nil {
			overrides = map[string]string{}
		}
		overrides["KOCORT_AGENT_DIR"] = agentDir
		overrides["PI_CODING_AGENT_DIR"] = agentDir
	}
	if environment == nil {
		if len(overrides) == 0 {
			return env, nil
		}
		for key, value := range overrides {
			env = append(env, key+"="+value)
		}
		return env, nil
	}
	return environment.AppendToEnv(env, overrides)
}
