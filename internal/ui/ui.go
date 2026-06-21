// Package ui centralizes terminal output and prompts: leveled logging
// (charmbracelet/log), styled stage banners + run bookends (lipgloss), and the
// destructive confirmation prompt (huh). Keeping it here means stages and the
// runner share one consistent look and one place to gate TTY/colour behaviour.
//
// All styled output goes to os.Stderr, and the lipgloss renderer is bound to
// os.Stderr too, so colour detection (TTY + NO_COLOR) matches where the bytes
// actually land — piping `2>file` yields clean plaintext, a real terminal gets
// colour. Subprocess stdout/stderr stream straight through the Runner, untouched.
package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/muesli/termenv"
)

var (
	renderer = lipgloss.NewRenderer(os.Stderr)
	logger   = log.NewWithOptions(os.Stderr, log.Options{ReportTimestamp: false})
)

var (
	bannerStyle  = renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	headerStyle  = renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	ruleStyle    = renderer.NewStyle().Foreground(lipgloss.Color("63"))
	dimStyle     = renderer.NewStyle().Faint(true)
	stepStyle    = renderer.NewStyle().Faint(true)
	successStyle = renderer.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	failStyle    = renderer.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	badgeStyle   = renderer.NewStyle().Bold(true).
			Foreground(lipgloss.Color("0")).Background(lipgloss.Color("214")).Padding(0, 1)
)

// DisableColor forces plain ASCII output (the --no-color flag). NO_COLOR is also
// honoured automatically by the stderr-bound renderer and logger; this makes the
// override explicit and covers both.
func DisableColor() {
	renderer.SetColorProfile(termenv.Ascii)
	logger.SetColorProfile(termenv.Ascii)
}

const ruleWidth = 60

// RunBanner opens a phase run with a one-line header (version · phase · stage
// count), tagged DRY RUN when nothing will actually execute.
func RunBanner(version, phase string, total int, dryRun bool) {
	line := bannerStyle.Render("archwright") + dimStyle.Render(" "+version) +
		"  " + phase + dimStyle.Render(fmt.Sprintf("  ·  %d stages", total))
	if dryRun {
		line += "  " + badgeStyle.Render("DRY RUN")
	}
	fmt.Fprintf(os.Stderr, "\n  %s\n", line)
}

// StageStart prints a stage banner carrying an [i/n] progress counter, e.g.
// "━━ [3/9] 30 · flatpak ━━━━━━".
func StageStart(idx, total, order int, name string) {
	label := fmt.Sprintf("[%d/%d] %02d · %s", idx, total, order, name)
	pad := ruleWidth - lipgloss.Width(label) - 4
	if pad < 3 {
		pad = 3
	}
	fmt.Fprintf(os.Stderr, "\n%s %s %s\n",
		ruleStyle.Render("━━"), headerStyle.Render(label), ruleStyle.Render(strings.Repeat("━", pad)))
}

// StageTime prints a faint per-stage elapsed-time footer. The stage itself emits
// the descriptive success line (OK); this only adds timing.
func StageTime(d time.Duration) {
	fmt.Fprintln(os.Stderr, dimStyle.Render("  ⤷ "+formatDuration(d)))
}

// RunComplete prints the closing summary for a successful run.
func RunComplete(phase string, total int, d time.Duration) {
	fmt.Fprintf(os.Stderr, "\n%s%s\n",
		successStyle.Render("✓ "+phase+" complete"),
		dimStyle.Render(fmt.Sprintf("  ·  %d stages  ·  %s", total, formatDuration(d))))
}

// RunFailed prints the closing summary when a stage failed; the underlying error
// is reported separately (ui.Error at the top level).
func RunFailed(idx, total int, name string, d time.Duration) {
	fmt.Fprintf(os.Stderr, "\n%s%s\n",
		failStyle.Render(fmt.Sprintf("✗ failed at [%d/%d] %s", idx, total, name)),
		dimStyle.Render("  ·  "+formatDuration(d)))
}

// formatDuration renders a compact, human duration: "0.4s", "12.3s", "3m12s".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
}

// Step echoes a state-changing command about to run (the Go analogue of the
// bash `run` echo). Dimmed so real command output stands out.
func Step(format string, a ...any) {
	fmt.Fprintln(os.Stderr, stepStyle.Render("→ "+fmt.Sprintf(format, a...)))
}

// Info / Warn / Error are leveled, styled log lines.
func Info(msg string, kv ...any)  { logger.Info(msg, kv...) }
func Warn(msg string, kv ...any)  { logger.Warn(msg, kv...) }
func Error(msg string, kv ...any) { logger.Error(msg, kv...) }

// OK prints a green success line for a completed step/stage.
func OK(format string, a ...any) {
	fmt.Fprintln(os.Stderr, successStyle.Render("✓ ")+fmt.Sprintf(format, a...))
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
