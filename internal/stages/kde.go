package stages

import (
	"fmt"

	"github.com/AdamJHall/archwright/internal/ui"
)

// kde is the Phase B 70-kde equivalent: select the Plasma global theme by writing
// LookAndFeelPackage into kdeglobals. Since Plasma 5.24 this is enough on its own —
// at login Plasma compares the configured global theme against ~/.config/kdedefaults
// and, on a mismatch, regenerates that cascade from the theme package (pulling in its
// color scheme, plasma theme, icons, fonts, …). So no running Plasma session is
// required, unlike the old plasma-apply-* helpers.
//
// Cursor theme, color scheme and wallpaper are intentionally not handled here: the
// first two come for free with the global theme, and wallpaper lives in the per-host
// appletsrc (generated containment IDs) which can't be set ahead of first login.
type kde struct{}

func init() { register(kde{}) }

func (kde) Order() int   { return 70 }
func (kde) Name() string { return "kde" }
func (kde) Phase() Phase { return Bootstrap }

func (kde) Run(ctx *Context) error {
	if de := ctx.Cfg.Desktop.Environment; de != "" && de != "kde" {
		ui.Info(fmt.Sprintf("desktop.environment is %q — skipping KDE stage", de))
		return nil
	}

	laf := ctx.Cfg.KDE.LookAndFeel
	if laf == "" {
		ui.Info("kde.look_and_feel unset — skipping KDE stage")
		return nil
	}

	// Write to ~/.config/kdeglobals; applied by Plasma on next login.
	if err := ctx.R.Cmd("kwriteconfig6",
		"--file", "kdeglobals", "--group", "KDE", "--key", "LookAndFeelPackage", laf); err != nil {
		return err
	}

	ui.OK("KDE global theme set to %q (applies on next login)", laf)
	return nil
}
