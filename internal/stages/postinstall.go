package stages

import (
	"fmt"
	"regexp"

	"github.com/AdamJHall/archwright/internal/config"
)

// This file holds the Phase A post-archinstall steps that run inside
// arch-chroot /mnt: configuring custom repos and installing custom kernels so the
// installed system already has them on first boot. Everything runs as root in the
// chroot (no sudo), and the repo config persists into the target so Phase B
// package installs resolve against it too.

// chrootCmd runs a command in the target via arch-chroot /mnt.
func chrootCmd(ctx *Context, name string, args ...string) error {
	return ctx.R.Chroot("/mnt", append([]string{name}, args...)...)
}

// chrootShell runs a shell snippet as root in the target.
func chrootShell(ctx *Context, script string) error {
	return ctx.R.Cmd("arch-chroot", "/mnt", "bash", "-c", script)
}

// configureRepos sets up each custom pacman repository in the target: import +
// locally sign its key, run its setup script, and append its pacman.conf section
// (idempotently). A final `pacman -Sy` refreshes the new databases.
func configureRepos(ctx *Context, repos []config.Repo) error {
	for _, r := range repos {
		if r.Key != "" {
			recv := []string{"--recv-keys", r.Key}
			if r.Keyserver != "" {
				recv = append(recv, "--keyserver", r.Keyserver)
			}
			if err := chrootCmd(ctx, "pacman-key", recv...); err != nil {
				return err
			}
			if err := chrootCmd(ctx, "pacman-key", "--lsign-key", r.Key); err != nil {
				return err
			}
		}
		if r.Setup != "" {
			if err := chrootShell(ctx, r.Setup); err != nil {
				return err
			}
		}
		if r.Server != "" || r.Include != "" {
			if err := chrootShell(ctx, pacmanConfEntry(r)); err != nil {
				return err
			}
		}
	}
	return chrootCmd(ctx, "pacman", "-Sy")
}

// pacmanConfEntry returns a root shell snippet that appends the repo's section to
// /etc/pacman.conf unless it's already present (grep guard).
func pacmanConfEntry(r config.Repo) string {
	block := "\n[" + r.Name + "]\n"
	if r.Server != "" {
		block += "Server = " + r.Server + "\n"
	}
	if r.Include != "" {
		block += "Include = " + r.Include + "\n"
	}
	return fmt.Sprintf(
		"grep -q '^\\[%s\\]' /etc/pacman.conf || printf '%%s' '%s' >> /etc/pacman.conf",
		r.Name, block,
	)
}

// configureUserShell sets the user's login shell in the target via chsh. archinstall
// always creates users with the default /bin/bash; this applies cfg.user.shell so the
// installed system boots with e.g. zsh as the login shell on first login. The shell
// binary must be in the pacstrap set (e.g. zsh) so it already exists — and is listed in
// /etc/shells — at chsh time.
func configureUserShell(ctx *Context, user, shell string) error {
	return chrootCmd(ctx, "chsh", "-s", shell, user)
}

// enableMultilib uncomments the [multilib] repository section in the target's
// /etc/pacman.conf (Arch ships it commented out) so 32-bit packages like steam resolve
// in Phase B, then syncs the new database. The sed range strips the leading '#' from the
// `[multilib]` header line and its following `Include` line. It is idempotent: an
// already-enabled section has no leading '#' on the header, so the range never matches.
func enableMultilib(ctx *Context) error {
	const uncomment = `sed -i '/^#\[multilib\]/,/^#Include/ s/^#//' /etc/pacman.conf`
	if err := chrootShell(ctx, uncomment); err != nil {
		return err
	}
	return chrootCmd(ctx, "pacman", "-Sy")
}

// configureLocales enables additional locales in the target's /etc/locale.gen
// and regenerates them. archinstall already enables + generates the default
// locale (system.locale -> LANG); this adds the extras (system.locales) so the
// installed system has them on first boot. Uncommenting is idempotent — the
// matching `#en_US.UTF-8 UTF-8` line is stripped of its leading `#`.
func configureLocales(ctx *Context, locales []string) error {
	for _, l := range locales {
		// Escape regex metacharacters in the locale (notably the '.' before the
		// charset) so the sed anchor matches literally.
		re := regexp.QuoteMeta(l)
		script := fmt.Sprintf(`sed -i 's/^#\(%s\b\)/\1/' /etc/locale.gen`, re)
		if err := chrootShell(ctx, script); err != nil {
			return err
		}
	}
	return chrootCmd(ctx, "locale-gen")
}

// installKernels installs the configured kernels in the target, optionally
// removes the stock `linux` kernel, and sets/applies the default kernel. The
// package install and stock-kernel removal are bootloader-agnostic; only the
// "make this kernel the default + regenerate boot config" tail differs per
// bootloader, branched off the configured bootloader (defaulting to grub).
func installKernels(ctx *Context, k config.KernelConfig) error {
	// Skip the pacman install when there are no extra kernel packages (Issue #3):
	// postInstall now also calls this purely to pin a base kernel as the default,
	// and `pacman -S` with zero package arguments would error.
	if len(k.Packages) > 0 {
		args := append([]string{"-S", "--needed", "--noconfirm"}, k.Packages...)
		if err := chrootCmd(ctx, "pacman", args...); err != nil {
			return err
		}
	}
	if k.ReplaceStock {
		// Validated to be safe: at least one replacement kernel is installed above.
		if err := chrootCmd(ctx, "pacman", "-Rns", "--noconfirm", "linux"); err != nil {
			return err
		}
	}
	if ctx.Cfg.Bootloader.EffectiveKind() == "systemd-boot" {
		return installKernelsSystemdBoot(ctx, k)
	}
	return installKernelsGrub(ctx, k)
}

// installKernelsGrub sets the GRUB default kernel (GRUB_TOP_LEVEL) and regenerates
// grub.cfg so the new entries (and default) take effect on first boot.
func installKernelsGrub(ctx *Context, k config.KernelConfig) error {
	if k.Default != "" {
		// GRUB_TOP_LEVEL pins a specific kernel image as the default menu entry.
		set := fmt.Sprintf(
			`sed -i '/^GRUB_TOP_LEVEL=/d' /etc/default/grub && `+
				`echo 'GRUB_TOP_LEVEL="/boot/vmlinuz-%s"' >> /etc/default/grub`,
			k.Default,
		)
		if err := chrootShell(ctx, set); err != nil {
			return err
		}
	}
	return chrootCmd(ctx, "grub-mkconfig", "-o", "/boot/grub/grub.cfg")
}

// installKernelsSystemdBoot makes the requested kernel the systemd-boot default.
// systemd-boot has no grub-mkconfig step: its entries are generated independently
// (e.g. by the kernel-install hooks archinstall sets up), and the default entry is
// selected by the `default` line in /boot/loader/loader.conf. We rewrite that line
// to match the requested kernel's entry id pattern (linux-<pkg>*), which
// kernel-install names per the installed kernel package. When no default kernel is
// configured, archinstall's own loader.conf default is left untouched.
//
// VM-validation-pending: the exact loader entry id naming (linux-<pkg>.conf vs a
// machine-id-prefixed name) is environment-dependent and MUST be verified against a
// real archinstall systemd-boot run in a QEMU VM before trusting on hardware. The
// glob (`linux-<pkg>*.conf`) is the defensible best-effort until then. We edit
// loader.conf directly rather than `bootctl set-default` because bootctl in the
// chroot may not see the ESP the way the booted system does.
func installKernelsSystemdBoot(ctx *Context, k config.KernelConfig) error {
	if k.Default == "" {
		return nil
	}
	// Replace (or append) the `default` line in loader.conf, idempotently.
	set := fmt.Sprintf(
		`entry="$(basename "$(ls /boot/loader/entries/*%s*.conf 2>/dev/null | head -n1)" 2>/dev/null)"; `+
			`[ -n "$entry" ] || entry="%s"; `+
			`if grep -q '^default ' /boot/loader/loader.conf 2>/dev/null; then `+
			`sed -i "s|^default .*|default ${entry}|" /boot/loader/loader.conf; `+
			`else echo "default ${entry}" >> /boot/loader/loader.conf; fi`,
		k.Default, k.Default,
	)
	return chrootShell(ctx, set)
}
