package stages

import (
	"os/exec"

	"github.com/AdamJHall/archwright/internal/ui"
)

// flatpak is the Phase B 30-flatpak equivalent: ensure flatpak + the Flathub
// remote, then install the configured flatpaks.
type flatpak struct{}

func init() { register(flatpak{}) }

func (flatpak) Order() int   { return 30 }
func (flatpak) Name() string { return "flatpak" }
func (flatpak) Phase() Phase { return Bootstrap }

func (flatpak) Run(ctx *Context) error {
	apps := ctx.Cfg.Flatpaks
	if len(apps) == 0 {
		ui.Warn("no flatpaks in config — skipping")
		return nil
	}

	if _, err := exec.LookPath("flatpak"); err != nil {
		if err := ctx.R.Root("pacman", "-S", "--needed", "--noconfirm", "flatpak"); err != nil {
			return err
		}
	}

	if err := ctx.R.Cmd("flatpak", "remote-add", "--if-not-exists", "flathub",
		"https://flathub.org/repo/flathub.flatpakrepo"); err != nil {
		return err
	}
	if err := ctx.R.Cmd("flatpak", append([]string{"install", "-y", "--noninteractive", "flathub"}, apps...)...); err != nil {
		return err
	}
	ui.OK("flatpaks installed")
	return nil
}
