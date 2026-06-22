package ui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0.0s"},
		{400 * time.Millisecond, "0.4s"},
		{12300 * time.Millisecond, "12.3s"},
		{59 * time.Second, "59.0s"},
		{60 * time.Second, "1m00s"},
		{192 * time.Second, "3m12s"},
		{75 * time.Minute, "75m00s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns what was
// written. The ui functions write to the current os.Stderr at call time.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	return buf.String()
}

// TestDisableColorPlaintext guards the colour-correctness cleanup: with colour
// disabled, styled output must carry no ANSI escapes while keeping the content
// (so `2>file` / NO_COLOR yields clean plaintext).
func TestDisableColorPlaintext(t *testing.T) {
	DisableColor()
	out := captureStderr(t, func() {
		RunBanner("v1.2.0", "install", 9, true)
		StageStart(2, 9, 10, "archinstall")
		StageTime(3200 * time.Millisecond)
		OK("archinstall complete")
		RunComplete("install", 9, 4*time.Minute+20*time.Second)
	})
	if strings.Contains(out, "\x1b[") {
		t.Errorf("colour-disabled output contains ANSI escapes:\n%q", out)
	}
	for _, want := range []string{"DRY RUN", "[2/9]", "archinstall", "3.2s", "install complete", "4m20s"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; full output:\n%s", want, out)
		}
	}
}
