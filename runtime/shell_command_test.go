package runtime

import (
	"errors"
	"reflect"
	"testing"

	"github.com/kocort/kocort/internal/infra"
)

func TestResolveCommandShellUnixFallback(t *testing.T) {
	spec, err := infra.ResolveCommandShell("linux", func(string) string { return "" }, func(name string) (string, error) {
		switch name {
		case "bash":
			return "", errors.New("missing")
		case "sh":
			return "/bin/sh", nil
		default:
			return "", errors.New("missing")
		}
	})
	if err != nil {
		t.Fatalf("resolve shell: %v", err)
	}
	if spec.Path != "/bin/sh" {
		t.Fatalf("expected /bin/sh, got %q", spec.Path)
	}
	if !reflect.DeepEqual(spec.Args, []string{"-lc"}) {
		t.Fatalf("expected -lc args, got %#v", spec.Args)
	}
}

func TestResolveCommandShellWindowsPrefersPwsh(t *testing.T) {
	spec, err := infra.ResolveCommandShell("windows", func(string) string { return "" }, func(name string) (string, error) {
		if name == "pwsh" {
			return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
		}
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatalf("resolve shell: %v", err)
	}
	if spec.Path != `C:\Program Files\PowerShell\7\pwsh.exe` {
		t.Fatalf("unexpected shell path %q", spec.Path)
	}
	if !reflect.DeepEqual(spec.Args, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}) {
		t.Fatalf("unexpected args %#v", spec.Args)
	}
}

func TestResolveCommandShellWindowsFallsBackToComspec(t *testing.T) {
	spec, err := infra.ResolveCommandShell("windows", func(key string) string {
		if key == "COMSPEC" {
			return `C:\Windows\System32\cmd.exe`
		}
		return ""
	}, func(string) (string, error) {
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatalf("resolve shell: %v", err)
	}
	if spec.Path != `C:\Windows\System32\cmd.exe` {
		t.Fatalf("unexpected COMSPEC path %q", spec.Path)
	}
	if !reflect.DeepEqual(spec.Args, []string{"/C"}) {
		t.Fatalf("unexpected args %#v", spec.Args)
	}
}

func TestResolveCommandShellHonorsOverride(t *testing.T) {
	env := func(key string) string {
		switch key {
		case "KOCORT_SHELL":
			return "/custom/shell"
		case "KOCORT_SHELL_ARGS":
			return "--flag -c"
		default:
			return ""
		}
	}
	spec, err := infra.ResolveCommandShell("linux", env, func(string) (string, error) {
		return "", errors.New("should not be called")
	})
	if err != nil {
		t.Fatalf("resolve shell: %v", err)
	}
	if spec.Path != "/custom/shell" {
		t.Fatalf("unexpected override path %q", spec.Path)
	}
	if !reflect.DeepEqual(spec.Args, []string{"--flag", "-c"}) {
		t.Fatalf("unexpected args %#v", spec.Args)
	}
}
