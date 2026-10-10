// Package tmux owns session creation and pane orchestration. All calls use argv,
// never an interpolated shell command, and target stable session/window/pane IDs.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"multitui/internal/config"
)

type Runner interface {
	Output(context.Context, ...string) (string, error)
	Interactive(context.Context, ...string) error
}

type ExecRunner struct {
	Binary string
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
}

func (r ExecRunner) Output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return string(data), fmt.Errorf("tmux %s: %w: %s", args[0], err, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}

func (r ExecRunner) Interactive(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r.In, r.Out, r.Err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return nil
}

type Client struct {
	Runner Runner
}

func New(in io.Reader, out, stderr io.Writer) (*Client, error) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		return nil, errors.New("tmux is required (version 3 or newer); install with `brew install tmux` on macOS or your Linux package manager")
	}
	return &Client{Runner: ExecRunner{Binary: binary, In: in, Out: out, Err: stderr}}, nil
}

var versionPattern = regexp.MustCompile(`^tmux (\d+)(?:\.|$)`)

func (c *Client) Check(ctx context.Context) error {
	output, err := c.Runner.Output(ctx, "-V")
	if err != nil {
		return err
	}
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if len(match) < 2 {
		return fmt.Errorf("cannot parse tmux version %q; version 3 or newer is required", strings.TrimSpace(output))
	}
	major, err := strconv.Atoi(match[1])
	if err != nil || major < 3 {
		return fmt.Errorf("tmux 3 or newer is required (found %s)", strings.TrimSpace(output))
	}
	return nil
}

type Options struct {
	Name, Agent, Cwd, Layout, Command string
	Count, Width, Height              int
	KillOnDetach                      bool
}

type Session struct {
	ID, Name, Agent          string
	Windows, Panes, Attached int
	Created                  time.Time
}

var sessionID = regexp.MustCompile(`^\$[0-9]+$`)
var windowID = regexp.MustCompile(`^@[0-9]+$`)
var paneID = regexp.MustCompile(`^%[0-9]+$`)

func (c *Client) Create(ctx context.Context, opts Options) (_ string, err error) {
	if !config.ValidName(opts.Name) || opts.Name == "all" {
		return "", errors.New("session name must start with a letter or digit and contain only letters, digits, underscores or hyphens; 'all' is reserved")
	}
	if !config.ValidName(opts.Agent) {
		return "", errors.New("invalid agent name")
	}
	if opts.Count < 1 {
		return "", errors.New("instance count must be positive")
	}
	if !config.ValidLayout(opts.Layout) {
		return "", fmt.Errorf("unsupported layout %q", opts.Layout)
	}
	if opts.Command == "" || strings.ContainsAny(opts.Command, "\r\n\x00") {
		return "", errors.New("invalid pane command")
	}
	if opts.Width <= 0 {
		opts.Width = 120
	}
	if opts.Height <= 0 {
		opts.Height = 40
	}
	shell := os.Getenv("SHELL")
	if !filepath.IsAbs(shell) {
		shell = "/bin/sh"
	}
	// Supplying a shell explicitly prevents inherited default-command values
	// from launching another program before we can send the agent command.
	shellCommand := "exec " + config.Quote(shell)
	output, err := c.Runner.Output(ctx, "new-session", "-d", "-P", "-F", "#{session_id}\t#{window_id}\t#{pane_id}", "-s", opts.Name, "-n", "agents", "-c", opts.Cwd, "-x", strconv.Itoa(opts.Width), "-y", strconv.Itoa(opts.Height), shellCommand)
	if err != nil {
		return "", err
	}
	ids := strings.Split(strings.TrimSpace(output), "\t")
	if len(ids) != 3 || !sessionID.MatchString(ids[0]) || !windowID.MatchString(ids[1]) || !paneID.MatchString(ids[2]) {
		// A successful new-session owns this exact name, even if its response was malformed.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, cleanupErr := c.Runner.Output(cleanupCtx, "kill-session", "-t", "="+opts.Name)
		return "", errors.Join(fmt.Errorf("unexpected new-session response %q", output), cleanupErr)
	}
	sid, wid := ids[0], ids[1]
	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, cleanupErr := c.Runner.Output(cleanupCtx, "kill-session", "-t", sid); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup session %s: %w", opts.Name, cleanupErr))
		}
	}()
	for _, option := range [][2]string{{"@mt-managed", "1"}, {"@mt-agent", opts.Agent}, {"destroy-unattached", "off"}} {
		if _, err = c.Runner.Output(ctx, "set-option", "-t", sid, option[0], option[1]); err != nil {
			return "", err
		}
	}
	panes := []string{ids[2]}
	for len(panes) < opts.Count {
		// Always rebalance between splits: repeatedly splitting one tiny pane
		// fails long before the terminal actually runs out of room.
		output, err = c.Runner.Output(ctx, "split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "-t", panes[0], "-c", opts.Cwd, shellCommand)
		if err != nil {
			return "", fmt.Errorf("create pane %d of %d (enlarge the terminal or reduce count): %w", len(panes)+1, opts.Count, err)
		}
		id := strings.TrimSpace(output)
		if !paneID.MatchString(id) {
			return "", fmt.Errorf("unexpected split-window response %q", output)
		}
		panes = append(panes, id)
		if _, err = c.Runner.Output(ctx, "select-layout", "-t", wid, "tiled"); err != nil {
			return "", err
		}
	}
	if _, err = c.Runner.Output(ctx, "select-layout", "-t", wid, opts.Layout); err != nil {
		return "", err
	}
	if opts.KillOnDetach {
		// Enable tmux's native last-client cleanup only after the first attach.
		// This preserves --no-attach sessions and avoids racing detach hooks
		// against tmux updating session_attached. Native cleanup also handles
		// terminal disconnects and switches away from the session.
		hook := "set-option -t " + config.Quote(sid) + " destroy-unattached on"
		if _, err = c.Runner.Output(ctx, "set-hook", "-t", sid, "client-session-changed[999]", hook); err != nil {
			return "", err
		}
	}
	for _, pane := range panes {
		if _, err = c.Runner.Output(ctx, "send-keys", "-t", pane, "-l", "--", opts.Command); err != nil {
			return "", err
		}
		if _, err = c.Runner.Output(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
			return "", err
		}
	}
	if _, err = c.Runner.Output(ctx, "select-pane", "-t", panes[0]); err != nil {
		return "", err
	}
	return opts.Name, nil
}

func (c *Client) List(ctx context.Context) ([]Session, error) {
	format := "#{session_id}\t#{session_name}\t#{@mt-managed}\t#{@mt-agent}\t#{session_windows}\t#{session_attached}\t#{session_created}"
	output, err := c.Runner.Output(ctx, "list-sessions", "-F", format)
	if err != nil {
		// A server with no sessions exits nonzero. Permission errors and broken
		// sockets must still be reported, rather than disguised as an empty list.
		lower := strings.ToLower(output)
		if strings.Contains(lower, "no server running") || strings.Contains(lower, "no sessions") || (strings.Contains(lower, "error connecting to") && strings.Contains(lower, "no such file or directory")) {
			return []Session{}, nil
		}
		return nil, err
	}
	sessions := []Session{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			return nil, fmt.Errorf("unexpected list-sessions response %q", line)
		}
		if fields[2] != "1" {
			continue
		}
		if !sessionID.MatchString(fields[0]) {
			return nil, fmt.Errorf("invalid session ID %q", fields[0])
		}
		windows, e1 := strconv.Atoi(fields[4])
		attached, e2 := strconv.Atoi(fields[5])
		created, e3 := strconv.ParseInt(fields[6], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			return nil, fmt.Errorf("invalid session metadata %q", line)
		}
		panes, paneErr := c.Runner.Output(ctx, "list-panes", "-s", "-t", fields[0], "-F", "#{pane_id}")
		if paneErr != nil {
			return nil, paneErr
		}
		count := len(strings.Fields(panes))
		sessions = append(sessions, Session{ID: fields[0], Name: fields[1], Agent: fields[3], Windows: windows, Panes: count, Attached: attached, Created: time.Unix(created, 0)})
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Name < sessions[j].Name
	})
	return sessions, nil
}

func (c *Client) Find(ctx context.Context, name string) (Session, error) {
	sessions, err := c.List(ctx)
	if err != nil {
		return Session{}, err
	}
	for _, session := range sessions {
		if session.Name == name {
			return session, nil
		}
	}
	return Session{}, fmt.Errorf("managed session %q not found; run `mt ls`", name)
}

func (c *Client) Attach(ctx context.Context, name string, inside bool) error {
	session, err := c.Find(ctx, name)
	if err != nil {
		return err
	}
	if inside {
		_, err = c.Runner.Output(ctx, "switch-client", "-t", session.ID)
		return err
	}
	return c.Runner.Interactive(ctx, "attach-session", "-t", session.ID)
}

func (c *Client) Kill(ctx context.Context, name string) error {
	session, err := c.Find(ctx, name)
	if err != nil {
		return err
	}
	return c.KillSession(ctx, session)
}

func (c *Client) KillSession(ctx context.Context, session Session) error {
	if !sessionID.MatchString(session.ID) {
		return errors.New("invalid session ID")
	}
	_, err := c.Runner.Output(ctx, "kill-session", "-t", session.ID)
	return err
}

func (c *Client) KillAll(ctx context.Context) (int, error) {
	sessions, err := c.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	var failures []error
	for _, session := range sessions {
		if err := c.KillSession(ctx, session); err != nil {
			failures = append(failures, err)
		} else {
			count++
		}
	}
	return count, errors.Join(failures...)
}

func Inside() bool {
	return os.Getenv("TMUX") != ""
}
