package stages

import (
	"strings"

	"github.com/AdamJHall/archwright/internal/ui"
)

// flatpak is the Phase B 30-flatpak equivalent: ensure flatpak, register the
// declared remotes, then install each configured app from its named remote.
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

	if err := ensureTool(ctx, "flatpak", "flatpak"); err != nil {
		return err
	}

	// Register exactly the declared remotes — nothing is implicit. flathub, if
	// used, must be listed in flatpak_remotes (validation guarantees every app's
	// remote is declared).
	for _, rem := range ctx.Cfg.FlatpakRemotes {
		if err := ctx.R.Cmd("flatpak", "remote-add", "--if-not-exists", rem.Name, rem.URL); err != nil {
			return err
		}
	}
	// Install each app from its named remote. Each entry is "remote:appid"
	// (enforced at validate time); per-app install keeps remote attribution
	// unambiguous (an accepted cost over one batched install).
	for _, app := range apps {
		remote, appid, _ := strings.Cut(app, ":")
		if err := ctx.R.Cmd("flatpak", "install", "-y", "--noninteractive", remote, appid); err != nil {
			return err
		}
	}
	ui.OK("flatpaks installed")
	return nil
}
