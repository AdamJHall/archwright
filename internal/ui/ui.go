// Package ui centralizes terminal output and prompts: leveled logging
// (charmbracelet/log), styled stage headers (lipgloss), and the destructive
// confirmation prompt (huh). Keeping it here means stages and the runner share
// one consistent look and one place to gate TTY behaviour.
//
// Output normally goes to os.Stderr. In TUI mode the program installs a sink
// (SetSink) so Header/Step/OK/Info/Warn/Error flow into the viewport instead of
// straight to the terminal — bubbletea owns the screen then, so nothing may
// write to os.Stdout/os.Stderr directly. Plain mode (sink == nil) is the
// original code path, byte-for-byte unchanged. Prompts always run before the
// TUI starts (see main.runPhase) and so always use the real terminal.
package ui

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
)

// sink, when non-nil, receives all leveled/styled output (TUI mode). When nil,
// output goes to os.Stderr exactly as before (plain mode).
var sink io.Writer

var logger = log.NewWithOptions(os.Stderr, log.Options{
	ReportTimestamp: false,
})

// SetSink redirects all ui output to w (the TUI viewport pump). Passing nil
// restores the default os.Stderr behaviour. The leveled logger is repointed so
// Info/Warn/Error keep their exact formatting, just to a different writer.
func SetSink(w io.Writer) {
	sink = w
	if w != nil {
		logger.SetOutput(w)
	} else {
		logger.SetOutput(os.Stderr)
	}
}

// out returns the current output stream: the sink in TUI mode, else os.Stderr.
// This keeps plain mode byte-for-byte identical (writes still go to os.Stderr).
func out() io.Writer {
	if sink != nil {
		return sink
	}
	return os.Stderr
}

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("63")). // soft purple
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("63")).
			BorderTop(true).BorderBottom(true).
			Padding(0, 1)

	stepStyle    = lipgloss.NewStyle().Faint(true)
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
)

// Header prints a styled banner for a stage, e.g. "10 · partition".
func Header(order int, name string) {
	fmt.Fprintln(out(), headerStyle.Render(fmt.Sprintf("%02d · %s", order, name)))
}

// Step echoes a state-changing command about to run (the Go analogue of the
// bash `run` echo). Dimmed so real command output stands out.
func Step(format string, a ...any) {
	fmt.Fprintln(out(), stepStyle.Render("→ "+fmt.Sprintf(format, a...)))
}

// Info / Warn / Error are leveled, styled log lines.
func Info(msg string, kv ...any)  { logger.Info(msg, kv...) }
func Warn(msg string, kv ...any)  { logger.Warn(msg, kv...) }
func Error(msg string, kv ...any) { logger.Error(msg, kv...) }

// OK prints a green success line for completed steps/stages.
func OK(format string, a ...any) {
	fmt.Fprintln(out(), successStyle.Render("✓ ")+fmt.Sprintf(format, a...))
}

// Password prompts (hidden input) for a password and a confirmation, returning
// the value once both match and are non-empty. Requires a TTY. Used to seed the
// archinstall credentials file for a real (non --yes) install.
func Password(prompt string) (string, error) {
	var pw, confirm string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(prompt).EchoMode(huh.EchoModePassword).Value(&pw).
			Validate(func(s string) error {
				if s == "" {
					return fmt.Errorf("password must not be empty")
				}
				return nil
			}),
		huh.NewInput().Title("Confirm password").EchoMode(huh.EchoModePassword).Value(&confirm).
			Validate(func(s string) error {
				if s != pw {
					return fmt.Errorf("passwords do not match")
				}
				return nil
			}),
	)).Run()
	if err != nil {
		return "", fmt.Errorf("password prompt aborted: %w", err)
	}
	return pw, nil
}

// ConfirmErase blocks until the user types the exact phrase (e.g. "ERASE"),
// returning an error if they abort. Skipped by callers when assume-yes is set.
// Requires a TTY; on a non-interactive stream huh returns an error, which the
// caller should treat as "not confirmed".
func ConfirmErase(phrase, prompt string) error {
	var typed string
	err := huh.NewInput().
		Title(prompt).
		Description(fmt.Sprintf("Type %q to proceed, or Ctrl-C to abort:", phrase)).
		Value(&typed).
		Validate(func(s string) error {
			if s != phrase {
				return fmt.Errorf("must type %q exactly", phrase)
			}
			return nil
		}).
		Run()
	if err != nil {
		return fmt.Errorf("confirmation aborted: %w", err)
	}
	return nil
}
