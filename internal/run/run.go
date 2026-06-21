// Package run centralizes command execution, dry-run, logging, and a recorded
// "plan" of everything a run would do. The plan makes stages testable: a test
// runs a stage in dry-run and asserts on the commands it planned, without
// touching the host. This is the Go equivalent of the bash `run`/`run_sh`
// helpers — every state-changing action goes through a Runner.
package run

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/AdamJHall/archwright/internal/ui"
)

type Runner struct {
	DryRun bool // print/record commands instead of executing them
	Sudo   bool // prefix privileged commands with sudo (Phase B as user); false in Phase A (already root)

	// Out, when non-nil, receives BOTH stdout and stderr of every executed
	// command. It is the hook for a TUI viewport that captures streamed output;
	// when nil, output streams to os.Stdout/os.Stderr exactly as before.
	Out io.Writer
	// Env, when non-empty, is layered (key=value) on top of the inherited process
	// environment for every executed command. Dir, when set, is the working dir.
	Env map[string]string
	Dir string

	// Plan records every command (Cmd/Shell/Chroot) in order, whether or not it
	// actually executed. Tests assert on this.
	Plan []string
}

func (r *Runner) record(line string) { r.Plan = append(r.Plan, line) }

// outWriter returns r.Out when set, else the given default stream, so a nil Out
// preserves the original os.Stdout/os.Stderr wiring exactly.
func (r *Runner) outWriter(def io.Writer) io.Writer {
	if r.Out != nil {
		return r.Out
	}
	return def
}

// prepare applies the Runner's Env and Dir (if any) to a command before it runs.
func (r *Runner) prepare(cmd *exec.Cmd) {
	if len(r.Env) > 0 {
		env := os.Environ()
		for k, v := range r.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}
}

// Cmd runs a program with args, streaming its output (we deliberately do not
// hide it behind a spinner so installer progress and errors stay visible). In
// dry-run it only logs and records.
func (r *Runner) Cmd(name string, args ...string) error {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.record(line)
	ui.Step("%s", line)
	if r.DryRun {
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = r.outWriter(os.Stdout), r.outWriter(os.Stderr), os.Stdin
	r.prepare(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Capture runs name+args and returns the command's stdout as a string, recording
// it in .Plan exactly like Cmd. stderr still streams (to Out or os.Stderr). In
// dry-run it records and returns "" with a nil error. Use this for the rare
// state-querying command whose output a stage needs, rather than dropping to
// os/exec directly (which would bypass dry-run and the recorded plan).
func (r *Runner) Capture(name string, args ...string) (string, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.record(line)
	ui.Step("%s", line)
	if r.DryRun {
		return "", nil
	}
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, r.outWriter(os.Stderr), os.Stdin
	r.prepare(cmd)
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s: %w", name, err)
	}
	return out.String(), nil
}

// Root runs a command with root privileges: directly when already root (Phase A,
// live ISO) or via sudo otherwise (Phase B, as the user).
func (r *Runner) Root(name string, args ...string) error {
	if r.Sudo {
		return r.Cmd("sudo", append([]string{name}, args...)...)
	}
	return r.Cmd(name, args...)
}

// Try runs a command but never fails the stage (the analogue of bash `|| true`),
// for best-effort steps like partprobe/udevadm.
func (r *Runner) Try(name string, args ...string) {
	_ = r.Cmd(name, args...)
}

// Shell runs a string through `bash -c`, for pipes/redirects/conditionals
// (genfstab, idempotent sed edits, gpg key import). Prefer Cmd where possible.
func (r *Runner) Shell(script string) error {
	r.record("sh: " + script)
	ui.Step("sh: %s", script)
	if r.DryRun {
		return nil
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = r.outWriter(os.Stdout), r.outWriter(os.Stderr), os.Stdin
	r.prepare(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("shell: %w", err)
	}
	return nil
}

// Chroot runs a command inside arch-chroot at root (Phase A live ISO).
func (r *Runner) Chroot(root string, args ...string) error {
	return r.Cmd("arch-chroot", append([]string{root}, args...)...)
}
