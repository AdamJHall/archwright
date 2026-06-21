package stages

import (
	"os/exec"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/ui"
)

// yay is the Phase B 10-yay equivalent: install the configured AUR helper (the
// -bin variant, no Go compile). Defaults to yay; aur_helper: paru selects paru.
// makepkg must not run as root, so this uses Cmd/Shell as the user.
type yay struct{}

func init() { register(yay{}) }

func (yay) Order() int   { return 10 }
func (yay) Name() string { return "yay" }
func (yay) Phase() Phase { return Bootstrap }

func (yay) Run(ctx *Context) error {
	helper := aurHelper(ctx.Cfg)
	if _, err := exec.LookPath(helper); err == nil {
		ui.OK("%s already installed", helper)
		return nil
	}
	if err := ctx.R.Root("pacman", "-S", "--needed", "--noconfirm", "git", "base-devel"); err != nil {
		return err
	}
	// Clone + build + clean in one shell so the temp dir and cwd are handled.
	if err := cloneBuild(ctx, "https://aur.archlinux.org/"+helper+"-bin.git", "makepkg -si --noconfirm"); err != nil {
		return err
	}
	ui.OK("%s installed", helper)
	return nil
}

// aurHelper returns the configured AUR helper binary, defaulting to "yay".
func aurHelper(cfg *config.Config) string {
	if cfg.AurHelper != "" {
		return cfg.AurHelper
	}
	return "yay"
}
