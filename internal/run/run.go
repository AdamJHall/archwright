// Package run centralizes command execution, dry-run, logging, and a recorded
// "plan" of everything a run would do. The plan makes stages testable: a test
// runs a stage in dry-run and asserts on the commands it planned, without
// touching the host. This is the Go equivalent of the bash `run`/`run_sh`
// helpers — every state-changing action goes through a Runner.
package run

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/AdamJHall/archwright/internal/ui"
)

type Runner struct {
	DryRun bool // print/record commands instead of executing them
	Sudo   bool // prefix privileged commands with sudo (Phase B as user); false in Phase A (already root)

	// Env, when non-empty, is layered (key=value) on top of the inherited process
	// environment for every executed command. Dir, when set, is the working dir.
	Env map[string]string
	Dir string

	// Plan records every command (Cmd/Shell/Chroot) in order, whether or not it
	// actually executed. Tests assert on this.
	Plan []string
}

func (r *Runner) record(line string) { r.Plan = append(r.Plan, line) }

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
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	r.prepare(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Root runs a command with root privileges: directly when already root (Phase A,
// live ISO) or via sudo otherwise (Phase B, as the user).
func (r *Runner) Root(name string, args ...string) error {
	if r.Sudo {
		return r.Cmd("sudo", append([]string{name}, args...)...)
	}
	return r.Cmd(name, args...)
}

// RootShell runs a shell script with root privileges through `bash -c`: via
// `sudo bash -c <script>` when not already root (Phase B, as the user) or
// directly when already root (Phase A, live ISO). This lets a shell pipeline
// run wholly as root without inner per-command `sudo`. It records/prints the
// same `sh: <script>` line as Shell so the recorded .Plan stays uniform.
func (r *Runner) RootShell(script string) error {
	r.record("sh: " + script)
	ui.Step("sh: %s", script)
	if r.DryRun {
		return nil
	}
	var cmd *exec.Cmd
	if r.Sudo {
		cmd = exec.Command("sudo", "bash", "-c", script)
	} else {
		cmd = exec.Command("bash", "-c", script)
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	r.prepare(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("shell: %w", err)
	}
	return nil
}

// Try runs a command but never fails the stage (the analogue of bash `|| true`),
// for best-effort steps like partprobe/udevadm.
func (r *Runner) Try(name string, args ...string) {
	_ = r.Cmd(name, args...)
}

// TryRoot is the Root analogue of Try: a best-effort privileged command that
// never fails the stage. It adds sudo in Phase B and runs direct in Phase A,
// keyed off Sudo, exactly like Root.
func (r *Runner) TryRoot(name string, args ...string) {
	_ = r.Root(name, args...)
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
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
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
