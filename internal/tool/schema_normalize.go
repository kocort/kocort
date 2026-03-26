package tool

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Tool Schema Normalization — Multi-provider compatibility
//
// Different LLM providers have varying support for JSON Schema constructs
// in tool/function parameter definitions. This module normalizes schemas
// to maximize compatibility across providers.
//
// Normalizations:
//   - Gemini: remove unsupported keywords (format, default, examples, $ref,
//     pattern, minLength, maxLength, minimum, maximum, multipleOf, etc.)
//   - xAI: strip unsupported keywords (format, pattern, examples, $ref)
//   - anyOf/oneOf unions: merge into single object schema
//   - enum merge + dedup
//   - required merge: intersection across variants
//   - additionalProperties: preserved
//

// ---------------------------------------------------------------------------

// SchemaProvider identifies the target LLM provider for schema normalization.
type SchemaProvider string

const (
	SchemaProviderGeneric   SchemaProvider = ""
	SchemaProviderGemini    SchemaProvider = "gemini"
	SchemaProviderXAI       SchemaProvider = "xai"
	SchemaProviderOpenAI    SchemaProvider = "openai"
	SchemaProviderAnthropic SchemaProvider = "anthropic"
)

// geminiUnsupportedKeywords are JSON Schema keywords not supported by Gemini.
var geminiUnsupportedKeywords = map[string]bool{
	"format":            true,
	"default":           true,
	"examples":          true,
	"$ref":              true,
	"$schema":           true,
	"$id":               true,
	"pattern":           true,
	"minLength":         true,
	"maxLength":         true,
	"minimum":           true,
	"maximum":           true,
	"multipleOf":        true,
	"exclusiveMinimum":  true,
	"exclusiveMaximum":  true,
	"minItems":          true,
	"maxItems":          true,
	"uniqueItems":       true,
	"minProperties":     true,
	"maxProperties":     true,
	"patternProperties": true,
	"if":                true,
	"then":              true,
	"else":              true,
	"not":               true,
	"const":             true,
	"contentMediaType":  true,
	"contentEncoding":   true,
	"title":             true,
}

// xaiUnsupportedKeywords are JSON Schema keywords not supported by xAI.
var xaiUnsupportedKeywords = map[string]bool{
	"format":   true,
	"pattern":  true,
	"examples": true,
	"$ref":     true,
	"$schema":  true,
	"$id":      true,
	"title":    true,
}

// NormalizeToolParameters normalizes a tool parameter schema for the given provider.
// The schema is modified in place and also returned.
func NormalizeToolParameters(schema map[string]any, provider SchemaProvider) map[string]any {
	if schema == nil {
		return schema
	}

	// Step 1: Flatten anyOf/oneOf unions.
	schema = flattenUnions(schema)

	// Step 2: Provider-specific keyword removal.
	switch provider {
	case SchemaProviderGemini:
		stripKeywords(schema, geminiUnsupportedKeywords)
	case SchemaProviderXAI:
		stripKeywords(schema, xaiUnsupportedKeywords)
	}

	return schema
}

// ---------------------------------------------------------------------------
// Union flattening (anyOf / oneOf)
// ---------------------------------------------------------------------------

// flattenUnions recursively merges anyOf/oneOf arrays into a single object schema.
func flattenUnions(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}

	// Process nested properties first.
	if props, ok := schema["properties"].(map[string]any); ok {
		for key, val := range props {
			if sub, ok := val.(map[string]any); ok {
				props[key] = flattenUnions(sub)
			}
		}
	}

	// Process items (for arrays).
	if items, ok := schema["items"].(map[string]any); ok {
		schema["items"] = flattenUnions(items)
	}

	// Flatten anyOf.
	if anyOf, ok := extractSchemaArray(schema, "anyOf"); ok && len(anyOf) > 0 {
		merged := mergeVariants(anyOf)
		delete(schema, "anyOf")
		for k, v := range merged {
			if _, exists := schema[k]; !exists {
				schema[k] = v
			}
		}
		return schema
	}

	// Flatten oneOf.
	if oneOf, ok := extractSchemaArray(schema, "oneOf"); ok && len(oneOf) > 0 {
		merged := mergeVariants(oneOf)
		delete(schema, "oneOf")
		for k, v := range merged {
			if _, exists := schema[k]; !exists {
				schema[k] = v
			}
		}
		return schema
	}

	return schema
}

// extractSchemaArray extracts an array of schemas from a key.
func extractSchemaArray(schema map[string]any, key string) ([]map[string]any, bool) {
	val, ok := schema[key]
	if !ok {
		return nil, false
	}
	arr, ok := val.([]any)
	if !ok {
		return nil, false
	}
	result := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			result = append(result, m)
		}
	}
	return result, len(result) > 0
}

// mergeVariants merges multiple schema variants into a single unified schema.
// - Properties: union of all properties
// - Required: intersection of all required arrays
// - Enum: union + dedup
// - Type: prefer "object", fallback to first variant's type
func mergeVariants(variants []map[string]any) map[string]any {
	if len(variants) == 0 {
		return map[string]any{}
	}
	if len(variants) == 1 {
		return flattenUnions(variants[0])
	}

	merged := map[string]any{}

	// Merge type — prefer "object" if any variant is object.
	mergedType := ""
	for _, v := range variants {
		if t, ok := v["type"].(string); ok {
			if t == "object" {
				mergedType = "object"
				break
			}
			if mergedType == "" {
				mergedType = t
			}
		}
	}
	if mergedType != "" {
		merged["type"] = mergedType
	}

	// Merge description — use first non-empty.
	for _, v := range variants {
		if desc, ok := v["description"].(string); ok && strings.TrimSpace(desc) != "" {
			merged["description"] = desc
			break
		}
	}

	// Merge properties (union).
	allProps := map[string]any{}
	for _, v := range variants {
		if props, ok := v["properties"].(map[string]any); ok {
			for k, val := range props {
				if _, exists := allProps[k]; !exists {
					if sub, ok := val.(map[string]any); ok {
						allProps[k] = flattenUnions(sub)
					} else {
						allProps[k] = val
					}
				}
			}
		}
	}
	if len(allProps) > 0 {
		merged["properties"] = allProps
		if mergedType == "" {
			merged["type"] = "object"
		}
	}

	// Merge required (intersection).
	merged = mergeRequired(merged, variants)

	// Merge enum (union + dedup).
	merged = mergeEnums(merged, variants)

	// Preserve additionalProperties from first variant that has it.
	for _, v := range variants {
		if ap, ok := v["additionalProperties"]; ok {
			merged["additionalProperties"] = ap
			break
		}
	}

	return merged
}

// mergeRequired computes the intersection of required arrays across variants.
func mergeRequired(merged map[string]any, variants []map[string]any) map[string]any {
	var requiredSets []map[string]bool
	for _, v := range variants {
		reqArr, ok := v["required"].([]any)
		if !ok {
			continue
		}
		set := make(map[string]bool, len(reqArr))
		for _, r := range reqArr {
			if s, ok := r.(string); ok {
				set[s] = true
			}
		}
		requiredSets = append(requiredSets, set)
	}

	if len(requiredSets) == 0 {
		return merged
	}

	// Intersection: a field is required only if it appears in ALL variants that have required.
	intersection := make(map[string]bool)
	for k := range requiredSets[0] {
		intersection[k] = true
	}
	for _, set := range requiredSets[1:] {
		for k := range intersection {
			if !set[k] {
				delete(intersection, k)
			}
		}
	}

	if len(intersection) > 0 {
		required := make([]any, 0, len(intersection))
		for k := range intersection {
			required = append(required, k)
		}
		// Sort for deterministic output.
		sort.Slice(required, func(i, j int) bool {
			return required[i].(string) < required[j].(string)
		})
		merged["required"] = required
	}

	return merged
}

// mergeEnums computes the union of enum values across variants, deduplicated.
func mergeEnums(merged map[string]any, variants []map[string]any) map[string]any {
	seen := map[string]bool{}
	var allEnums []any

	for _, v := range variants {
		enumArr, ok := v["enum"].([]any)
		if !ok {
			continue
		}
		for _, e := range enumArr {
			key := anyToString(e)
			if !seen[key] {
				seen[key] = true
				allEnums = append(allEnums, e)
			}
		}
	}

	if len(allEnums) > 0 {
		merged["enum"] = allEnums
	}

	return merged
}

// ---------------------------------------------------------------------------
// Keyword stripping
// ---------------------------------------------------------------------------

// stripKeywords recursively removes unsupported keywords from a schema.
func stripKeywords(schema map[string]any, unsupported map[string]bool) {
	if schema == nil {
		return
	}

	for key := range unsupported {
		delete(schema, key)
	}

	// Recurse into properties.
	if props, ok := schema["properties"].(map[string]any); ok {
		for _, val := range props {
			if sub, ok := val.(map[string]any); ok {
				stripKeywords(sub, unsupported)
			}
		}
	}

	// Recurse into items.
	if items, ok := schema["items"].(map[string]any); ok {
		stripKeywords(items, unsupported)
	}

	// Recurse into anyOf/oneOf that survived flattening.
	for _, arrayKey := range []string{"anyOf", "oneOf", "allOf"} {
		if arr, ok := schema[arrayKey].([]any); ok {
			for _, item := range arr {
				if sub, ok := item.(map[string]any); ok {
					stripKeywords(sub, unsupported)
				}
			}
		}
	}
}

// anyToString converts a value to a string key for deduplication.
func anyToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", val)
	}
}
