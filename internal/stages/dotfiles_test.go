package stages

import (
	"strings"
	"testing"
)

// The dotfiles stage (80) selects a dotfiles manager. These tests assert the
// recorded command plan for each manager. The chezmoi/yadm idempotency probes the
// real $HOME via os.Stat; in tests that path won't exist, so the init/clone branch
// is what we assert (we don't depend on the apply/pull branch).

// chezmoiInitialized reports whether the test host already has chezmoi initialized;
// when it does, the stage takes the `chezmoi apply` branch instead of init, so the
// init-with-repo assertions are skipped (host-independent, like the AUR/KDE tests).
func chezmoiInitialized() bool { return homeHas(".local/share/chezmoi/.git") }

func TestDotfiles_ChezmoiDefault(t *testing.T) {
	// Unset manager defaults to chezmoi; the repo comes from dotfiles.repo.
	plan := planForCfg(t, Bootstrap, "dotfiles", `
dotfiles:
  repo: https://github.com/example/dotfiles
`)
	if chezmoiInitialized() {
		mustContain(t, plan, "chezmoi apply")
		return
	}
	mustContain(t, plan, "chezmoi init --apply https://github.com/example/dotfiles")
}

func TestDotfiles_ChezmoiExplicit(t *testing.T) {
	// Explicit manager + repo.
	plan := planForCfg(t, Bootstrap, "dotfiles", `
dotfiles:
  manager: chezmoi
  repo: https://github.com/example/df-explicit
`)
	if chezmoiInitialized() {
		mustContain(t, plan, "chezmoi apply")
		return
	}
	mustContain(t, plan, "chezmoi init --apply https://github.com/example/df-explicit")
}

func TestDotfiles_Yadm(t *testing.T) {
	// On a host that already has yadm cloned the stage takes the `yadm pull` branch;
	// skip the clone assertion there (host-independent).
	if homeHas(".local/share/yadm/repo.git") {
		t.Skip("yadm already cloned on this host; clone branch not exercised")
	}
	plan := planForCfg(t, Bootstrap, "dotfiles", `
dotfiles:
  manager: yadm
  repo: https://github.com/example/dotfiles
`)
	mustContain(t, plan, "yadm clone https://github.com/example/dotfiles")
}

func TestDotfiles_BareGit(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "dotfiles", `
dotfiles:
  manager: bare-git
  repo: https://github.com/example/dotfiles
`)
	j := strings.Join(plan, "\n")
	if !strings.Contains(j, `git clone --bare https://github.com/example/dotfiles "$HOME/.dotfiles"`) {
		t.Errorf("bare-git plan should clone the bare repo, got:\n%s", j)
	}
	if !strings.Contains(j, `git --git-dir="$HOME/.dotfiles" --work-tree="$HOME" checkout -f`) {
		t.Errorf("bare-git plan should checkout the work-tree, got:\n%s", j)
	}
}

func TestDotfiles_NoneIsNoOp(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "dotfiles", `
dotfiles:
  manager: none
  repo: https://github.com/example/dotfiles
`)
	if len(plan) != 0 {
		t.Errorf("manager: none should record no commands, got plan:\n%s", strings.Join(plan, "\n"))
	}
}

func TestDotfiles_UnsetRepoSkips(t *testing.T) {
	// A non-none manager with no repo is a clean skip.
	plan := planForCfg(t, Bootstrap, "dotfiles", `
dotfiles:
  manager: chezmoi
`)
	if len(plan) != 0 {
		t.Errorf("unset repo should skip with no commands, got plan:\n%s", strings.Join(plan, "\n"))
	}
}
