package infra

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type ShellSpec struct {
	Path string
	Args []string
}

func CommandShellContext(ctx context.Context, command string) (*exec.Cmd, error) {
	spec, err := ResolveCommandShell(runtime.GOOS, os.Getenv, exec.LookPath)
	if err != nil {
		return nil, err
	}
	args := append(append([]string{}, spec.Args...), PrepareShellCommand(spec, command))
	return exec.CommandContext(ctx, spec.Path, args...), nil
}

func PrepareShellCommand(spec ShellSpec, command string) string {
	if !isPowerShellShell(spec.Path) {
		return command
	}
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return command
	}
	return "& { $utf8NoBom = New-Object System.Text.UTF8Encoding $false; [Console]::InputEncoding = $utf8NoBom; [Console]::OutputEncoding = $utf8NoBom; $OutputEncoding = $utf8NoBom; " + command + " }"
}

func ResolveCommandShell(goos string, getenv func(string) string, lookPath func(string) (string, error)) (ShellSpec, error) {
	if shell := strings.TrimSpace(getenv("KOCORT_SHELL")); shell != "" {
		args := ShellArgsFor(shell, strings.TrimSpace(getenv("KOCORT_SHELL_ARGS")), goos)
		return ShellSpec{Path: shell, Args: args}, nil
	}
	if goos == "windows" {
		for _, candidate := range []struct {
			name string
			args []string
		}{
			{name: "pwsh", args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
			{name: "powershell.exe", args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
		} {
			if path, err := lookPath(candidate.name); err == nil {
				return ShellSpec{Path: path, Args: candidate.args}, nil
			}
		}
		if comspec := strings.TrimSpace(getenv("COMSPEC")); comspec != "" {
			return ShellSpec{Path: comspec, Args: []string{"/C"}}, nil
		}
		if path, err := lookPath("cmd.exe"); err == nil {
			return ShellSpec{Path: path, Args: []string{"/C"}}, nil
		}
		return ShellSpec{}, fmt.Errorf("no supported shell found on windows (tried pwsh, powershell.exe, COMSPEC, cmd.exe)")
	}
	for _, candidate := range []string{"bash", "sh"} {
		if path, err := lookPath(candidate); err == nil {
			return ShellSpec{Path: path, Args: []string{"-lc"}}, nil
		}
	}
	if shell := strings.TrimSpace(getenv("SHELL")); shell != "" {
		return ShellSpec{Path: shell, Args: []string{"-lc"}}, nil
	}
	return ShellSpec{}, fmt.Errorf("no supported shell found (tried bash, sh, SHELL)")
}

func ShellArgsFor(shellPath string, rawArgs string, goos string) []string {
	if rawArgs != "" {
		return strings.Fields(rawArgs)
	}
	lowerPath := strings.ToLower(strings.TrimSpace(shellPath))
	if goos == "windows" {
		if isPowerShellShell(lowerPath) {
			return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}
		}
		return []string{"/C"}
	}
	return []string{"-lc"}
}

func isPowerShellShell(shellPath string) bool {
	lowerPath := strings.ToLower(strings.TrimSpace(shellPath))
	return strings.Contains(lowerPath, "pwsh") || strings.Contains(lowerPath, "powershell")
}
