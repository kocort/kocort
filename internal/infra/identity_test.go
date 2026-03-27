package infra

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestParseIdentityMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    IdentityFile
	}{
		{
			name:    "full",
			content: "- **Name**: Claw\n- **Emoji**: 🦀\n- **Theme**: ocean\n- **Creature**: crab\n- **Vibe**: calm\n- **Avatar**: claw.png",
			want:    IdentityFile{Name: "Claw", Emoji: "🦀", Theme: "ocean", Creature: "crab", Vibe: "calm", Avatar: "claw.png"},
		},
		{
			name:    "plain_colons",
			content: "Name: Axle\nEmoji: ⚙️",
			want:    IdentityFile{Name: "Axle", Emoji: "⚙️"},
		},
		{
			name:    "empty",
			content: "",
			want:    IdentityFile{},
		},
		{
			name:    "no_colon",
			content: "just some text\nanother line",
			want:    IdentityFile{},
		},
		{
			name:    "blank_values",
			content: "Name: \nEmoji: ",
			want:    IdentityFile{},
		},
		{
			name:    "case_insensitive_labels",
			content: "NAME: Upper\ntheme: dark",
			want:    IdentityFile{Name: "Upper", Theme: "dark"},
		},
		{
			name:    "markdown_bold",
			content: "- **Name**: _Bold_\n- **Vibe**: *chill*",
			want:    IdentityFile{Name: "Bold", Vibe: "chill"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseIdentityMarkdown(tt.content)
			if got != tt.want {
				t.Errorf("ParseIdentityMarkdown() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestStaticIdentityResolver_Resolve(t *testing.T) {
	t.Run("default_identity", func(t *testing.T) {
		resolver := NewStaticIdentityResolver(nil)
		identity, err := resolver.Resolve(context.Background(), "main")
		if err != nil {
			t.Fatal(err)
		}
		if identity.ID != "main" {
			t.Errorf("ID = %q, want %q", identity.ID, "main")
		}
		if identity.DefaultProvider != "openai" {
			t.Errorf("DefaultProvider = %q, want %q", identity.DefaultProvider, "openai")
		}
		if identity.DefaultModel != "gpt-4.1" {
			t.Errorf("DefaultModel = %q, want %q", identity.DefaultModel, "gpt-4.1")
		}
	})

	t.Run("configured_identity", func(t *testing.T) {
		dir := t.TempDir()
		resolver := NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"bot": {
				Name:            "MyBot",
				DefaultProvider: "anthropic",
				DefaultModel:    "claude-3",
				WorkspaceDir:    dir,
			},
		})
		identity, err := resolver.Resolve(context.Background(), "bot")
		if err != nil {
			t.Fatal(err)
		}
		if identity.ID != "bot" {
			t.Errorf("ID = %q, want %q", identity.ID, "bot")
		}
		if identity.Name != "MyBot" {
			t.Errorf("Name = %q, want %q", identity.Name, "MyBot")
		}
		if identity.DefaultProvider != "anthropic" {
			t.Errorf("DefaultProvider = %q, want %q", identity.DefaultProvider, "anthropic")
		}
	})

	t.Run("configured_identity_allows_empty_default_model", func(t *testing.T) {
		dir := t.TempDir()
		resolver := NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"bare": {WorkspaceDir: dir},
		})
		identity, err := resolver.Resolve(context.Background(), "bare")
		if err != nil {
			t.Fatal(err)
		}
		if identity.ID != "bare" {
			t.Errorf("ID should be filled")
		}
		if identity.Name != "bare" {
			t.Errorf("Name should default to ID")
		}
		if identity.DefaultProvider != "" {
			t.Errorf("DefaultProvider = %q, want empty", identity.DefaultProvider)
		}
		if identity.DefaultModel != "" {
			t.Errorf("DefaultModel = %q, want empty", identity.DefaultModel)
		}
	})

	t.Run("normalizes_agent_id", func(t *testing.T) {
		resolver := NewStaticIdentityResolver(nil)
		identity, err := resolver.Resolve(context.Background(), "  MAIN  ")
		if err != nil {
			t.Fatal(err)
		}
		if identity.ID != "main" {
			t.Errorf("ID = %q, want normalized %q", identity.ID, "main")
		}
	})
}

func TestStaticIdentityResolver_List(t *testing.T) {
	t.Run("nil_resolver", func(t *testing.T) {
		var r *StaticIdentityResolver
		if r.List() != nil {
			t.Error("nil resolver should return nil")
		}
	})

	t.Run("empty", func(t *testing.T) {
		resolver := NewStaticIdentityResolver(nil)
		if resolver.List() != nil {
			t.Error("empty resolver should return nil")
		}
	})

	t.Run("with_identities", func(t *testing.T) {
		dir := t.TempDir()
		resolver := NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"a": {Name: "A", WorkspaceDir: dir},
		})
		list := resolver.List()
		if len(list) != 1 {
			t.Fatalf("expected 1 identity, got %d", len(list))
		}
	})
}

func TestApplyWorkspaceIdentityFile(t *testing.T) {
	t.Run("no_identity_file", func(t *testing.T) {
		dir := t.TempDir()
		identity := core.AgentIdentity{ID: "test", Name: "test", WorkspaceDir: dir}
		result := ApplyWorkspaceIdentityFile(identity)
		if result.ID != "test" {
			t.Error("identity should be unchanged")
		}
	})

	t.Run("with_identity_file", func(t *testing.T) {
		dir := t.TempDir()
		content := "Name: Claw\nEmoji: 🦀\nTheme: ocean\nVibe: calm\nCreature: crab"
		if err := os.WriteFile(filepath.Join(dir, DefaultIdentityFilename), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		identity := core.AgentIdentity{ID: "test", Name: "test", WorkspaceDir: dir}
		result := ApplyWorkspaceIdentityFile(identity)
		if result.Name != "Claw" {
			t.Errorf("Name = %q, want %q", result.Name, "Claw")
		}
		if result.Emoji != "🦀" {
			t.Errorf("Emoji = %q, want %q", result.Emoji, "🦀")
		}
		if result.Theme != "ocean" {
			t.Errorf("Theme = %q, want %q", result.Theme, "ocean")
		}
		if result.PersonaPrompt == "" {
			t.Error("PersonaPrompt should be generated")
		}
	})

	t.Run("does_not_overwrite_explicit_name", func(t *testing.T) {
		dir := t.TempDir()
		content := "Name: Claw"
		if err := os.WriteFile(filepath.Join(dir, DefaultIdentityFilename), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		identity := core.AgentIdentity{ID: "test", Name: "ExplicitName", WorkspaceDir: dir}
		result := ApplyWorkspaceIdentityFile(identity)
		if result.Name != "ExplicitName" {
			t.Errorf("Name should not be overwritten, got %q", result.Name)
		}
	})

	t.Run("empty_workspace_dir", func(t *testing.T) {
		identity := core.AgentIdentity{ID: "test", Name: "test", WorkspaceDir: ""}
		result := ApplyWorkspaceIdentityFile(identity)
		if result.ID != "test" {
			t.Error("identity should be unchanged with empty workspace dir")
		}
	})
}

func TestNullMemoryProvider(t *testing.T) {
	provider := NullMemoryProvider{}
	hits, err := provider.Recall(context.Background(), core.AgentIdentity{}, core.SessionResolution{}, "query")
	if err != nil {
		t.Fatal(err)
	}
	if hits != nil {
		t.Error("NullMemoryProvider should return nil hits")
	}
}
