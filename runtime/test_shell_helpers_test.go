package runtime

import (
	"os"
	"os/exec"
	stdruntime "runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/infra"
)

type testShellHelper struct {
	spec          infra.ShellSpec
	powerShell    bool
	pwdExpression string
}

func newTestShellHelper(t *testing.T) testShellHelper {
	t.Helper()
	spec, err := infra.ResolveCommandShell(stdruntime.GOOS, os.Getenv, exec.LookPath)
	if err != nil {
		t.Fatalf("resolve test shell: %v", err)
	}
	lower := strings.ToLower(strings.TrimSpace(spec.Path))
	powerShell := strings.Contains(lower, "pwsh") || strings.Contains(lower, "powershell")
	pwdExpression := "$PWD"
	if powerShell {
		pwdExpression = "(Get-Location).Path"
	}
	return testShellHelper{spec: spec, powerShell: powerShell, pwdExpression: pwdExpression}
}

func (h testShellHelper) Command(script string) (string, []string) {
	return h.spec.Path, h.Args(script)
}

func (h testShellHelper) Args(script string) []string {
	args := append([]string{}, h.spec.Args...)
	return append(args, script)
}

func (h testShellHelper) AllowTrailingArgs(script string) string {
	if !h.powerShell {
		return script
	}
	return "& { param([Parameter(ValueFromRemainingArguments = $true)][object[]]$RemainingArgs) " + script + " }"
}

func (h testShellHelper) quote(text string) string {
	if h.powerShell {
		return "'" + strings.ReplaceAll(text, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'"
}

func (h testShellHelper) envRef(name string) string {
	if h.powerShell {
		return "$env:" + name
	}
	return "${" + name + "}"
}

func (h testShellHelper) StdinEchoScript() string {
	if h.powerShell {
		return "[Console]::Out.Write([Console]::In.ReadToEnd())"
	}
	return "cat"
}

func (h testShellHelper) LinesScript(lines ...string) string {
	if h.powerShell {
		statements := make([]string, 0, len(lines))
		for _, line := range lines {
			statements = append(statements, "Write-Output "+h.quote(line))
		}
		return strings.Join(statements, "; ")
	}
	quoted := make([]string, 0, len(lines))
	for _, line := range lines {
		quoted = append(quoted, h.quote(line))
	}
	return "printf '%s\n' " + strings.Join(quoted, " ")
}

func (h testShellHelper) SleepScript(seconds int) string {
	if h.powerShell {
		return "Start-Sleep -Seconds " + strconv.Itoa(seconds)
	}
	return "sleep " + strconv.Itoa(seconds)
}

func (h testShellHelper) JoinedEnvScript(names ...string) string {
	if h.powerShell {
		parts := make([]string, 0, len(names)*2)
		for index, name := range names {
			if index > 0 {
				parts = append(parts, h.quote("|"))
			}
			parts = append(parts, h.envRef(name))
		}
		return "[Console]::Out.Write(" + strings.Join(parts, " + ") + ")"
	}
	refs := make([]string, 0, len(names))
	for _, name := range names {
		refs = append(refs, h.envRef(name))
	}
	return "printf %s \"" + strings.Join(refs, "|") + "\""
}

func (h testShellHelper) StderrExitScript(message string, code int) string {
	if h.powerShell {
		return "[Console]::Error.WriteLine(" + h.quote(message) + "); exit " + strconv.Itoa(code)
	}
	return "printf '%s\n' " + h.quote(message) + " 1>&2; exit " + strconv.Itoa(code)
}

func (h testShellHelper) DelayedOutputScript(first string, second string, seconds int) string {
	if h.powerShell {
		return "Write-Output " + h.quote(first) + "; Start-Sleep -Seconds " + strconv.Itoa(seconds) + "; Write-Output " + h.quote(second)
	}
	return "printf '%s\n' " + h.quote(first) + "; sleep " + strconv.Itoa(seconds) + "; printf '%s\n' " + h.quote(second)
}

func (h testShellHelper) PwdAndSandboxEnvScript() string {
	if h.powerShell {
		return strings.Join([]string{
			"[Console]::Out.WriteLine(" + h.pwdExpression + ")",
			"[Console]::Out.WriteLine(" + h.envRef("KOCORT_SANDBOX_WORKSPACE") + ")",
			"[Console]::Out.Write(" + h.envRef("KOCORT_SANDBOX_DIRS") + ")",
		}, "; ")
	}
	return "printf '%s\n%s\n%s' \"$PWD\" \"$KOCORT_SANDBOX_WORKSPACE\" \"$KOCORT_SANDBOX_DIRS\""
}
