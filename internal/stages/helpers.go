package stages

import (
	"fmt"
	"os/exec"
)

// This file holds small helpers shared by the Phase B stages, collapsing patterns
// that were duplicated across several stages. They emit the same commands the
// inline code did, so the recorded .Plan (and therefore the stage tests) is
// unchanged — this is a behaviour-preserving refactor.

// ensureTool installs pkg via pacman (--needed --noconfirm) when bin is not
// already on PATH. The LookPath probe is read-only and not recorded; only the
// install (when needed) goes through the Runner.
func ensureTool(ctx *Context, bin, pkg string) error {
	if _, err := exec.LookPath(bin); err == nil {
		return nil
	}
	return ctx.R.Root("pacman", "-S", "--needed", "--noconfirm", pkg)
}

// ensureKernelParam adds a single token to GRUB_CMDLINE_LINUX_DEFAULT in
// /etc/default/grub, idempotently (grep-guarded sed).
func ensureKernelParam(ctx *Context, tok string) error {
	return ctx.R.Shell(fmt.Sprintf(
		`grep -qE 'GRUB_CMDLINE_LINUX_DEFAULT="[^"]*\b%[1]s\b' /etc/default/grub || `+
			`sudo sed -i -E 's/(GRUB_CMDLINE_LINUX_DEFAULT=")/\1%[1]s /' /etc/default/grub`,
		tok))
}

// cloneBuild git-clones a repo into a temp dir, runs inCheckout from within it,
// and removes the temp dir — the shared "clone, build, clean" idiom. cloneArgs is
// the git-clone argument string (any flags plus the URL); inCheckout is the shell
// run from inside the checkout.
func cloneBuild(ctx *Context, cloneArgs, inCheckout string) error {
	return ctx.R.Shell(fmt.Sprintf(
		`tmp="$(mktemp -d)" && git clone %s "$tmp" && (cd "$tmp" && %s) && rm -rf "$tmp"`,
		cloneArgs, inCheckout))
}
