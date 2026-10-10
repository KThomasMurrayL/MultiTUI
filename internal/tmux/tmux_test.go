package tmux

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls                       [][]string
	interactive                 [][]string
	fail                        string
	version, listing, listError string
	panes                       int
}

func (f *fakeRunner) Output(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if args[0] == f.fail {
		return "failure", errors.New("injected failure")
	}
	switch args[0] {
	case "-V":
		return f.version, nil
	case "new-session":
		return "$7\t@12\t%21\n", nil
	case "split-window":
		f.panes++
		return fmt.Sprintf("%%%d\n", 21+f.panes), nil
	case "list-sessions":
		if f.listError != "" {
			return f.listError, errors.New(f.listError)
		}
		return f.listing, nil
	case "list-panes":
		return "%21\n%22\n", nil
	}
	return "", nil
}

func (f *fakeRunner) Interactive(_ context.Context, args ...string) error {
	f.interactive = append(f.interactive, append([]string(nil), args...))
	return nil
}

func options(count int) Options {
	return Options{Name: "feat-x", Agent: "codex", Count: count, Cwd: "/tmp/a project", Layout: "main-vertical", Command: "/usr/bin/env '/agent' 'Enter'", Width: 180, Height: 50}
}

func callsNamed(f *fakeRunner, name string) [][]string {
	var result [][]string
	for _, call := range f.calls {
		if call[0] == name {
			result = append(result, call)
		}
	}
	return result
}

func TestCreateTargetsIDsAndLaunchesEveryPane(t *testing.T) {
	f := &fakeRunner{}
	c := Client{Runner: f}
	name, err := c.Create(context.Background(), options(4))
	if err != nil || name != "feat-x" {
		t.Fatalf("%q: %v", name, err)
	}
	if len(callsNamed(f, "split-window")) != 3 {
		t.Fatal(f.calls)
	}
	layouts := callsNamed(f, "select-layout")
	if len(layouts) != 4 || !reflect.DeepEqual(layouts[3], []string{"select-layout", "-t", "@12", "main-vertical"}) {
		t.Fatal(layouts)
	}
	sends := callsNamed(f, "send-keys")
	if len(sends) != 8 {
		t.Fatal(sends)
	}
	for i := 0; i < 4; i++ {
		pane := fmt.Sprintf("%%%d", 21+i)
		if !reflect.DeepEqual(sends[2*i], []string{"send-keys", "-t", pane, "-l", "--", options(4).Command}) {
			t.Fatal(sends[2*i])
		}
		if !reflect.DeepEqual(sends[2*i+1], []string{"send-keys", "-t", pane, "Enter"}) {
			t.Fatal(sends[2*i+1])
		}
	}
	for _, call := range callsNamed(f, "split-window") {
		if !strings.Contains(strings.Join(call, "|"), "/tmp/a project") {
			t.Fatal("cwd not shared", call)
		}
	}
	if len(callsNamed(f, "kill-session")) != 0 {
		t.Fatal("successful session was killed")
	}
}

func TestOnePaneAndKillOnDetach(t *testing.T) {
	f := &fakeRunner{}
	opts := options(1)
	opts.KillOnDetach = true
	if _, err := (&Client{Runner: f}).Create(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if len(callsNamed(f, "split-window")) != 0 {
		t.Fatal("single instance split")
	}
	hooks := callsNamed(f, "set-hook")
	if len(hooks) != 1 || hooks[0][3] != "client-session-changed[999]" || hooks[0][4] != "set-option -t '$7' destroy-unattached on" {
		t.Fatal(hooks)
	}
}

func TestFailureCleansUpOnlyNewSession(t *testing.T) {
	for _, command := range []string{"set-option", "split-window", "select-layout", "send-keys", "select-pane", "set-hook"} {
		t.Run(command, func(t *testing.T) {
			f := &fakeRunner{fail: command}
			opts := options(3)
			opts.KillOnDetach = true
			if _, err := (&Client{Runner: f}).Create(context.Background(), opts); err == nil {
				t.Fatal("failure not reported")
			}
			kills := callsNamed(f, "kill-session")
			if len(kills) != 1 || !reflect.DeepEqual(kills[0], []string{"kill-session", "-t", "$7"}) {
				t.Fatal(kills)
			}
		})
	}
	f := &fakeRunner{fail: "new-session"}
	if _, err := (&Client{Runner: f}).Create(context.Background(), options(3)); err == nil {
		t.Fatal("new-session failure not reported")
	}
	if len(callsNamed(f, "kill-session")) != 0 {
		t.Fatal("failed creation must not kill a preexisting session")
	}
}

func TestInvalidOptionsDoNotCreate(t *testing.T) {
	for _, edit := range []func(*Options){
		func(o *Options) {
			o.Count = 0
		}, func(o *Options) {
			o.Name = "bad:session"
		},
		func(o *Options) {
			o.Name = "all"
		}, func(o *Options) {
			o.Layout = "bad"
		},
		func(o *Options) {
			o.Command = "bad\ncommand"
		},
	} {
		f := &fakeRunner{}
		opts := options(2)
		edit(&opts)
		if _, err := (&Client{Runner: f}).Create(context.Background(), opts); err == nil || len(f.calls) != 0 {
			t.Fatalf("invalid option reached tmux: %+v", opts)
		}
	}
}

func TestVersionCheck(t *testing.T) {
	for _, tc := range []struct {
		version string
		valid   bool
	}{{"tmux 3.0\n", true}, {"tmux 3.5a", true}, {"tmux 3.4-next", true}, {"tmux 4.0", true}, {"tmux 2.9", false}, {"invalid", false}} {
		err := (&Client{Runner: &fakeRunner{version: tc.version}}).Check(context.Background())
		if (err == nil) != tc.valid {
			t.Fatalf("%q: %v", tc.version, err)
		}
	}
}

const sessions = "$3\tmt-personal\t\t\t1\t0\t1700000000\n$7\tfeat-x\t1\tcodex\t1\t0\t1700000001\n$8\tmt-claude-123\t1\tclaude\t1\t1\t1700000002\n"

func TestListAndKillAllOnlyManageTaggedSessions(t *testing.T) {
	f := &fakeRunner{listing: sessions}
	c := &Client{Runner: f}
	list, err := c.List(context.Background())
	if err != nil || len(list) != 2 || list[0].Name != "feat-x" || list[0].Panes != 2 {
		t.Fatalf("%+v: %v", list, err)
	}
	count, err := c.KillAll(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("%d: %v", count, err)
	}
	kills := callsNamed(f, "kill-session")
	if !reflect.DeepEqual(kills, [][]string{{"kill-session", "-t", "$7"}, {"kill-session", "-t", "$8"}}) {
		t.Fatal(kills)
	}
	if err := c.Kill(context.Background(), "mt-personal"); err == nil {
		t.Fatal("unmanaged session was killed")
	}
	if err := c.Kill(context.Background(), "feat"); err == nil {
		t.Fatal("partial session name was accepted")
	}
}

func TestAttachSwitchesInsideTmux(t *testing.T) {
	for _, inside := range []bool{true, false} {
		f := &fakeRunner{listing: sessions}
		if err := (&Client{Runner: f}).Attach(context.Background(), "feat-x", inside); err != nil {
			t.Fatal(err)
		}
		if inside {
			if len(f.interactive) != 0 || !reflect.DeepEqual(callsNamed(f, "switch-client"), [][]string{{"switch-client", "-t", "$7"}}) {
				t.Fatal(f.calls, f.interactive)
			}
		} else if !reflect.DeepEqual(f.interactive, [][]string{{"attach-session", "-t", "$7"}}) {
			t.Fatal(f.interactive)
		}
	}
}

func TestListEmptyServerAndRealErrors(t *testing.T) {
	for _, text := range []string{"no server running on /tmp/tmux/default", "no sessions", "error connecting to /tmp/tmux/default (No such file or directory)"} {
		list, err := (&Client{Runner: &fakeRunner{listError: text}}).List(context.Background())
		if err != nil || len(list) != 0 {
			t.Fatalf("%q: %+v %v", text, list, err)
		}
	}
	if _, err := (&Client{Runner: &fakeRunner{listError: "error connecting to socket (Permission denied)"}}).List(context.Background()); err == nil {
		t.Fatal("permission error hidden")
	}
}
