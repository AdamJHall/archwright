// Package tui provides the scrollable-viewport terminal UI for archwright.
//
// In TUI mode bubbletea owns the alt-screen and the render loop, so subprocess
// output can no longer go straight to os.Stdout — it is captured via a teaWriter
// pump (an io.Writer set as the Runner's Out) and forwarded into the model as
// outputMsg messages, then rendered into a scrollable viewport. Plain mode does
// not use this package at all; it remains the unchanged os.Stderr code path.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Message types pumped into the model.
type (
	// outputMsg carries a chunk of subprocess (or ui) output. One message per
	// Write; the model keeps a full scrollback buffer.
	outputMsg string
	// stageMsg updates the header banner when a new stage starts.
	stageMsg struct {
		order int
		name  string
	}
	// doneMsg signals the phase finished (err nil on success).
	doneMsg struct{ err error }
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("63")). // soft purple, matches ui.Header
			Padding(0, 1)
	footerStyle = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
)

// model is a viewport plus a header/footer. Now that output never goes to
// os.Stdout while the TUI owns the screen, a viewport is safe (the constraint
// CLAUDE.md used to forbid).
type model struct {
	vp     viewport.Model
	buf    *strings.Builder // full scrollback; pointer so the Elm value-copy of
	stage  string           // the model never copies a non-zero strings.Builder
	done   bool
	err    error
	follow bool // auto-scroll to bottom unless the user scrolled up
	ready  bool // viewport sized by the first WindowSizeMsg
	width  int
	height int
}

// newModel returns the initial model. follow defaults to true (autoscroll on).
func newModel() model { return model{follow: true, buf: &strings.Builder{}} }

func (m model) Init() tea.Cmd { return nil }

// headerView renders the current stage banner (or a default while idle).
func (m model) headerView() string {
	s := m.stage
	if s == "" {
		s = "archwright"
	}
	return headerStyle.Render(s)
}

// footerView renders scroll position + keybind hints, or a final status line.
func (m model) footerView() string {
	if m.done {
		if m.err != nil {
			return errStyle.Render("✗ " + m.err.Error())
		}
		return okStyle.Render("✓ done — press q to quit")
	}
	follow := "follow"
	if !m.follow {
		follow = "paused"
	}
	return footerStyle.Render(fmt.Sprintf(
		"%3.0f%%  [%s]  ↑/↓ scroll · G/End follow · q quit",
		m.vp.ScrollPercent()*100, follow))
}

// chromeHeight is the number of rows the header+footer consume.
const chromeHeight = 2

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		h := msg.Height - chromeHeight
		if h < 1 {
			h = 1
		}
		if !m.ready {
			m.vp = viewport.New(msg.Width, h)
			m.vp.SetContent(m.buf.String())
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = h
		}
		if m.follow {
			m.vp.GotoBottom()
		}
		return m, nil

	case outputMsg:
		m.buf.WriteString(string(msg))
		if m.ready {
			m.vp.SetContent(m.buf.String())
			if m.follow {
				m.vp.GotoBottom()
			}
		}
		return m, nil

	case stageMsg:
		m.stage = fmt.Sprintf("%02d · %s", msg.order, msg.name)
		return m, nil

	case doneMsg:
		m.done = true
		m.err = msg.err
		if m.follow && m.ready {
			m.vp.GotoBottom()
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "G", "end":
			m.follow = true
			if m.ready {
				m.vp.GotoBottom()
			}
			return m, nil
		case "up", "k", "pgup", "b", "home", "g":
			// Any explicit upward navigation pauses follow.
			m.follow = false
		}
	}

	// Delegate remaining messages (scrolling, mouse) to the viewport.
	if m.ready {
		m.vp, cmd = m.vp.Update(msg)
	}
	return m, cmd
}

func (m model) View() string {
	if !m.ready {
		return "initializing…"
	}
	return strings.Join([]string{
		m.headerView(),
		m.vp.View(),
		m.footerView(),
	}, "\n")
}
