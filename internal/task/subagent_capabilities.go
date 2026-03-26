package task

// ---------------------------------------------------------------------------

//
// Implements the three-tier role system (main / orchestrator / leaf) and
// associated control-scope resolution used to gate spawn and control
// operations at each nesting depth.
//

// ---------------------------------------------------------------------------

// SubagentSessionRole describes the role a subagent session plays in the
// spawn hierarchy.
type SubagentSessionRole string

const (
	// SubagentRoleMain is the top-level agent (depth == 0).
	SubagentRoleMain SubagentSessionRole = "main"
	// SubagentRoleOrchestrator is a mid-level agent (0 < depth < maxSpawnDepth)
	// that can itself spawn and control children.
	SubagentRoleOrchestrator SubagentSessionRole = "orchestrator"
	// SubagentRoleLeaf is the deepest-level agent (depth >= maxSpawnDepth)
	// that cannot spawn further children.
	SubagentRoleLeaf SubagentSessionRole = "leaf"
)

// SubagentControlScope describes what children a session is allowed to
// control at runtime.
type SubagentControlScope string

const (
	// SubagentControlChildren means the session can control (kill/steer/send)
	// its own children.
	SubagentControlChildren SubagentControlScope = "children"
	// SubagentControlNone means the session cannot control any children.
	SubagentControlNone SubagentControlScope = "none"
)

// DefaultSubagentMaxSpawnDepth is the default maximum spawn depth when
// no explicit value is configured.
const DefaultSubagentMaxSpawnDepth = 5

// SubagentCapabilities describes the resolved capabilities of a session at
// a specific depth in the spawn hierarchy.
type SubagentCapabilities struct {
	Depth              int
	Role               SubagentSessionRole
	ControlScope       SubagentControlScope
	CanSpawn           bool
	CanControlChildren bool
}

// ResolveSubagentRoleForDepth determines the session role for a given depth

func ResolveSubagentRoleForDepth(depth int, maxSpawnDepth int) SubagentSessionRole {
	if depth < 0 {
		depth = 0
	}
	if maxSpawnDepth < 1 {
		maxSpawnDepth = DefaultSubagentMaxSpawnDepth
	}

	if depth <= 0 {
		return SubagentRoleMain
	}
	if depth < maxSpawnDepth {
		return SubagentRoleOrchestrator
	}
	return SubagentRoleLeaf
}

// ResolveSubagentControlScopeForRole returns the control scope for a role.
func ResolveSubagentControlScopeForRole(role SubagentSessionRole) SubagentControlScope {
	if role == SubagentRoleLeaf {
		return SubagentControlNone
	}
	return SubagentControlChildren
}

// ResolveSubagentCapabilities resolves the full capability set for a session
// at a given depth.
func ResolveSubagentCapabilities(depth int, maxSpawnDepth int) SubagentCapabilities {
	role := ResolveSubagentRoleForDepth(depth, maxSpawnDepth)
	scope := ResolveSubagentControlScopeForRole(role)
	return SubagentCapabilities{
		Depth:              depth,
		Role:               role,
		ControlScope:       scope,
		CanSpawn:           role == SubagentRoleMain || role == SubagentRoleOrchestrator,
		CanControlChildren: scope == SubagentControlChildren,
	}
}

// ResolveStoredSubagentCapabilities resolves capabilities for a session,
// optionally using persisted role and control scope from the session entry.
// If storedRole or storedControlScope are non-empty, they override the
// depth-derived defaults.
func ResolveStoredSubagentCapabilities(depth int, maxSpawnDepth int, storedRole string, storedControlScope string) SubagentCapabilities {
	caps := ResolveSubagentCapabilities(depth, maxSpawnDepth)

	if storedRole != "" {
		role := SubagentSessionRole(storedRole)
		switch role {
		case SubagentRoleMain, SubagentRoleOrchestrator, SubagentRoleLeaf:
			caps.Role = role
		}
	}
	if storedControlScope != "" {
		scope := SubagentControlScope(storedControlScope)
		switch scope {
		case SubagentControlChildren, SubagentControlNone:
			caps.ControlScope = scope
		}
	}

	caps.CanSpawn = caps.Role == SubagentRoleMain || caps.Role == SubagentRoleOrchestrator
	caps.CanControlChildren = caps.ControlScope == SubagentControlChildren
	return caps
}
