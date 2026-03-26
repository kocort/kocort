package config

import (
	_ "embed"
)

//go:embed default_config.json
var embeddedDefaultConfigJSON []byte

// DefaultConfigJSON returns a copy of the embedded default config JSON.
func DefaultConfigJSON() []byte {
	return append([]byte{}, embeddedDefaultConfigJSON...)
}
