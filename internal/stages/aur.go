package stages

import "github.com/AdamJHall/archwright/internal/ui"

// aur installs AUR packages via the configured AUR helper (yay by default, or
// paru). No per-package special-casing: the helper imports any missing PGP keys
// a PKGBUILD declares (validpgpkeys) itself under --noconfirm, including
// 1Password's, so this stays generic.
type aur struct{}

func init() { register(aur{}) }

func (aur) Order() int   { return 40 }
func (aur) Name() string { return "aur" }
func (aur) Phase() Phase { return Bootstrap }

func (aur) Run(ctx *Context) error {
	pkgs := ctx.Cfg.AUR
	if len(pkgs) == 0 {
		ui.Warn("no AUR packages in config — skipping")
		return nil
	}
	args := append([]string{"-S", "--needed", "--noconfirm"}, pkgs...)
	return ctx.R.Cmd(aurHelper(ctx.Cfg), args...)
}
