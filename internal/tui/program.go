package tui

import (
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

// teaWriter is the pump: it turns bytes written by the Runner (subprocess
// stdout+stderr) and by the ui sink into outputMsg messages for the program.
// One message per Write; interleaving matches the terminal because both the
// child's stdout and stderr point at the same writer.
type teaWriter struct{ p *tea.Program }

func (w teaWriter) Write(b []byte) (int, error) {
	w.p.Send(outputMsg(string(b)))
	return len(b), nil
}

// Program wraps a bubbletea program for archwright's viewport TUI. It exposes
// the io.Writer sink (for Runner.Out and the ui package) and typed Send helpers
// so callers never construct message values directly.
type Program struct {
	p *tea.Program
}

// NewProgram builds the alt-screen viewport program. Call Writer() to obtain the
// sink to wire into the Runner and the ui package, then Run() on the main thread
// while the phase executes in a goroutine.
func NewProgram() *Program {
	p := tea.NewProgram(newModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	return &Program{p: p}
}

// Writer returns the io.Writer that forwards output into the viewport.
func (pr *Program) Writer() io.Writer { return teaWriter{p: pr.p} }

// Stage updates the header banner for a newly started stage.
func (pr *Program) Stage(order int, name string) {
	pr.p.Send(stageMsg{order: order, name: name})
}

// Output forwards a line of styled ui output (Header/Step/OK/...) into the view.
func (pr *Program) Output(s string) { pr.p.Send(outputMsg(s)) }

// Done signals the phase finished; err is nil on success.
func (pr *Program) Done(err error) { pr.p.Send(doneMsg{err: err}) }

// Run runs the tea program on the calling (main) goroutine until quit.
func (pr *Program) Run() error {
	_, err := pr.p.Run()
	return err
}
