package stages

import (
	"os/exec"

	"github.com/AdamJHall/archwright/internal/ui"
)

// yay is the Phase B 10-yay equivalent: install the yay AUR helper (yay-bin, no
// Go compile). makepkg must not run as root, so this uses Cmd/Shell as the user.
type yay struct{}

func init() { register(yay{}) }

func (yay) Order() int   { return 10 }
func (yay) Name() string { return "yay" }
func (yay) Phase() Phase { return Bootstrap }

func (yay) Run(ctx *Context) error {
	if _, err := exec.LookPath("yay"); err == nil {
		ui.OK("yay already installed")
		return nil
	}
	if err := ctx.R.Root("pacman", "-S", "--needed", "--noconfirm", "git", "base-devel"); err != nil {
		return err
	}
	// Clone + build + clean in one shell so the temp dir and cwd are handled.
	if err := ctx.R.Shell(
		`tmp="$(mktemp -d)" && git clone https://aur.archlinux.org/yay-bin.git "$tmp" && ` +
			`(cd "$tmp" && makepkg -si --noconfirm) && rm -rf "$tmp"`,
	); err != nil {
		return err
	}
	ui.OK("yay installed")
	return nil
}
