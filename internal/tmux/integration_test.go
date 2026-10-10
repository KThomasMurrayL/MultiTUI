package tmux

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"multitui/internal/config"
)

// Run with MT_INTEGRATION=1 go test ./internal/tmux. Each test uses its own
// server socket and an empty tmux config, leaving the user's sessions alone.
type isolatedRunner struct {
	ExecRunner
	socket string
}

func (r isolatedRunner) Output(ctx context.Context, args ...string) (string, error) {
	return r.ExecRunner.Output(ctx, append([]string{"-L", r.socket, "-f", "/dev/null"}, args...)...)
}

func (r isolatedRunner) Interactive(ctx context.Context, args ...string) error {
	return r.ExecRunner.Interactive(ctx, append([]string{"-L", r.socket, "-f", "/dev/null"}, args...)...)
}

func integrationClient(t *testing.T) (*Client, isolatedRunner) {
	t.Helper()
	if os.Getenv("MT_INTEGRATION") != "1" {
		t.Skip("set MT_INTEGRATION=1 to test a real tmux server")
	}
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("MT_INTEGRATION requires tmux on PATH")
	}
	r := isolatedRunner{ExecRunner: ExecRunner{Binary: binary, In: os.Stdin, Out: io.Discard, Err: io.Discard}, socket: fmt.Sprintf("mt-test-%d-%d", os.Getpid(), time.Now().UnixNano())}
	t.Setenv("TMUX", "")
	t.Setenv("SHELL", "/bin/sh")
	c := &Client{Runner: r}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = r.Output(ctx, "kill-server")
	})
	return c, r
}

func TestIntegrationCreateLaunchListAndKill(t *testing.T) {
	c, r := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	outputFile := filepath.Join(dir, "agent output")
	command := "printf '%s\\n' \"$PWD\" 'literal $value' >> " + config.Quote(outputFile)
	for _, count := range []int{1, 4, 12} {
		opts := Options{Name: fmt.Sprintf("custom-%d", count), Agent: "test", Count: count, Cwd: dir, Layout: "tiled", Command: command, Width: 240, Height: 80}
		if _, err := c.Create(ctx, opts); err != nil {
			t.Fatal(err)
		}
	}
	list, err := c.List(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("%+v: %v", list, err)
	}
	for _, session := range list {
		var count int
		if _, err := fmt.Sscanf(session.Name, "custom-%d", &count); err != nil || session.Panes != count {
			t.Fatalf("%+v: %v", session, err)
		}
		output, err := r.Output(ctx, "list-panes", "-s", "-t", session.ID, "-F", "#{pane_current_path}")
		if err != nil {
			t.Fatal(err)
		}
		for _, cwd := range strings.Split(strings.TrimSpace(output), "\n") {
			if cwd != dir {
				t.Fatalf("cwd %q, want %q", cwd, dir)
			}
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(outputFile)
		if strings.Count(string(data), "literal $value\n") == 17 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not all agents launched: %q", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := r.Output(ctx, "new-session", "-d", "-s", "unrelated"); err != nil {
		t.Fatal(err)
	}
	count, err := c.KillAll(ctx)
	if err != nil || count != 3 {
		t.Fatalf("killed %d: %v", count, err)
	}
	if _, err := r.Output(ctx, "has-session", "-t", "=unrelated"); err != nil {
		t.Fatal("kill all touched unrelated session", err)
	}
}

func TestIntegrationSplitFailureAndDuplicatePreserveSessions(t *testing.T) {
	c, r := integrationClient(t)
	ctx := context.Background()
	opts := Options{Name: "too-small", Agent: "test", Count: 12, Cwd: t.TempDir(), Layout: "tiled", Command: "printf test", Width: 4, Height: 4}
	if _, err := c.Create(ctx, opts); err == nil {
		t.Fatal("expected insufficient pane space")
	}
	if _, err := r.Output(ctx, "has-session", "-t", "=too-small"); err == nil {
		t.Fatal("partial session not cleaned up")
	}
	opts.Name, opts.Count, opts.Width, opts.Height = "duplicate", 1, 120, 40
	if _, err := c.Create(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(ctx, opts); err == nil {
		t.Fatal("duplicate session accepted")
	}
	if _, err := r.Output(ctx, "has-session", "-t", "=duplicate"); err != nil {
		t.Fatal("duplicate attempt killed existing session", err)
	}
}

func TestIntegrationKillOnDetach(t *testing.T) {
	c, r := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := Options{Name: "detach-test", Agent: "test", Count: 1, Cwd: t.TempDir(), Layout: "tiled", Command: "printf ready", KillOnDetach: true}
	if _, err := c.Create(ctx, opts); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, r.Binary, "-L", r.socket, "-C", "attach-session", "-t", "=detach-test")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		session, err := c.Find(ctx, "detach-test")
		if err == nil && session.Attached == 1 {
			setting, err := r.Output(ctx, "show-options", "-v", "-t", session.ID, "destroy-unattached")
			if err == nil && strings.TrimSpace(setting) == "on" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("control client did not attach", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := fmt.Fprintln(stdin, "detach-client"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		list, err := c.List(ctx)
		if err == nil && len(list) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("kill_on_detach did not remove session: %+v: %v", list, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
