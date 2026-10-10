// Package cli handles argument parsing separately from terminal UI and tmux.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"

	"multitui/internal/config"
	"multitui/internal/tmux"
	"multitui/internal/ui"
)

const help = `MultiTUI — coding agents tiled in one terminal

Usage:
  mt <agent> [count] [flags]       Launch agents (count defaults to 1)
  mt                             Interactive launcher
  mt ls [--plain]                 Browse managed sessions (table when piped)
  mt attach <session>             Attach or switch to a managed session
  mt kill <session|all>           Destroy managed sessions and their agents

Flags (may appear before or after positionals):
  --layout <layout>               tiled, even-horizontal, even-vertical, main-vertical
  --session <name>                Custom session name (default mt-<agent>-<timestamp>)
  --cwd <dir>                     Shared working directory (default current directory)
  --no-attach                     Create detached; print only the session name
  --config <path>                 Config file (default ~/.config/mt/config.yaml)
  --plain                         Print mt ls as a table instead of opening the UI
  --version                      Print version
  -h, --help                      Show this help

Examples:
  mt claude 4
  mt codex 2 --layout even-horizontal --session feat-x
  mt opencode --cwd ~/project --no-attach
  mt attach feat-x

In tmux, Ctrl-b then arrow selects a pane, z zooms, d detaches (default keys).
Detaching keeps agents alive unless defaults.kill_on_detach is configured.
`

type Args struct {
	Positionals                    []string
	Layout, Session, Cwd, Config   string
	NoAttach, Plain, Version, Help bool
}

// Parse accepts interspersed flags using the stdlib parser, with an explicit
// option vocabulary so unknown flags and missing values cannot become agents.
func Parse(args []string) (Args, error) {
	var result Args
	fs := flag.NewFlagSet("mt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&result.Layout, "layout", "", "")
	fs.StringVar(&result.Session, "session", "", "")
	fs.StringVar(&result.Cwd, "cwd", "", "")
	fs.StringVar(&result.Config, "config", "", "")
	fs.BoolVar(&result.NoAttach, "no-attach", false, "")
	fs.BoolVar(&result.Plain, "plain", false, "")
	fs.BoolVar(&result.Version, "version", false, "")
	fs.BoolVar(&result.Help, "help", false, "")
	fs.BoolVar(&result.Help, "h", false, "")
	var flags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			result.Positionals = append(result.Positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			result.Positionals = append(result.Positionals, arg)
			continue
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		f := fs.Lookup(name)
		if f == nil {
			return Args{}, fmt.Errorf("unknown flag %s", arg)
		}
		flags = append(flags, arg)
		if boolFlag, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !boolFlag.IsBoolFlag() {
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
					return Args{}, fmt.Errorf("flag --%s requires a value", name)
				}
				i++
				flags = append(flags, args[i])
			}
		}
	}
	if err := fs.Parse(flags); err != nil {
		return Args{}, err
	}
	return result, nil
}

type App struct {
	In       *os.File
	Out, Err io.Writer
	Version  string
}

func (a App) interactive() bool {
	out, ok := a.Out.(*os.File)
	return ok && a.In != nil && term.IsTerminal(a.In.Fd()) && term.IsTerminal(out.Fd())
}

func (a App) client(ctx context.Context) (*tmux.Client, error) {
	c, err := tmux.New(a.In, a.Out, a.Err)
	if err != nil {
		return nil, err
	}
	if err := c.Check(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (a App) Run(ctx context.Context, argv []string) error {
	args, err := Parse(argv)
	if err != nil {
		return fmt.Errorf("%w; run `mt --help`", err)
	}
	if args.Help || (len(args.Positionals) == 1 && args.Positionals[0] == "help") {
		_, err := fmt.Fprint(a.Out, help)
		return err
	}
	if args.Version {
		_, err := fmt.Fprintf(a.Out, "mt %s\n", a.Version)
		return err
	}
	if len(args.Positionals) > 0 {
		switch args.Positionals[0] {
		case "ls", "attach", "kill":
			return a.manage(ctx, args)
		}
	}
	if args.Plain {
		return errors.New("--plain is only valid with `mt ls`")
	}
	if len(args.Positionals) > 2 {
		return errors.New("usage: mt <agent> [count] [flags]")
	}
	path := args.Config
	if path == "" {
		path, err = config.Path()
		if err != nil {
			return err
		}
	}
	cfg, err := config.Load(path, args.Config != "")
	if err != nil {
		return err
	}
	layout := args.Layout
	if layout == "" {
		layout = cfg.Defaults.Layout
	}
	if !config.ValidLayout(layout) {
		return fmt.Errorf("invalid layout %q; choose %s", layout, strings.Join(config.Layouts, ", "))
	}
	count := 1
	agentName := ""
	if len(args.Positionals) == 0 {
		if !a.interactive() {
			return errors.New("interactive launcher requires a terminal; use `mt <agent> [count] --no-attach` or `mt --help`")
		}
		launch, accepted, err := ui.RunLauncher(ctx, cfg.Names(), layout, a.In, a.Out)
		if err != nil {
			return err
		}
		if !accepted {
			return nil
		}
		agentName, count, layout = launch.Agent, launch.Count, launch.Layout
	} else {
		agentName = args.Positionals[0]
		if len(args.Positionals) == 2 {
			count, err = strconv.Atoi(args.Positionals[1])
			if err != nil || count < 1 {
				return fmt.Errorf("instance count must be a positive integer (got %q)", args.Positionals[1])
			}
		}
	}
	agent, ok := cfg.Agents[agentName]
	if !ok {
		return fmt.Errorf("unknown agent %q; configured agents: %s", agentName, strings.Join(cfg.Names(), ", "))
	}
	cwd := args.Cwd
	if cwd == "" {
		cwd = "."
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory %q is not a directory", cwd)
	}
	command, err := agent.Command(cwd)
	if err != nil {
		return err
	}
	name := args.Session
	if name == "" {
		name = "mt-" + agentName + "-" + time.Now().UTC().Format("20060102-150405.000000000")
		name = strings.ReplaceAll(name, ".", "-")
	}
	if !config.ValidName(name) || name == "all" {
		return errors.New("session name must start with a letter or digit and contain only letters, digits, underscores or hyphens; 'all' is reserved")
	}
	if !args.NoAttach && !tmux.Inside() && !a.interactive() {
		return errors.New("attaching requires a terminal; use --no-attach to create a detached session")
	}
	c, err := a.client(ctx)
	if err != nil {
		return err
	}
	if count > 8 {
		fmt.Fprintln(a.Err, "mt: warning: more than 8 panes can be cramped; enlarge the terminal or zoom with Ctrl-b then z")
	}
	width, height := 120, 40
	if a.interactive() {
		if w, h, err := term.GetSize(a.In.Fd()); err == nil && w > 0 && h > 1 {
			width, height = w, h-1
		}
	}
	name, err = c.Create(ctx, tmux.Options{Name: name, Agent: agentName, Count: count, Layout: layout, Cwd: cwd, Command: command, Width: width, Height: height, KillOnDetach: cfg.Defaults.KillOnDetach})
	if err != nil {
		return err
	}
	if args.NoAttach {
		_, err := fmt.Fprintln(a.Out, name)
		return err
	}
	if err := c.Attach(ctx, name, tmux.Inside()); err != nil {
		return fmt.Errorf("session %s was created but attach failed; retry with `mt attach %s`: %w", name, name, err)
	}
	return nil
}

func (a App) manage(ctx context.Context, args Args) error {
	command := args.Positionals[0]
	if args.Layout != "" || args.Session != "" || args.Cwd != "" || args.NoAttach || args.Config != "" {
		return fmt.Errorf("launch flags are not valid with `mt %s`", command)
	}
	if args.Plain && command != "ls" {
		return errors.New("--plain is only valid with `mt ls`")
	}
	if command == "ls" && len(args.Positionals) != 1 {
		return errors.New("usage: mt ls [--plain]")
	}
	if command != "ls" && len(args.Positionals) != 2 {
		return fmt.Errorf("usage: mt %s <session%s>", command, map[string]string{"kill": "|all"}[command])
	}
	if command == "attach" && !tmux.Inside() && !a.interactive() {
		return errors.New("attaching requires a terminal")
	}
	c, err := a.client(ctx)
	if err != nil {
		return err
	}
	switch command {
	case "ls":
		if a.interactive() && !args.Plain {
			name, err := ui.RunManager(ctx, c, a.In, a.Out)
			if err != nil || name == "" {
				return err
			}
			return c.Attach(ctx, name, tmux.Inside())
		}
		sessions, err := c.List(ctx)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			_, err := fmt.Fprintln(a.Out, "No managed sessions.")
			return err
		}
		w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SESSION\tAGENT\tPANES\tCLIENTS\tCREATED")
		for _, session := range sessions {
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", session.Name, session.Agent, session.Panes, session.Attached, session.Created.Local().Format("2006-01-02 15:04"))
		}
		return w.Flush()
	case "attach":
		return c.Attach(ctx, args.Positionals[1], tmux.Inside())
	case "kill":
		name := args.Positionals[1]
		if name == "all" {
			count, err := c.KillAll(ctx)
			fmt.Fprintf(a.Out, "Killed %d managed session(s).\n", count)
			return err
		}
		if err := c.Kill(ctx, name); err != nil {
			return err
		}
		_, err := fmt.Fprintf(a.Out, "Killed %s.\n", name)
		return err
	}
	return nil
}
