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
// removes the stock `linux` kernel, sets the GRUB default, and regenerates the
// GRUB config so the new entries (and default) take effect on first boot.
func installKernels(ctx *Context, k config.KernelConfig) error {
	args := append([]string{"-S", "--needed", "--noconfirm"}, k.Packages...)
	if err := chrootCmd(ctx, "pacman", args...); err != nil {
		return err
	}
	if k.ReplaceStock {
		// Validated to be safe: at least one replacement kernel is installed above.
		if err := chrootCmd(ctx, "pacman", "-Rns", "--noconfirm", "linux"); err != nil {
			return err
		}
	}
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

// partDev returns the kernel partition device for a base device and number:
// /dev/sda -> /dev/sda1, but /dev/nvme0n1 -> /dev/nvme0n1p1.
func partDev(dev string, n int) string {
	if len(dev) > 0 {
		if last := dev[len(dev)-1]; last >= '0' && last <= '9' {
			return fmt.Sprintf("%sp%d", dev, n)
		}
	}
	return fmt.Sprintf("%s%d", dev, n)
}
