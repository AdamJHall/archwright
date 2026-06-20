package stages

import (
	"os/exec"

	"github.com/AdamJHall/archwright/internal/ui"
)

// kde is the Phase B 70-kde equivalent: apply Plasma customization via the
// plasma-apply-* helpers. These need a running Plasma session, so failures are
// warned (not fatal) and missing tools are skipped.
type kde struct{}

func init() { register(kde{}) }

func (kde) Order() int   { return 70 }
func (kde) Name() string { return "kde" }
func (kde) Phase() Phase { return Bootstrap }

func (kde) Run(ctx *Context) error {
	k := ctx.Cfg.KDE

	// tool, value, optional leading flag (lookandfeel uses -a).
	type apply struct {
		tool, value, flag, label string
	}
	for _, a := range []apply{
		{"plasma-apply-lookandfeel", k.LookAndFeel, "-a", "look & feel"},
		{"plasma-apply-colorscheme", k.ColorScheme, "", "color scheme"},
		{"plasma-apply-cursortheme", k.CursorTheme, "", "cursor theme"},
		{"plasma-apply-wallpaperimage", k.Wallpaper, "", "wallpaper"},
	} {
		if a.value == "" {
			continue
		}
		if _, err := exec.LookPath(a.tool); err != nil {
			ui.Warn(a.tool+" not found — skipping", "what", a.label)
			continue
		}
		var err error
		if a.flag != "" {
			err = ctx.R.Cmd(a.tool, a.flag, a.value)
		} else {
			err = ctx.R.Cmd(a.tool, a.value)
		}
		if err != nil {
			ui.Warn(a.tool+" failed (need a running Plasma session?)", "value", a.value)
		}
	}

	ui.OK("KDE customization applied")
	return nil
}
