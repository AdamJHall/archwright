package stages

import (
	"os"
	"path/filepath"

	"github.com/AdamJHall/archwright/internal/ui"
)

// chezmoi is the Phase B 80-chezmoi equivalent: install chezmoi and pull the
// dotfiles. Final Phase B step.
type chezmoi struct{}

func init() { register(chezmoi{}) }

func (chezmoi) Order() int   { return 80 }
func (chezmoi) Name() string { return "chezmoi" }
func (chezmoi) Phase() Phase { return Bootstrap }

func (chezmoi) Run(ctx *Context) error {
	repo := ctx.Cfg.Chezmoi.Repo
	if repo == "" {
		ui.Warn("no chezmoi.repo configured — skipping dotfiles")
		return nil
	}

	if err := ensureTool(ctx, "chezmoi", "chezmoi"); err != nil {
		return err
	}

	// Already initialized? Apply. Otherwise init from the repo.
	initialized := false
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".local/share/chezmoi/.git")); err == nil {
			initialized = true
		}
	}
	if initialized {
		if err := ctx.R.Cmd("chezmoi", "apply"); err != nil {
			return err
		}
	} else {
		if err := ctx.R.Cmd("chezmoi", "init", "--apply", repo); err != nil {
			return err
		}
	}

	ui.OK("dotfiles applied")
	return nil
}
