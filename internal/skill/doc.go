// Package skill manages the skill system including skill installation,
// runtime registration, status monitoring, and cross-agent skill dispatch.
//
// Canonical implementation files:
//   - skills.go          — Skill loading, building, frontmatter parsing
//   - skills_runtime.go  — Requirements evaluation, watcher, env overrides, config resolution
//   - skills_install.go  — Skill installation logic
//   - skills_status.go   — Status building, install preferences
//   - skills_remote.go   — Remote skill eligibility
//   - skill_commands.go  — Skill command invocation/resolution
//   - helpers.go         — Small shared utilities
//
// Design: Skills are installable capability modules that extend agent
// functionality through tool definitions, environment configuration,
// and cross-agent dispatch specifications.
package skill
