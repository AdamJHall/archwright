package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
)

// captureStderr runs fn with os.Stderr replaced by a pipe and returns what was
// written. It restores os.Stderr afterwards.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

// TestPlainModeUnchanged is the hard requirement: with no sink set (plain mode),
// Header/Step/OK write to os.Stderr exactly the styled bytes they always have —
// the lipgloss-rendered string plus a trailing newline. If this drifts, the
// non-TTY/CI/`--dry-run | less` output has regressed.
func TestPlainModeUnchanged(t *testing.T) {
	SetSink(nil) // ensure plain mode
	t.Cleanup(func() { SetSink(nil) })

	tests := []struct {
		name string
		call func()
		want string
	}{
		{
			name: "Header",
			call: func() { Header(10, "partition") },
			want: headerStyle.Render(fmt.Sprintf("%02d · %s", 10, "partition")) + "\n",
		},
		{
			name: "Step",
			call: func() { Step("pacman -S %s", "vim") },
			want: stepStyle.Render("→ "+fmt.Sprintf("pacman -S %s", "vim")) + "\n",
		},
		{
			name: "OK",
			call: func() { OK("done") },
			want: successStyle.Render("✓ ") + "done" + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := captureStderr(t, tt.call)
			if got != tt.want {
				t.Errorf("plain-mode output drifted\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestSinkRoutesOutput confirms TUI mode sends styled output to the sink and not
// to os.Stderr, so bubbletea retains exclusive ownership of the screen.
func TestSinkRoutesOutput(t *testing.T) {
	var buf bytes.Buffer
	SetSink(&buf)
	t.Cleanup(func() { SetSink(nil) })

	stderr := captureStderr(t, func() {
		Header(20, "archinstall")
		Step("genfstab")
		OK("ready")
		Info("informational")
	})

	if stderr != "" {
		t.Errorf("TUI mode wrote to os.Stderr: %q", stderr)
	}
	for _, want := range []string{"archinstall", "genfstab", "ready", "informational"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("sink missing %q; got %q", want, buf.String())
		}
	}
}
