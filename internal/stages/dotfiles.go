package stages

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/AdamJHall/archwright/internal/ui"
)

// dotfiles is the Phase B 80-dotfiles stage: install the configured dotfiles
// manager and apply the dotfiles. Final Phase B step before setup.
//
// The manager is selectable (chezmoi, yadm, bare-git, none); chezmoi is the
// default and preserves the historical behaviour. The repo defaults to
// chezmoi.repo when dotfiles.repo is unset, so pre-existing configs keep working.
type dotfiles struct{}

func init() { register(dotfiles{}) }

func (dotfiles) Order() int   { return 80 }
func (dotfiles) Name() string { return "dotfiles" }
func (dotfiles) Phase() Phase { return Bootstrap }

// effectiveManager returns dotfiles.manager, defaulting to "chezmoi" when empty.
func (dotfiles) effectiveManager(ctx *Context) string {
	if m := ctx.Cfg.Dotfiles.Manager; m != "" {
		return m
	}
	return "chezmoi"
}

// effectiveRepo returns dotfiles.repo, falling back to the legacy chezmoi.repo.
func (dotfiles) effectiveRepo(ctx *Context) string {
	if r := ctx.Cfg.Dotfiles.Repo; r != "" {
		return r
	}
	return ctx.Cfg.Chezmoi.Repo
}

func (d dotfiles) Run(ctx *Context) error {
	manager := d.effectiveManager(ctx)
	if manager == "none" {
		ui.Warn("dotfiles.manager is none — skipping dotfiles")
		return nil
	}

	repo := d.effectiveRepo(ctx)
	if repo == "" {
		ui.Warn("no dotfiles repo configured — skipping")
		return nil
	}

	switch manager {
	case "chezmoi":
		if err := d.runChezmoi(ctx, repo); err != nil {
			return err
		}
	case "yadm":
		if err := d.runYadm(ctx, repo); err != nil {
			return err
		}
	case "bare-git":
		if err := d.runBareGit(ctx, repo); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown dotfiles manager %q", manager)
	}

	ui.OK("dotfiles applied")
	return nil
}

// runChezmoi preserves the historical behaviour exactly: install chezmoi, then
// `chezmoi apply` if already initialized, else `chezmoi init --apply <repo>`.
func (dotfiles) runChezmoi(ctx *Context, repo string) error {
	if err := ensureTool(ctx, "chezmoi", "chezmoi"); err != nil {
		return err
	}

	// Already initialized? Apply. Otherwise init from the repo.
	if homeHas(".local/share/chezmoi/.git") {
		return ctx.R.Cmd("chezmoi", "apply")
	}
	return ctx.R.Cmd("chezmoi", "init", "--apply", repo)
}

// runYadm installs yadm and either pulls (best-effort) an existing clone or
// clones the repo. Idempotency probes yadm's repo.git directory.
func (dotfiles) runYadm(ctx *Context, repo string) error {
	if err := ensureTool(ctx, "yadm", "yadm"); err != nil {
		return err
	}

	if homeHas(".local/share/yadm/repo.git") {
		ctx.R.Try("yadm", "pull") // best-effort: a clean tree may have nothing to pull
		return nil
	}
	return ctx.R.Cmd("yadm", "clone", repo)
}

// runBareGit implements the classic bare-repo-in-$HOME pattern: a bare repo at
// ~/.dotfiles with the work-tree set to $HOME. Idempotent — the clone is skipped
// when the bare dir already exists, then the work-tree is (re)checked out.
func (dotfiles) runBareGit(ctx *Context, repo string) error {
	return ctx.R.Shell(fmt.Sprintf(
		`{ [ -d "$HOME/.dotfiles" ] || git clone --bare %s "$HOME/.dotfiles"; } && `+
			`git --git-dir="$HOME/.dotfiles" --work-tree="$HOME" checkout -f`,
		repo))
}

// homeHas reports whether the given path under the user's home directory exists.
// It is a read-only probe (not recorded in the plan); under dry-run the path
// usually won't exist, so the init/clone branch is what the plan shows.
func homeHas(rel string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, rel))
	return err == nil
}
