package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"multitui/internal/tmux"
)

func key(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func TestLauncherSelectionAndCancellation(t *testing.T) {
	m := newLauncher([]string{"claude", "codex"}, "main-vertical")
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = model.(launcher)
	if m.agent != 1 {
		t.Fatal(m)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = model.(launcher)
	model, _ = m.Update(key("+"))
	m = model.(launcher)
	if m.count != 2 || m.layout != 3 {
		t.Fatal(m)
	}
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(launcher)
	if !m.accepted || cmd == nil {
		t.Fatal("launch was not accepted")
	}
	m = newLauncher([]string{"claude"}, "tiled")
	model, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if model.(launcher).accepted || cmd == nil {
		t.Fatal("cancel failed")
	}
	m.focus = 1
	m = m.adjust(-1)
	if m.count != 1 {
		t.Fatal("count below one")
	}
}

type fakeManager struct {
	killed string
}

func (f *fakeManager) List(context.Context) ([]tmux.Session, error) {
	return []tmux.Session{{ID: "$1", Name: "one"}}, nil
}
func (f *fakeManager) KillSession(_ context.Context, session tmux.Session) error {
	f.killed = session.Name
	return nil
}

func TestManagerKillRequiresExplicitKeyAndRefreshes(t *testing.T) {
	f := &fakeManager{}
	m := manager{ctx: context.Background(), client: f, sessions: []tmux.Session{{ID: "$1", Name: "one"}}}
	model, _ := m.Update(key("x"))
	m = model.(manager)
	if !m.confirming {
		t.Fatal("no kill confirmation")
	}
	model, cmd := m.Update(key("n"))
	m = model.(manager)
	if m.confirming || cmd != nil || f.killed != "" {
		t.Fatal("cancelled kill ran")
	}
	model, _ = m.Update(key("x"))
	m = model.(manager)
	model, cmd = m.Update(key("y"))
	m = model.(manager)
	if cmd == nil || !m.loading {
		t.Fatal("kill was not scheduled")
	}
	msg := cmd()
	if f.killed != "one" {
		t.Fatal("wrong session killed")
	}
	model, cmd = m.Update(msg)
	if cmd == nil || !model.(manager).loading {
		t.Fatal("kill did not refresh")
	}
	model, _ = model.Update(sessionsMsg{sessions: []tmux.Session{}})
	if model.(manager).cursor != 0 || model.(manager).loading {
		t.Fatal(model)
	}
}

func TestManagerAttachAndError(t *testing.T) {
	m := manager{sessions: []tmux.Session{{Name: "one"}}}
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if model.(manager).selected != "one" || cmd == nil {
		t.Fatal("attach failed")
	}
	model, _ = m.Update(sessionsMsg{err: errors.New("broken socket")})
	if !strings.Contains(model.View(), "broken socket") {
		t.Fatal("load error hidden")
	}
}

func TestManagerKeepsSelectionVisible(t *testing.T) {
	m := manager{height: 12, cursor: 15}
	for i := 0; i < 20; i++ {
		m.sessions = append(m.sessions, tmux.Session{Name: fmt.Sprintf("session-%02d", i)})
	}
	view := m.View()
	if !strings.Contains(view, "> session-15") || strings.Contains(view, "session-00") {
		t.Fatalf("selected session not in viewport: %s", view)
	}
}
