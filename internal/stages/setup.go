package stages

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/ui"
)

// setup is the Phase B 85-setup stage: run the ordered list of setup steps that
// install what a dotfiles repo references but can't vendor itself (oh-my-zsh and
// its custom plugins, tmux's TPM, theme repos). Each step is a git clone or a
// shell command; they execute in the order written. It runs after chezmoi (80)
// so the dotfiles dirs the clones target already exist.
type setup struct{}

func init() { register(setup{}) }

func (setup) Order() int   { return 85 }
func (setup) Name() string { return "setup" }
func (setup) Phase() Phase { return Bootstrap }

func (setup) Run(ctx *Context) error {
	steps := ctx.Cfg.Setup.Steps
	if len(steps) == 0 {
		ui.Warn("no setup steps in config — skipping")
		return nil
	}

	for _, s := range steps {
		if s.Clone != nil {
			if err := cloneRepo(ctx, *s.Clone); err != nil {
				return err
			}
			continue
		}
		if err := ctx.R.Shell(s.Command); err != nil {
			return err
		}
	}

	ui.OK("setup complete")
	return nil
}

// cloneRepo clones c into its destination, idempotently: an existing dest is left
// alone (or `git pull`ed when c.Update is set) so the stage is safe to re-run.
func cloneRepo(ctx *Context, c config.Clone) error {
	dest := expandHome(c.Dest)

	// Idempotency depends on host state, which doesn't exist under --dry-run; there
	// we always plan the clone so the printed plan shows the intended action.
	if !ctx.R.DryRun {
		if _, err := os.Stat(dest); err == nil {
			if c.Update {
				return ctx.R.Cmd("git", "-C", dest, "pull", "--ff-only")
			}
			ui.Info("clone target already present — skipping", "dest", dest)
			return nil
		}
	}

	args := []string{"clone", "--depth", "1"}
	if c.Ref != "" {
		args = append(args, "--branch", c.Ref)
	}
	args = append(args, c.URL, dest)
	return ctx.R.Cmd("git", args...)
}

// expandHome turns a leading ~ into the user's home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
