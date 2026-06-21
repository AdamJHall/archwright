package stages

import (
	"strings"

	"github.com/AdamJHall/archwright/internal/ui"
)

// plymouth is the Phase B 50-plymouth equivalent: install Plymouth, wire its
// initramfs hook, set the 'quiet splash' kernel params, set the theme, and
// regenerate grub.cfg.
type plymouth struct{}

func init() { register(plymouth{}) }

func (plymouth) Order() int   { return 50 }
func (plymouth) Name() string { return "plymouth" }
func (plymouth) Phase() Phase { return Bootstrap }

func (plymouth) Run(ctx *Context) error {
	theme := ctx.Cfg.Plymouth.Theme
	if theme == "" {
		theme = "bgrt"
	}
	cmdline := ctx.Cfg.GRUB.CmdlineExtra
	if cmdline == "" {
		cmdline = "quiet splash"
	}

	if err := ensureTool(ctx, "plymouth", "plymouth"); err != nil {
		return err
	}

	// Add the plymouth hook after 'udev' (fallback 'systemd'), idempotently.
	if err := ctx.R.Shell(
		`grep -qE '^HOOKS=.*\bplymouth\b' /etc/mkinitcpio.conf || ` +
			`sudo sed -i -E 's/^(HOOKS=.*)\budev\b/\1udev plymouth/; t; s/^(HOOKS=.*)\bsystemd\b/\1systemd plymouth/' /etc/mkinitcpio.conf`,
	); err != nil {
		return err
	}

	// Ensure each kernel param is present in GRUB_CMDLINE_LINUX_DEFAULT.
	for _, tok := range strings.Fields(cmdline) {
		if err := ensureKernelParam(ctx, tok); err != nil {
			return err
		}
	}

	// -R rebuilds the initramfs, picking up the new HOOKS line too.
	if err := ctx.R.Root("plymouth-set-default-theme", "-R", theme); err != nil {
		return err
	}
	// Regenerating the bootloader config is GRUB's concern, not Plymouth's: route
	// it through the shared bootloader-aware helper (same emitted command).
	if err := regenerateBootConfig(ctx); err != nil {
		return err
	}

	ui.OK("plymouth configured")
	return nil
}
