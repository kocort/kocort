package ffi

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestLibName(t *testing.T) {
	tests := []struct {
		goos   string
		base   string
		expect string
	}{
		{"darwin", "llama", "libllama.dylib"},
		{"darwin", "ggml", "libggml.dylib"},
		{"linux", "llama", "libllama.so"},
		{"windows", "llama", "llama.dll"},
	}
	for _, tt := range tests {
		if runtime.GOOS != tt.goos {
			continue
		}
		got := LibName(tt.base)
		if got != tt.expect {
			t.Errorf("LibName(%q) = %q, want %q", tt.base, got, tt.expect)
		}
	}
}

func TestLibExtension(t *testing.T) {
	ext := LibExtension()
	switch runtime.GOOS {
	case "darwin":
		if ext != ".dylib" {
			t.Errorf("expected .dylib, got %s", ext)
		}
	case "linux":
		if ext != ".so" {
			t.Errorf("expected .so, got %s", ext)
		}
	case "windows":
		if ext != ".dll" {
			t.Errorf("expected .dll, got %s", ext)
		}
	}
}

func TestRequiredLibNames(t *testing.T) {
	names := requiredLibNames()
	// Windows has 3 required libs (no ggml-cpu.dll — it ships arch-specific variants),
	// other platforms have 4.
	minExpected := 4
	if runtime.GOOS == "windows" {
		minExpected = 3
	}
	if len(names) < minExpected {
		t.Fatalf("expected at least %d required libs, got %d", minExpected, len(names))
	}
	for _, n := range names {
		t.Logf("required lib: %s", n)
	}
}

func TestSchemaToGrammarBasicTypes(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		expect bool
	}{
		{"string", `{"type":"string"}`, true},
		{"number", `{"type":"number"}`, true},
		{"integer", `{"type":"integer"}`, true},
		{"boolean", `{"type":"boolean"}`, true},
		{"null", `{"type":"null"}`, true},
		{"object", `{"type":"object","properties":{"name":{"type":"string"}}}`, true},
		{"array", `{"type":"array","items":{"type":"string"}}`, true},
		{"enum", `{"enum":["a","b","c"]}`, true},
		{"invalid", `{invalid}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SchemaToGrammar([]byte(tt.schema))
			if tt.expect && result == nil {
				t.Error("expected non-nil result, got nil")
			}
			if !tt.expect && result != nil {
				t.Errorf("expected nil, got: %s", string(result))
			}
			if result != nil {
				t.Logf("Grammar:\n%s", string(result))
			}
		})
	}
}

func TestSchemaToGrammarObject(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"age":  map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}
	b, _ := json.Marshal(schema)
	result := SchemaToGrammar(b)
	if result == nil {
		t.Fatal("expected non-nil grammar")
	}
	grammar := string(result)
	t.Logf("Grammar:\n%s", grammar)
	if !strings.Contains(grammar, "root") {
		t.Error("grammar should contain root rule")
	}
}

func TestSchemaToGrammarOneOf(t *testing.T) {
	schema := `{"oneOf":[{"type":"string"},{"type":"integer"}]}`
	result := SchemaToGrammar([]byte(schema))
	if result == nil {
		t.Fatal("expected non-nil grammar for oneOf")
	}
	grammar := string(result)
	t.Logf("Grammar:\n%s", grammar)
	if !strings.Contains(grammar, "root") {
		t.Error("grammar should contain root rule")
	}
}

func TestDownloadURL(t *testing.T) {
	tests := []struct {
		version string
		gpu     string
	}{
		{"b8720", ""},
		{"b8720", "vulkan"},
		{"b8720", "cuda-12.4"},
	}
	for _, tt := range tests {
		url := buildDownloadURL(tt.version, tt.gpu)
		t.Logf("URL(%s, %q) = %s", tt.version, tt.gpu, url)
		if url == "" {
			t.Error("expected non-empty URL")
		}
		if !strings.Contains(url, tt.version) {
			t.Errorf("URL should contain version %s", tt.version)
		}
	}
}
