package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// step applies one message to the model and returns the concrete model back.
func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.model", next)
	}
	return got
}

// sized returns a ready model with a viewport of the given size.
func sized(t *testing.T, w, h int) model {
	t.Helper()
	return step(t, newModel(), tea.WindowSizeMsg{Width: w, Height: h})
}

func TestUpdateOutputAppends(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"single chunk", []string{"hello\n"}, "hello\n"},
		{"multiple chunks", []string{"a\n", "b\n", "c\n"}, "a\nb\nc\n"},
		{"partial line then rest", []string{"foo", "bar\n"}, "foobar\n"},
		{"empty chunk", []string{""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := sized(t, 80, 24)
			for _, c := range tt.chunks {
				m = step(t, m, outputMsg(c))
			}
			if got := m.buf.String(); got != tt.want {
				t.Errorf("buf = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestOutputBeforeReadyBuffers(t *testing.T) {
	// Output that arrives before the first WindowSizeMsg must still be retained
	// and shown once the viewport becomes ready.
	m := step(t, newModel(), outputMsg("early\n"))
	if m.ready {
		t.Fatal("model should not be ready before WindowSizeMsg")
	}
	if got := m.buf.String(); got != "early\n" {
		t.Fatalf("buf = %q; want early buffered", got)
	}
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if !m.ready {
		t.Fatal("model should be ready after WindowSizeMsg")
	}
	if !strings.Contains(m.vp.View(), "early") {
		t.Errorf("viewport view missing buffered output: %q", m.vp.View())
	}
}

func TestFollowToggle(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		startFllw  bool
		wantFollow bool
	}{
		{"up pauses follow", "up", true, false},
		{"k pauses follow", "k", true, false},
		{"pgup pauses follow", "pgup", true, false},
		{"G re-enables follow", "G", false, true},
		{"end re-enables follow", "end", false, true},
		{"home pauses follow", "home", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := sized(t, 80, 24)
			m.follow = tt.startFllw
			m = step(t, m, keyMsg(tt.key))
			if m.follow != tt.wantFollow {
				t.Errorf("follow = %v; want %v", m.follow, tt.wantFollow)
			}
		})
	}
}

func TestAutoscrollOnlyWhenFollowing(t *testing.T) {
	// Fill far past one screen so scrolling is meaningful.
	m := sized(t, 20, 5)
	for i := 0; i < 100; i++ {
		m = step(t, m, outputMsg("line\n"))
	}
	if !m.vp.AtBottom() {
		t.Fatal("following model should auto-scroll to bottom")
	}
	// Pause follow, scroll to top, then append: must NOT jump to bottom.
	m.follow = false
	m.vp.GotoTop()
	atTop := m.vp.AtTop()
	m = step(t, m, outputMsg("more\n"))
	if !atTop {
		t.Fatal("precondition: expected viewport at top after GotoTop")
	}
	if m.vp.AtBottom() {
		t.Error("paused model jumped to bottom on new output")
	}
}

func TestWindowSizeResize(t *testing.T) {
	tests := []struct {
		name    string
		w, h    int
		wantW   int
		wantVPH int
	}{
		{"normal", 100, 40, 100, 38},
		{"tiny height clamps to 1", 80, 1, 80, 1},
		{"zero height clamps to 1", 80, 0, 80, 1},
		{"wide", 200, 50, 200, 48},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := sized(t, tt.w, tt.h)
			if m.vp.Width != tt.wantW {
				t.Errorf("vp.Width = %d; want %d", m.vp.Width, tt.wantW)
			}
			if m.vp.Height != tt.wantVPH {
				t.Errorf("vp.Height = %d; want %d", m.vp.Height, tt.wantVPH)
			}
		})
	}
}

func TestResizeAfterReady(t *testing.T) {
	m := sized(t, 80, 24)
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if m.vp.Width != 120 || m.vp.Height != 28 {
		t.Errorf("resize gave %dx%d; want 120x28", m.vp.Width, m.vp.Height)
	}
}

func TestStageMsgUpdatesHeader(t *testing.T) {
	m := sized(t, 80, 24)
	m = step(t, m, stageMsg{order: 10, name: "partition"})
	if !strings.Contains(m.headerView(), "10 · partition") {
		t.Errorf("header = %q; want stage banner", m.headerView())
	}
}

func TestDoneMsg(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantSub string
	}{
		{"success", nil, "done"},
		{"failure", errors.New("boom"), "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := sized(t, 80, 24)
			m = step(t, m, doneMsg{err: tt.err})
			if !m.done {
				t.Fatal("done flag not set")
			}
			if m.err != tt.err {
				t.Errorf("err = %v; want %v", m.err, tt.err)
			}
			if !strings.Contains(m.footerView(), tt.wantSub) {
				t.Errorf("footer = %q; want substring %q", m.footerView(), tt.wantSub)
			}
		})
	}
}

func TestQuitKey(t *testing.T) {
	m := sized(t, 80, 24)
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("expected a quit command, got nil")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("quit command produced nil message")
	} else if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("quit command produced %T; want tea.QuitMsg", msg)
	}
}

// keyMsg builds a tea.KeyMsg whose String() matches the given key name for the
// subset of keys the model branches on.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	default: // single runes like k, G, q, g, b
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}
