// Package core defines shared types, enums, value objects, and interfaces
// used across all kocort packages. It has zero internal dependencies and
// serves as the foundation layer of the dependency graph.
//
// Design principles:
//   - No *Runtime references — types that need Runtime stay in the runtime package
//   - Pure data types and simple interfaces only
//   - No business logic or side effects
package core
