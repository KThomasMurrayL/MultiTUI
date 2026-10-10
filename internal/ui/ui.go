// Package ui provides the Bubble Tea launcher and session browser. tmux takes
// over the terminal only after the Bubble Tea program has fully exited.
package ui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"multitui/internal/config"
	"multitui/internal/tmux"
)

type Launch struct {
	Agent, Layout string
	Count         int
}

type launcher struct {
	agents                      []string
	agent, layout, count, focus int
	help, accepted              bool
}

func newLauncher(agents []string, layout string) launcher {
	m := launcher{agents: agents, count: 1}
	for i, value := range config.Layouts {
		if value == layout {
			m.layout = i
		}
	}
	return m
}

func (m launcher) Init() tea.Cmd {
	return nil
}

func (m launcher) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.help {
		switch key.String() {
		case "ctrl+c", "esc", "q":
			return m, tea.Quit
		case "?", "enter":
			m.help = false
		}
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "esc", "q":
		return m, tea.Quit
	case "?":
		m.help = !m.help
	case "enter":
		if m.help {
			m.help = false
		} else if len(m.agents) > 0 {
			m.accepted = true
			return m, tea.Quit
		}
	case "tab", "down", "j":
		m.focus = (m.focus + 1) % 3
	case "shift+tab", "up", "k":
		m.focus = (m.focus + 2) % 3
	case "left", "h", "-":
		m = m.adjust(-1)
	case "right", "l", "+", "=":
		m = m.adjust(1)
	}
	return m, nil
}

func (m launcher) adjust(delta int) launcher {
	switch m.focus {
	case 0:
		if len(m.agents) > 0 {
			m.agent = (m.agent + delta + len(m.agents)) % len(m.agents)
		}
	case 1:
		m.count += delta
		if m.count < 1 {
			m.count = 1
		}
	case 2:
		m.layout = (m.layout + delta + len(config.Layouts)) % len(config.Layouts)
	}
	return m
}

func (m launcher) View() string {
	if m.help {
		return "MultiTUI help\n\nTab or up/down: choose a field\nLeft/right or +/-: change the value\nEnter: launch agents\nEsc or q: cancel\n\nInside tmux (default prefix Ctrl-b):\n  Prefix + arrow: select a pane\n  Prefix + z: zoom the current pane\n  Prefix + d: detach and keep agents running\n\nAll agents share the launch directory.\nPress ? or Enter to return.\n"
	}
	if len(m.agents) == 0 {
		return "No agents configured. Press q to quit.\n"
	}
	rows := []string{fmt.Sprintf("Agent      %s", m.agents[m.agent]), fmt.Sprintf("Instances  %d", m.count), fmt.Sprintf("Layout     %s", config.Layouts[m.layout])}
	var b strings.Builder
	b.WriteString("MultiTUI — launch coding agents\n\n")
	for i, row := range rows {
		if i == m.focus {
			b.WriteString("> ")
		} else {
			b.WriteString("  ")
		}
		b.WriteString(row + "\n")
	}
	if m.count > 8 {
		b.WriteString("\nMany panes can be cramped; use a large terminal.\n")
	}
	b.WriteString("\nTab: field  Left/right: change  Enter: launch  ?: help  q: cancel\n")
	return b.String()
}

func RunLauncher(ctx context.Context, agents []string, layout string, in io.Reader, out io.Writer) (Launch, bool, error) {
	model, err := tea.NewProgram(newLauncher(agents, layout), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	if err != nil {
		return Launch{}, false, err
	}
	m := model.(launcher)
	if !m.accepted {
		return Launch{}, false, nil
	}
	return Launch{Agent: m.agents[m.agent], Count: m.count, Layout: config.Layouts[m.layout]}, true, nil
}

type SessionManager interface {
	List(context.Context) ([]tmux.Session, error)
	KillSession(context.Context, tmux.Session) error
}

type sessionsMsg struct {
	sessions []tmux.Session
	err      error
}
type killedMsg struct {
	err error
}

type manager struct {
	ctx                       context.Context
	client                    SessionManager
	sessions                  []tmux.Session
	cursor                    int
	loading, help, confirming bool
	message                   string
	selected                  string
	height                    int
}

func (m manager) refresh() tea.Cmd {
	return func() tea.Msg {
		sessions, err := m.client.List(m.ctx)
		return sessionsMsg{sessions, err}
	}
}

func (m manager) Init() tea.Cmd {
	return m.refresh()
}

func (m manager) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case sessionsMsg:
		m.loading = false
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
		}
		m.sessions = msg.sessions
		if m.cursor >= len(m.sessions) {
			m.cursor = max(0, len(m.sessions)-1)
		}
		return m, nil
	case killedMsg:
		if msg.err != nil {
			m.message = msg.err.Error()
		} else {
			m.message = "Session killed."
		}
		m.loading = true
		return m, m.refresh()
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.confirming {
			m.confirming = false
			if key == "y" && len(m.sessions) > 0 {
				session := m.sessions[m.cursor]
				m.loading = true
				return m, func() tea.Msg {
					return killedMsg{m.client.KillSession(m.ctx, session)}
				}
			}
			return m, nil
		}
		if m.help {
			if key == "?" || key == "esc" || key == "enter" {
				m.help = false
			}
			if key == "q" {
				return m, tea.Quit
			}
			return m, nil
		}
		switch key {
		case "q", "esc":
			return m, tea.Quit
		case "?":
			m.help = true
		case "r":
			if !m.loading {
				m.loading = true
				m.message = ""
				return m, m.refresh()
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.sessions) {
				m.cursor++
			}
		case "enter":
			if !m.loading && len(m.sessions) > 0 {
				m.selected = m.sessions[m.cursor].Name
				return m, tea.Quit
			}
		case "x":
			if !m.loading && len(m.sessions) > 0 {
				m.confirming = true
			}
		}
	}
	return m, nil
}

func (m manager) View() string {
	if m.help {
		return "MultiTUI sessions help\n\nUp/down: select session\nEnter: attach (switches clients inside tmux)\nx then y: kill selected session and its agents\nr: refresh\nq: quit\n\nTmux default keys: Ctrl-b then d detaches; Ctrl-b then z zooms.\nPress ? or Enter to return.\n"
	}
	var b strings.Builder
	b.WriteString("MultiTUI — managed sessions\n\n")
	if m.loading {
		b.WriteString("Loading...\n")
	}
	if !m.loading && len(m.sessions) == 0 {
		b.WriteString("No managed sessions. Start one with mt <agent> [count].\n")
	}
	height := m.height
	if height == 0 {
		height = 24
	}
	visible := max(1, height-9)
	start := max(0, m.cursor-visible+1)
	end := min(len(m.sessions), start+visible)
	if start > 0 {
		fmt.Fprintf(&b, "  ... %d earlier session(s)\n", start)
	}
	for i := start; i < end; i++ {
		session := m.sessions[i]
		if i == m.cursor {
			b.WriteString("> ")
		} else {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%s  %s  %d panes  %d clients\n", session.Name, session.Agent, session.Panes, session.Attached)
	}
	if end < len(m.sessions) {
		fmt.Fprintf(&b, "  ... %d later session(s)\n", len(m.sessions)-end)
	}
	if m.message != "" {
		b.WriteString("\n" + m.message + "\n")
	}
	if m.confirming {
		fmt.Fprintf(&b, "\nKill %s and all its agents? y to kill, any other key to cancel.\n", m.sessions[m.cursor].Name)
	}
	b.WriteString("\nUp/down: select  Enter: attach  x: kill  r: refresh  ?: help  q: quit\n")
	return b.String()
}

func RunManager(ctx context.Context, client SessionManager, in io.Reader, out io.Writer) (string, error) {
	model, err := tea.NewProgram(manager{ctx: ctx, client: client, loading: true}, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	return model.(manager).selected, nil
}
