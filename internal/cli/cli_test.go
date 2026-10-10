package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseInterspersedFlags(t *testing.T) {
	for _, args := range [][]string{
		{"codex", "2", "--layout", "even-horizontal", "--session=feat-x", "--cwd", "/tmp/project space", "--no-attach"},
		{"--no-attach", "--layout=even-horizontal", "codex", "--session", "feat-x", "2", "--cwd=/tmp/project space"},
	} {
		parsed, err := Parse(args)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(parsed.Positionals, []string{"codex", "2"}) || parsed.Layout != "even-horizontal" || parsed.Session != "feat-x" || parsed.Cwd != "/tmp/project space" || !parsed.NoAttach {
			t.Fatalf("%+v", parsed)
		}
	}
	parsed, err := Parse([]string{"--", "codex", "--no-attach"})
	if err != nil || !reflect.DeepEqual(parsed.Positionals, []string{"codex", "--no-attach"}) || parsed.NoAttach {
		t.Fatalf("%+v: %v", parsed, err)
	}
}

func TestParseRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{{"codex", "--typo"}, {"--layout"}, {"--cwd", "--no-attach"}, {"--no-attach=maybe"}} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestHelpAndVersionNeedNoDependencies(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "relative-invalid-config")
	for _, args := range [][]string{{"--help"}, {"help"}, {"--version"}} {
		var out, stderr bytes.Buffer
		app := App{In: os.Stdin, Out: &out, Err: &stderr, Version: "1.2.3"}
		if err := app.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatal("empty output")
		}
		if args[0] == "--version" && out.String() != "mt 1.2.3\n" {
			t.Fatal(out.String())
		}
	}
}

func TestInvalidLaunchFailsBeforeTmux(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, tc := range []struct {
		args     []string
		contains string
	}{
		{[]string{}, "requires a terminal"},
		{[]string{"not-an-agent", "--no-attach"}, "unknown agent"},
		{[]string{"codex", "0", "--no-attach"}, "positive integer"},
		{[]string{"codex", "-2", "--no-attach"}, "unknown flag"},
		{[]string{"codex", "2", "3"}, "usage:"},
		{[]string{"codex", "--layout", "diagonal"}, "invalid layout"},
		{[]string{"codex", "--cwd", filepath.Join(dir, "missing"), "--no-attach"}, "working directory"},
		{[]string{"ls", "--no-attach"}, "launch flags"},
		{[]string{"ls", "extra"}, "usage:"},
		{[]string{"attach"}, "usage:"},
		{[]string{"kill", "one", "two"}, "usage:"},
	} {
		var out bytes.Buffer
		app := App{In: os.Stdin, Out: &out, Err: &out}
		err := app.Run(context.Background(), tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.contains) {
			t.Fatalf("%v: %v (want %q)", tc.args, err, tc.contains)
		}
	}
}

func TestDetachedLaunchAndManagementThroughExecutable(t *testing.T) {
	// Exercise the actual exec wrapper and CLI together, including paths with
	// spaces. The fake executable exposes only tagged sessions to management.
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("MT_TEST_LOG", log)
	t.Setenv("PATH", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TMUX", "")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$MT_TEST_LOG"
case "$1" in
  -V) printf 'tmux 3.5a\n' ;;
  new-session) printf '$7\t@12\t%%21\n' ;;
  split-window) printf '%%22\n' ;;
  list-sessions) printf '$7\tfeat-x\t1\ttest-agent\t1\t0\t1700000000\n$8\tunrelated\t\t\t1\t0\t1700000000\n' ;;
  list-panes) printf '%%21\n%%22\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("agents:\n  test-agent: {cmd: agent, args: ['literal argument']}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	app := App{In: os.Stdin, Out: &out, Err: &stderr}
	if err := app.Run(context.Background(), []string{"test-agent", "2", "--config", configPath, "--session", "feat-x", "--cwd", dir, "--no-attach"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "feat-x\n" {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := app.Run(context.Background(), []string{"ls"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "feat-x") || strings.Contains(out.String(), "unrelated") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := app.Run(context.Background(), []string{"kill", "all"}); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "kill-session -t $7") || strings.Contains(string(calls), "kill-session -t $8") {
		t.Fatal(string(calls))
	}
}
