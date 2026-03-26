package tool

import (
	"testing"
)

// ---------------------------------------------------------------------------
// NormalizeToolParameters tests
// ---------------------------------------------------------------------------

func TestNormalizeToolParameters_Nil(t *testing.T) {
	result := NormalizeToolParameters(nil, SchemaProviderGeneric)
	if result != nil {
		t.Fatal("expected nil")
	}
}

func TestNormalizeToolParameters_NoChange(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}
	result := NormalizeToolParameters(schema, SchemaProviderOpenAI)
	if result["type"] != "object" {
		t.Fatal("expected type=object")
	}
}

// ---------------------------------------------------------------------------
// Gemini keyword stripping
// ---------------------------------------------------------------------------

func TestNormalizeToolParameters_GeminiStripsKeywords(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":      "string",
				"format":    "email",
				"pattern":   ".*@.*",
				"minLength": 1,
				"maxLength": 255,
				"default":   "user@example.com",
				"examples":  []any{"a@b.com"},
				"title":     "Name",
			},
			"age": map[string]any{
				"type":    "integer",
				"minimum": 0,
				"maximum": 150,
			},
		},
	}
	result := NormalizeToolParameters(schema, SchemaProviderGemini)
	props := result["properties"].(map[string]any)

	nameSchema := props["name"].(map[string]any)
	for _, key := range []string{"format", "pattern", "minLength", "maxLength", "default", "examples", "title"} {
		if _, ok := nameSchema[key]; ok {
			t.Errorf("Gemini: expected %q to be stripped from name", key)
		}
	}
	if nameSchema["type"] != "string" {
		t.Error("type should be preserved")
	}

	ageSchema := props["age"].(map[string]any)
	for _, key := range []string{"minimum", "maximum"} {
		if _, ok := ageSchema[key]; ok {
			t.Errorf("Gemini: expected %q to be stripped from age", key)
		}
	}
}

// ---------------------------------------------------------------------------
// xAI keyword stripping
// ---------------------------------------------------------------------------

func TestNormalizeToolParameters_XAIStripsKeywords(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":     "string",
				"format":   "uri",
				"pattern":  "^https://",
				"examples": []any{"https://example.com"},
				"title":    "URL",
			},
		},
	}
	result := NormalizeToolParameters(schema, SchemaProviderXAI)
	props := result["properties"].(map[string]any)
	urlSchema := props["url"].(map[string]any)
	for _, key := range []string{"format", "pattern", "examples", "title"} {
		if _, ok := urlSchema[key]; ok {
			t.Errorf("xAI: expected %q to be stripped", key)
		}
	}
	// Should keep minLength etc. (not in xAI unsupported).
	if urlSchema["type"] != "string" {
		t.Error("type should be preserved")
	}
}

// ---------------------------------------------------------------------------
// anyOf/oneOf flattening
// ---------------------------------------------------------------------------

func TestFlattenUnions_AnyOf(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required": []any{"name"},
			},
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":   map[string]any{"type": "integer"},
					"name": map[string]any{"type": "string"},
				},
				"required": []any{"id", "name"},
			},
		},
	}
	result := NormalizeToolParameters(schema, SchemaProviderGeneric)

	// anyOf should be removed.
	if _, ok := result["anyOf"]; ok {
		t.Fatal("anyOf should be flattened")
	}

	// Type should be object.
	if result["type"] != "object" {
		t.Fatalf("expected type=object, got %v", result["type"])
	}

	// Properties should be union: name + id.
	props := result["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Fatal("missing property 'name'")
	}
	if _, ok := props["id"]; !ok {
		t.Fatal("missing property 'id'")
	}

	// Required should be intersection: only "name" is in both.
	req := result["required"].([]any)
	if len(req) != 1 || req[0].(string) != "name" {
		t.Fatalf("expected required=[name], got %v", req)
	}
}

func TestFlattenUnions_OneOf(t *testing.T) {
	schema := map[string]any{
		"oneOf": []any{
			map[string]any{
				"type": "string",
				"enum": []any{"a", "b"},
			},
			map[string]any{
				"type": "string",
				"enum": []any{"b", "c"},
			},
		},
	}
	result := NormalizeToolParameters(schema, SchemaProviderGeneric)

	if _, ok := result["oneOf"]; ok {
		t.Fatal("oneOf should be flattened")
	}

	// Enum should be union: a, b, c (deduped).
	enums := result["enum"].([]any)
	if len(enums) != 3 {
		t.Fatalf("expected 3 enum values, got %d: %v", len(enums), enums)
	}
}

// ---------------------------------------------------------------------------
// Nested schema normalization
// ---------------------------------------------------------------------------

func TestNormalizeToolParameters_NestedProperties(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"config": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"nested": map[string]any{
						"type":    "string",
						"format":  "date",
						"pattern": "\\d{4}-\\d{2}-\\d{2}",
					},
				},
			},
		},
	}
	result := NormalizeToolParameters(schema, SchemaProviderGemini)
	configProps := result["properties"].(map[string]any)["config"].(map[string]any)["properties"].(map[string]any)
	nested := configProps["nested"].(map[string]any)
	if _, ok := nested["format"]; ok {
		t.Fatal("nested format should be stripped for Gemini")
	}
	if _, ok := nested["pattern"]; ok {
		t.Fatal("nested pattern should be stripped for Gemini")
	}
}

func TestNormalizeToolParameters_ArrayItems(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":    "string",
					"format":  "slug",
					"pattern": "^[a-z-]+$",
				},
			},
		},
	}
	result := NormalizeToolParameters(schema, SchemaProviderGemini)
	items := result["properties"].(map[string]any)["tags"].(map[string]any)["items"].(map[string]any)
	if _, ok := items["format"]; ok {
		t.Fatal("items format should be stripped for Gemini")
	}
}

// ---------------------------------------------------------------------------
// MergeVariants edge cases
// ---------------------------------------------------------------------------

func TestMergeVariants_Empty(t *testing.T) {
	result := mergeVariants(nil)
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}
}

func TestMergeVariants_Single(t *testing.T) {
	result := mergeVariants([]map[string]any{
		{"type": "string", "description": "test"},
	})
	if result["type"] != "string" {
		t.Fatal("expected type=string")
	}
}

func TestMergeVariants_AdditionalProperties(t *testing.T) {
	result := mergeVariants([]map[string]any{
		{"type": "object", "additionalProperties": false},
		{"type": "object"},
	})
	if result["additionalProperties"] != false {
		t.Fatal("expected additionalProperties=false preserved")
	}
}
