package localmodel

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed catalog.json
var catalogJSON []byte

// catalogData is the raw structure parsed from catalog.json.
type catalogData struct {
	Cerebellum []ModelPreset `json:"cerebellum"`
	Brain      []ModelPreset `json:"brain"`
}

// BuiltinCerebellumCatalog contains recommended models for the cerebellum (小脑).
// Small models optimized for safety review tasks.
// Loaded from catalog.json at startup.
var BuiltinCerebellumCatalog []ModelPreset

// BuiltinBrainCatalog contains recommended models for the brain (大脑) local mode.
// Larger models suitable for general agent tasks.
// Loaded from catalog.json at startup.
var BuiltinBrainCatalog []ModelPreset

func init() {
	var data catalogData
	if err := json.Unmarshal(catalogJSON, &data); err != nil {
		panic(fmt.Sprintf("localmodel: failed to parse catalog.json: %v", err))
	}
	BuiltinCerebellumCatalog = data.Cerebellum
	BuiltinBrainCatalog = data.Brain
}
