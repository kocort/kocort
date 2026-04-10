package ffi

import "github.com/kocort/kocort/internal/localmodel/grammar"

// SchemaToGrammar delegates to the grammar package.
// Deprecated: Use grammar.SchemaToGrammar directly.
func SchemaToGrammar(schema []byte) []byte {
	return grammar.SchemaToGrammar(schema)
}
