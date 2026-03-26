// Package config defines all configuration struct types (AppConfig,
// AgentsConfig, SessionConfig, etc.) and provides config file loading
// utilities. It depends only on internal/core for value object types.
//
// Migration note: Config types were extracted from runtime/config.go.
// The runtime package re-exports these types via type aliases for
// backward compatibility.
package config
