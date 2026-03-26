package ggml

// #cgo CPPFLAGS: -DGGML_USE_METAL -DGGML_METAL_EMBED_LIBRARY -DGGML_USE_BLAS
// #cgo LDFLAGS: -framework Foundation
import "C"

import (
	_ "github.com/kocort/kocort/internal/llama/ggml/src/ggml-blas"
	_ "github.com/kocort/kocort/internal/llama/ggml/src/ggml-metal"
)
