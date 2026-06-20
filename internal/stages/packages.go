package stages

import "github.com/AdamJHall/archwright/internal/ui"

// packages is the Phase B post/20-packages equivalent: install official-repo
// packages via pacman. Shows how a config slice drives a single command.
type packages struct{}

func init() { register(packages{}) }

func (packages) Order() int   { return 20 }
func (packages) Name() string { return "packages" }
func (packages) Phase() Phase { return Bootstrap }

func (packages) Run(ctx *Context) error {
	pkgs := ctx.Cfg.Packages
	if len(pkgs) == 0 {
		ui.Warn("no packages in config — skipping")
		return nil
	}
	args := append([]string{"-S", "--needed", "--noconfirm"}, pkgs...)
	return ctx.R.Root("pacman", args...)
}
