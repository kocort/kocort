package infra

import (
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func boolPtr(v bool) *bool { return &v }

func TestNewEnvironmentRuntime(t *testing.T) {
	env := NewEnvironmentRuntime(config.EnvironmentConfig{})
	if env == nil {
		t.Fatal("expected non-nil")
	}
}

func TestEnvironmentRuntime_Resolve(t *testing.T) {
	t.Run("from_entry_value", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"MY_KEY": {Value: "hello"},
			},
		})
		val, ok := env.Resolve("MY_KEY")
		if !ok || val != "hello" {
			t.Errorf("got %q, %v", val, ok)
		}
	})

	t.Run("from_os_env", func(t *testing.T) {
		t.Setenv("TEST_INFRA_ENV_RESOLVE", "from-os")
		env := NewEnvironmentRuntime(config.EnvironmentConfig{})
		val, ok := env.Resolve("TEST_INFRA_ENV_RESOLVE")
		if !ok || val != "from-os" {
			t.Errorf("got %q, %v", val, ok)
		}
	})

	t.Run("from_entry_from_env", func(t *testing.T) {
		t.Setenv("SOURCE_KEY", "source-value")
		env := NewEnvironmentRuntime(config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"TARGET_KEY": {FromEnv: "SOURCE_KEY"},
			},
		})
		val, ok := env.Resolve("TARGET_KEY")
		if !ok || val != "source-value" {
			t.Errorf("got %q, %v", val, ok)
		}
	})

	t.Run("empty_key", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{})
		_, ok := env.Resolve("")
		if ok {
			t.Error("empty key should return false")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{})
		_, ok := env.Resolve("DEFINITELY_NOT_SET_" + t.Name())
		if ok {
			t.Error("missing key should return false")
		}
	})

	t.Run("cycle_detection", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"A": {Value: "${B}"},
				"B": {Value: "${A}"},
			},
		})
		// Should not infinite loop, just fail to resolve
		_, ok := env.Resolve("A")
		// It's ok if not found due to cycle
		_ = ok
	})
}

func TestEnvironmentRuntime_ResolveString(t *testing.T) {
	t.Run("no_templates", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{})
		result, err := env.ResolveString("plain text")
		if err != nil {
			t.Fatal(err)
		}
		if result != "plain text" {
			t.Errorf("got %q", result)
		}
	})

	t.Run("with_template", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"NAME": {Value: "world"},
			},
		})
		result, err := env.ResolveString("hello ${NAME}!")
		if err != nil {
			t.Fatal(err)
		}
		if result != "hello world!" {
			t.Errorf("got %q", result)
		}
	})

	t.Run("strict_unresolved", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{
			Strict: boolPtr(true),
		})
		_, err := env.ResolveString("${MISSING_VAR}")
		if err == nil {
			t.Error("strict mode should error on unresolved")
		}
	})

	t.Run("non_strict_unresolved", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{})
		result, err := env.ResolveString("${MISSING_VAR_TEST_123}")
		if err != nil {
			t.Fatal(err)
		}
		if result != "${MISSING_VAR_TEST_123}" {
			t.Errorf("unresolved template should be kept as-is, got %q", result)
		}
	})

	t.Run("empty", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{})
		result, err := env.ResolveString("")
		if err != nil {
			t.Fatal(err)
		}
		if result != "" {
			t.Error("expected empty")
		}
	})
}

func TestEnvironmentRuntime_ResolveMap(t *testing.T) {
	env := NewEnvironmentRuntime(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"HOST": {Value: "localhost"},
		},
	})

	t.Run("resolves_values", func(t *testing.T) {
		result, err := env.ResolveMap(map[string]string{
			"url": "http://${HOST}:8080",
		})
		if err != nil {
			t.Fatal(err)
		}
		if result["url"] != "http://localhost:8080" {
			t.Errorf("got %q", result["url"])
		}
	})

	t.Run("nil_map", func(t *testing.T) {
		result, err := env.ResolveMap(nil)
		if err != nil {
			t.Fatal(err)
		}
		if result != nil {
			t.Error("expected nil")
		}
	})
}

func TestEnvironmentRuntime_Snapshot(t *testing.T) {
	t.Run("unmasked", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"KEY": {Value: "secret"},
			},
		})
		snap := env.Snapshot(false)
		if snap["KEY"] != "secret" {
			t.Errorf("got %q", snap["KEY"])
		}
	})

	t.Run("masked", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"SECRET": {Value: "mysecret", Masked: boolPtr(true)},
			},
		})
		snap := env.Snapshot(true)
		if snap["SECRET"] != "********" {
			t.Errorf("got %q, want masked", snap["SECRET"])
		}
	})

	t.Run("nil_runtime", func(t *testing.T) {
		var env *EnvironmentRuntime
		if env.Snapshot(false) != nil {
			t.Error("expected nil for nil runtime")
		}
	})

	t.Run("empty_entries", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{})
		if env.Snapshot(false) != nil {
			t.Error("expected nil for empty entries")
		}
	})
}

func TestEnvironmentRuntime_IsMasked(t *testing.T) {
	env := NewEnvironmentRuntime(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"PUBLIC":       {Value: "pub"},
			"MASKED":       {Value: "sec", Masked: boolPtr(true)},
			"REQUIRED":     {Value: "req", Required: boolPtr(true)},
			"NOT_REQUIRED": {Value: "nr", Required: boolPtr(false)},
		},
	})

	tests := []struct {
		key  string
		want bool
	}{
		{"PUBLIC", false},
		{"MASKED", true},
		{"REQUIRED", true},
		{"NOT_REQUIRED", false},
		{"NONEXISTENT", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := env.IsMasked(tt.key); got != tt.want {
				t.Errorf("IsMasked(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}

	t.Run("nil_runtime", func(t *testing.T) {
		var env *EnvironmentRuntime
		if env.IsMasked("KEY") {
			t.Error("nil runtime should not be masked")
		}
	})
}

func TestEnvironmentRuntime_MaskString(t *testing.T) {
	env := NewEnvironmentRuntime(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"API_KEY": {Value: "sk-123456", Masked: boolPtr(true)},
		},
	})

	t.Run("masks_value", func(t *testing.T) {
		result := env.MaskString("My key is sk-123456 ok")
		if result != "My key is ******** ok" {
			t.Errorf("got %q", result)
		}
	})

	t.Run("no_match", func(t *testing.T) {
		result := env.MaskString("nothing to mask here")
		if result != "nothing to mask here" {
			t.Errorf("got %q", result)
		}
	})

	t.Run("nil_runtime", func(t *testing.T) {
		var env *EnvironmentRuntime
		if env.MaskString("text") != "text" {
			t.Error("nil runtime should return original text")
		}
	})

	t.Run("empty_text", func(t *testing.T) {
		if env.MaskString("") != "" {
			t.Error("empty text should return empty")
		}
	})
}

func TestEnvironmentRuntime_Strict(t *testing.T) {
	t.Run("strict_true", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{Strict: boolPtr(true)})
		if !env.Strict() {
			t.Error("expected strict")
		}
	})

	t.Run("strict_false", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{Strict: boolPtr(false)})
		if env.Strict() {
			t.Error("expected not strict")
		}
	})

	t.Run("strict_nil", func(t *testing.T) {
		env := NewEnvironmentRuntime(config.EnvironmentConfig{})
		if env.Strict() {
			t.Error("nil strict should default to false")
		}
	})

	t.Run("nil_runtime", func(t *testing.T) {
		var env *EnvironmentRuntime
		if env.Strict() {
			t.Error("nil runtime should not be strict")
		}
	})
}

func TestEnvironmentRuntime_Reload(t *testing.T) {
	env := NewEnvironmentRuntime(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"KEY": {Value: "old"},
		},
	})
	val, ok := env.Resolve("KEY")
	if !ok || val != "old" {
		t.Fatal("initial value wrong")
	}

	env.Reload(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"KEY": {Value: "new"},
		},
	})
	val, ok = env.Resolve("KEY")
	if !ok || val != "new" {
		t.Errorf("after Reload got %q, want %q", val, "new")
	}
}

func TestEnvironmentRuntime_AppendToEnv(t *testing.T) {
	env := NewEnvironmentRuntime(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"FROM_CFG": {Value: "cfgval"},
		},
	})

	t.Run("merges_all_sources", func(t *testing.T) {
		result, err := env.AppendToEnv([]string{"EXISTING=exist"}, map[string]string{"OVER": "overval"})
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, item := range result {
			parts := splitEnvPair(item)
			found[parts[0]] = parts[1]
		}
		if found["EXISTING"] != "exist" {
			t.Errorf("EXISTING = %q", found["EXISTING"])
		}
		if found["OVER"] != "overval" {
			t.Errorf("OVER = %q", found["OVER"])
		}
		if found["FROM_CFG"] != "cfgval" {
			t.Errorf("FROM_CFG = %q", found["FROM_CFG"])
		}
	})

	t.Run("overrides_take_precedence", func(t *testing.T) {
		result, err := env.AppendToEnv([]string{"KEY=old"}, map[string]string{"KEY": "new"})
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, item := range result {
			parts := splitEnvPair(item)
			found[parts[0]] = parts[1]
		}
		if found["KEY"] != "new" {
			t.Errorf("override should win, got %q", found["KEY"])
		}
	})
}

func TestUniqueStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"dedupes", []string{"a", "b", "a", "c"}, []string{"a", "b", "c"}},
		{"empty", nil, nil},
		{"trims_whitespace", []string{" a ", " a", "b "}, []string{"a", "b"}},
		{"skips_blank", []string{"", " ", "a"}, []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UniqueStrings(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("len = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func splitEnvPair(item string) [2]string {
	for i, ch := range item {
		if ch == '=' {
			return [2]string{item[:i], item[i+1:]}
		}
	}
	return [2]string{item, ""}
}

func TestEnvironmentTemplatePattern(t *testing.T) {
	matches := environmentTemplatePattern.FindAllString("hello ${WORLD} and ${FOO_123}", -1)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0] != "${WORLD}" {
		t.Errorf("[0] = %q", matches[0])
	}
	if matches[1] != "${FOO_123}" {
		t.Errorf("[1] = %q", matches[1])
	}

	noMatches := environmentTemplatePattern.FindAllString("no templates here $plain", -1)
	if len(noMatches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(noMatches))
	}
}

func TestAppendAgentRuntimeEnv(t *testing.T) {
	t.Run("with_agent_dir", func(t *testing.T) {
		identity := core_AgentIdentityStub("test", "/tmp/agents/test")
		result, err := AppendAgentRuntimeEnv(nil, identity, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, item := range result {
			parts := splitEnvPair(item)
			found[parts[0]] = parts[1]
		}
		if found["KOCORT_AGENT_DIR"] != "/tmp/agents/test" {
			t.Errorf("KOCORT_AGENT_DIR = %q", found["KOCORT_AGENT_DIR"])
		}
	})

	t.Run("no_agent_dir_no_env", func(t *testing.T) {
		identity := core_AgentIdentityStub("test", "")
		result, err := AppendAgentRuntimeEnv([]string{"A=B"}, identity, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 1 || result[0] != "A=B" {
			t.Errorf("expected original env, got %v", result)
		}
	})

	t.Run("with_environment_appender", func(t *testing.T) {
		identity := core_AgentIdentityStub("test", "")
		env := NewEnvironmentRuntime(config.EnvironmentConfig{
			Entries: map[string]config.EnvironmentEntryConfig{
				"CFG_KEY": {Value: "cfgval"},
			},
		})
		result, err := AppendAgentRuntimeEnv(nil, identity, env, map[string]string{"EXTRA": "val"})
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, item := range result {
			parts := splitEnvPair(item)
			found[parts[0]] = parts[1]
		}
		if found["EXTRA"] != "val" {
			t.Errorf("EXTRA = %q", found["EXTRA"])
		}
		if found["CFG_KEY"] != "cfgval" {
			t.Errorf("CFG_KEY = %q", found["CFG_KEY"])
		}
	})
}

// Helper to create a minimal AgentIdentity for testing AppendAgentRuntimeEnv
func core_AgentIdentityStub(id, agentDir string) core.AgentIdentity {
	return core.AgentIdentity{ID: id, AgentDir: agentDir}
}
