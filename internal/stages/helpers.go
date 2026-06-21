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

// ensureKernelParam adds a single kernel command-line token, idempotently. It is
// the bootloader-aware seam for kernel-cmdline edits, keyed off the configured
// bootloader:
//   - grub: appends tok to GRUB_CMDLINE_LINUX_DEFAULT in /etc/default/grub
//     (grep-guarded sed) — UNCHANGED from before, byte-identical command.
//   - systemd-boot: appends tok to /etc/kernel/cmdline (the single-line cmdline
//     consumed by kernel-install when generating loader entries), grep-guarded so
//     re-runs don't duplicate the token. The file is created if missing.
func ensureKernelParam(ctx *Context, tok string) error {
	if ctx.Cfg.Bootloader.EffectiveKind() == "systemd-boot" {
		// /etc/kernel/cmdline is a single space-separated line. Add tok only if its
		// word boundary isn't already present; create the file if it doesn't exist.
		return ctx.R.Shell(fmt.Sprintf(
			`grep -qE '\b%[1]s\b' /etc/kernel/cmdline 2>/dev/null || `+
				`{ sudo touch /etc/kernel/cmdline && `+
				`sudo sed -i -E '$ s/$/ %[1]s/' /etc/kernel/cmdline; }`,
			tok))
	}
	return ctx.R.Shell(fmt.Sprintf(
		`grep -qE 'GRUB_CMDLINE_LINUX_DEFAULT="[^"]*\b%[1]s\b' /etc/default/grub || `+
			`sudo sed -i -E 's/(GRUB_CMDLINE_LINUX_DEFAULT=")/\1%[1]s /' /etc/default/grub`,
		tok))
}

// regenerateBootConfig regenerates the bootloader's generated configuration after a
// kernel-cmdline or theme change. It is the bootloader-aware seam for the
// "apply the config" step, keyed off the configured bootloader:
//   - grub: runs grub-mkconfig -o /boot/grub/grub.cfg through Root — UNCHANGED from
//     before, byte-identical command.
//   - systemd-boot: there is no grub.cfg to regenerate. We run `bootctl update` to
//     refresh the installed boot loader binary on the ESP; loader entries are
//     regenerated separately by kernel-install when the cmdline changes. This is
//     VM-validation-pending: on some setups `bootctl update` is a no-op and the
//     entry refresh happens via the kernel-install hooks instead — verify against a
//     real systemd-boot system in a QEMU VM before trusting on hardware.
func regenerateBootConfig(ctx *Context) error {
	if ctx.Cfg.Bootloader.EffectiveKind() == "systemd-boot" {
		return ctx.R.Root("bootctl", "update")
	}
	return ctx.R.Root("grub-mkconfig", "-o", "/boot/grub/grub.cfg")
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
