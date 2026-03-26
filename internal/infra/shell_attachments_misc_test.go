package infra

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func TestResolveCommandShell(t *testing.T) {
	alwaysFind := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	neverFind := func(name string) (string, error) { return "", &notFoundError{name} }
	emptyEnv := func(key string) string { return "" }

	t.Run("unix_finds_bash", func(t *testing.T) {
		spec, err := ResolveCommandShell("linux", emptyEnv, alwaysFind)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Path != "/usr/bin/bash" {
			t.Errorf("Path = %q", spec.Path)
		}
		if len(spec.Args) == 0 || spec.Args[0] != "-lc" {
			t.Errorf("Args = %v", spec.Args)
		}
	})

	t.Run("unix_falls_back_to_sh", func(t *testing.T) {
		callCount := 0
		lookPath := func(name string) (string, error) {
			callCount++
			if name == "bash" {
				return "", &notFoundError{"bash"}
			}
			return "/bin/sh", nil
		}
		spec, err := ResolveCommandShell("linux", emptyEnv, lookPath)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Path != "/bin/sh" {
			t.Errorf("Path = %q", spec.Path)
		}
	})

	t.Run("unix_falls_back_to_SHELL_env", func(t *testing.T) {
		env := func(key string) string {
			if key == "SHELL" {
				return "/usr/local/bin/zsh"
			}
			return ""
		}
		spec, err := ResolveCommandShell("linux", env, neverFind)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Path != "/usr/local/bin/zsh" {
			t.Errorf("Path = %q", spec.Path)
		}
	})

	t.Run("unix_no_shell_error", func(t *testing.T) {
		_, err := ResolveCommandShell("linux", emptyEnv, neverFind)
		if err == nil {
			t.Error("expected error when no shell found")
		}
	})

	t.Run("windows_finds_pwsh", func(t *testing.T) {
		spec, err := ResolveCommandShell("windows", emptyEnv, alwaysFind)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Path != "/usr/bin/pwsh" {
			t.Errorf("Path = %q", spec.Path)
		}
	})

	t.Run("windows_falls_back_to_comspec", func(t *testing.T) {
		env := func(key string) string {
			if key == "COMSPEC" {
				return `C:\Windows\System32\cmd.exe`
			}
			return ""
		}
		spec, err := ResolveCommandShell("windows", env, neverFind)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Path != `C:\Windows\System32\cmd.exe` {
			t.Errorf("Path = %q", spec.Path)
		}
	})

	t.Run("windows_no_shell_error", func(t *testing.T) {
		_, err := ResolveCommandShell("windows", emptyEnv, neverFind)
		if err == nil {
			t.Error("expected error when no shell found on windows")
		}
	})

	t.Run("custom_shell_env", func(t *testing.T) {
		env := func(key string) string {
			if key == "KOCORT_SHELL" {
				return "/custom/shell"
			}
			return ""
		}
		spec, err := ResolveCommandShell("linux", env, neverFind)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Path != "/custom/shell" {
			t.Errorf("Path = %q", spec.Path)
		}
	})

	t.Run("custom_shell_with_args", func(t *testing.T) {
		env := func(key string) string {
			switch key {
			case "KOCORT_SHELL":
				return "/custom/shell"
			case "KOCORT_SHELL_ARGS":
				return "-x -c"
			}
			return ""
		}
		spec, err := ResolveCommandShell("linux", env, neverFind)
		if err != nil {
			t.Fatal(err)
		}
		if len(spec.Args) != 2 || spec.Args[0] != "-x" || spec.Args[1] != "-c" {
			t.Errorf("Args = %v", spec.Args)
		}
	})
}

type notFoundError struct{ name string }

func (e *notFoundError) Error() string { return e.name + " not found" }

func TestShellArgsFor(t *testing.T) {
	tests := []struct {
		name      string
		shell     string
		rawArgs   string
		goos      string
		wantLen   int
		wantFirst string
	}{
		{"explicit_args", "/bin/bash", "-c", "linux", 1, "-c"},
		{"linux_default", "/bin/bash", "", "linux", 1, "-lc"},
		{"windows_pwsh", "pwsh.exe", "", "windows", 4, "-NoLogo"},
		{"windows_cmd", "cmd.exe", "", "windows", 1, "/C"},
		{"windows_powershell", "C:\\powershell.exe", "", "windows", 4, "-NoLogo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := ShellArgsFor(tt.shell, tt.rawArgs, tt.goos)
			if len(args) != tt.wantLen {
				t.Errorf("len = %d, want %d: %v", len(args), tt.wantLen, args)
				return
			}
			if args[0] != tt.wantFirst {
				t.Errorf("first arg = %q, want %q", args[0], tt.wantFirst)
			}
		})
	}
}

func TestAttachmentDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		att      core.Attachment
		fallback string
		want     string
	}{
		{"name_set", core.Attachment{Name: "file.txt"}, "", "file.txt"},
		{"type_set", core.Attachment{Type: "document"}, "", "document"},
		{"fallback", core.Attachment{}, "default", "default"},
		{"no_fallback", core.Attachment{}, "", "attachment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AttachmentDisplayName(tt.att, tt.fallback)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeAttachmentMime(t *testing.T) {
	tests := []struct {
		name string
		att  core.Attachment
		want string
	}{
		{"explicit", core.Attachment{MIMEType: "text/plain; charset=utf-8"}, "text/plain"},
		{"from_extension", core.Attachment{Name: "file.json"}, "application/json"},
		{"no_info", core.Attachment{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeAttachmentMime(tt.att)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAttachmentIsImage(t *testing.T) {
	if !AttachmentIsImage(core.Attachment{MIMEType: "image/png"}) {
		t.Error("should be image")
	}
	if AttachmentIsImage(core.Attachment{MIMEType: "text/plain"}) {
		t.Error("text should not be image")
	}
}

func TestAttachmentLikelyText(t *testing.T) {
	tests := []struct {
		name string
		att  core.Attachment
		want bool
	}{
		{"text_mime", core.Attachment{MIMEType: "text/plain"}, true},
		{"json_mime", core.Attachment{MIMEType: "application/json"}, true},
		{"go_ext", core.Attachment{Name: "main.go"}, true},
		{"py_ext", core.Attachment{Name: "script.py"}, true},
		{"binary_ext", core.Attachment{Name: "image.png"}, false},
		{"dockerfile", core.Attachment{Name: "Dockerfile"}, true},
		{"makefile", core.Attachment{Name: "Makefile"}, true},
		{"no_info", core.Attachment{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AttachmentLikelyText(tt.att)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAttachmentPromptText(t *testing.T) {
	t.Run("valid_text", func(t *testing.T) {
		att := core.Attachment{Name: "test.txt", MIMEType: "text/plain", Content: []byte("hello world")}
		text, ok, truncated := AttachmentPromptText(att)
		if !ok {
			t.Error("should be ok for text attachment")
		}
		if text != "hello world" {
			t.Errorf("text = %q", text)
		}
		if truncated {
			t.Error("should not be truncated")
		}
	})

	t.Run("empty_content", func(t *testing.T) {
		att := core.Attachment{Name: "test.txt", MIMEType: "text/plain"}
		_, ok, _ := AttachmentPromptText(att)
		if ok {
			t.Error("empty content should not be ok")
		}
	})

	t.Run("binary_content", func(t *testing.T) {
		att := core.Attachment{Name: "test.txt", MIMEType: "text/plain", Content: []byte{0xFF, 0xFE}}
		_, ok, _ := AttachmentPromptText(att)
		if ok {
			t.Error("invalid utf8 should not be ok")
		}
	})

	t.Run("truncation", func(t *testing.T) {
		longText := make([]byte, MaxPromptAttachmentTextChars+100)
		for i := range longText {
			longText[i] = 'a'
		}
		att := core.Attachment{Name: "big.txt", MIMEType: "text/plain", Content: longText}
		text, ok, truncated := AttachmentPromptText(att)
		if !ok {
			t.Error("should be ok")
		}
		if !truncated {
			t.Error("should be truncated")
		}
		if len(text) > MaxPromptAttachmentTextChars {
			t.Error("text should be clipped to max chars")
		}
	})
}

func TestAttachmentDataURL(t *testing.T) {
	t.Run("with_content", func(t *testing.T) {
		att := core.Attachment{MIMEType: "image/png", Content: []byte{0x89, 0x50}}
		url := AttachmentDataURL(att)
		if url == "" {
			t.Error("should return data URL")
		}
		if !contains(url, "data:image/png;base64,") {
			t.Errorf("unexpected URL: %q", url)
		}
	})

	t.Run("empty_content", func(t *testing.T) {
		att := core.Attachment{}
		if AttachmentDataURL(att) != "" {
			t.Error("empty content should return empty string")
		}
	})

	t.Run("default_mime", func(t *testing.T) {
		att := core.Attachment{Content: []byte{1, 2, 3}}
		url := AttachmentDataURL(att)
		if !contains(url, "application/octet-stream") {
			t.Errorf("should default mime, got %q", url)
		}
	})
}

func TestInjectTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		message string
		tz      string
		wantPfx string
	}{
		{"basic", "hello", "", "["},
		{"already_stamped", "[Mon 2024-01-01 10:00 UTC] hello", "", "[Mon"},
		{"empty", "", "", ""},
		{"with_timezone", "hello", "America/New_York", "["},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InjectTimestamp(tt.message, tt.tz, fixedTestTime())
			if tt.wantPfx != "" && !hasPrefix(result, tt.wantPfx) {
				t.Errorf("got %q, want prefix %q", result, tt.wantPfx)
			}
			if tt.message == "" && result != "" {
				t.Error("empty message should stay empty")
			}
		})
	}

	t.Run("already_stamped_not_double", func(t *testing.T) {
		msg := "[Mon 2024-01-01 10:00 UTC] hello"
		result := InjectTimestamp(msg, "", fixedTestTime())
		if result != msg {
			t.Errorf("should not double-stamp, got %q", result)
		}
	})
}

func TestHasUnbackedReminderCommitment(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"has_commitment", "I'll remind you tomorrow", true},
		{"no_commitment", "Sure, I can help with that", false},
		{"empty", "", false},
		{"with_note_already", "I'll remind you " + UnscheduledReminderNote, false},
		{"i_will_follow_up", "I will follow up on that", true},
		{"i_will_set_reminder", "I will set a reminder for you", true},
		{"i_will_schedule", "I'll schedule a reminder", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasUnbackedReminderCommitment(tt.text); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasSessionRelatedScheduledTasks(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		tasks := []core.TaskRecord{
			{Kind: core.TaskKindScheduled, Status: core.TaskStatusScheduled, SessionKey: "s1"},
		}
		if !HasSessionRelatedScheduledTasks(tasks, "s1") {
			t.Error("should find scheduled task")
		}
	})

	t.Run("canceled_excluded", func(t *testing.T) {
		tasks := []core.TaskRecord{
			{Kind: core.TaskKindScheduled, Status: core.TaskStatusCanceled, SessionKey: "s1"},
		}
		if HasSessionRelatedScheduledTasks(tasks, "s1") {
			t.Error("canceled tasks should be excluded")
		}
	})

	t.Run("different_session", func(t *testing.T) {
		tasks := []core.TaskRecord{
			{Kind: core.TaskKindScheduled, Status: core.TaskStatusScheduled, SessionKey: "s2"},
		}
		if HasSessionRelatedScheduledTasks(tasks, "s1") {
			t.Error("different session should not match")
		}
	})

	t.Run("empty_session_key", func(t *testing.T) {
		if HasSessionRelatedScheduledTasks(nil, "") {
			t.Error("empty session key should return false")
		}
	})
}

func TestAppendUnscheduledReminderNote(t *testing.T) {
	t.Run("appends_note", func(t *testing.T) {
		payloads := []core.ReplyPayload{
			{Text: "I'll remind you tomorrow"},
		}
		result := AppendUnscheduledReminderNote(payloads)
		if len(result) != 1 {
			t.Fatal("expected 1 payload")
		}
		if !contains(result[0].Text, UnscheduledReminderNote) {
			t.Error("note should be appended")
		}
	})

	t.Run("no_commitment", func(t *testing.T) {
		payloads := []core.ReplyPayload{
			{Text: "Sure, I'll help"},
		}
		result := AppendUnscheduledReminderNote(payloads)
		if contains(result[0].Text, UnscheduledReminderNote) {
			t.Error("note should not be appended without commitment")
		}
	})

	t.Run("error_payload_skipped", func(t *testing.T) {
		payloads := []core.ReplyPayload{
			{Text: "I'll remind you", IsError: true},
		}
		result := AppendUnscheduledReminderNote(payloads)
		if contains(result[0].Text, UnscheduledReminderNote) {
			t.Error("error payloads should be skipped")
		}
	})

	t.Run("only_first_payload", func(t *testing.T) {
		payloads := []core.ReplyPayload{
			{Text: "I'll remind you"},
			{Text: "I'll follow up"},
		}
		result := AppendUnscheduledReminderNote(payloads)
		if !contains(result[0].Text, UnscheduledReminderNote) {
			t.Error("first should have note")
		}
		if contains(result[1].Text, UnscheduledReminderNote) {
			t.Error("only first match should get note")
		}
	})
}

func TestParseSlogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},      // default
		{"unknown", slog.LevelInfo}, // default
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ParseSlogLevel(tt.input); got != tt.want {
				t.Errorf("ParseSlogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSlogAuditLoggerLogAuditEvent(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))
	defer func() {
		// Restore default handler.
		ApplySlogLevel(config.LoggingConfig{Level: "info"})
	}()

	logger, err := NewSlogAuditLogger(config.LoggingConfig{Level: "debug"}, "")
	if err != nil {
		t.Fatalf("NewSlogAuditLogger: %v", err)
	}
	logger.LogAuditEvent(core.AuditEvent{
		Level:      "info",
		Category:   "system",
		Type:       "boot",
		SessionKey: "s1",
		RunID:      "r1",
		ToolName:   "shell",
		TaskID:     "t1",
		Message:    "started",
		Data:       map[string]any{"key": "val"},
	})

	text := buf.String()
	if text == "" {
		t.Error("expected slog output, got empty")
	}
	for _, expected := range []string{"system", "boot", "s1", "r1", "shell", "t1", "started"} {
		if !contains(text, expected) {
			t.Errorf("missing %q in slog output %q", expected, text)
		}
	}
}

// Helper functions

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func fixedTestTime() time.Time {
	return time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
}
