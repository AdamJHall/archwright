package stages

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/run"
	"gopkg.in/yaml.v3"
)

// planForCfg runs a single stage in dry-run against a caller-supplied config and
// returns the recorded command plan. Self-contained so these tests don't depend
// on the shared testYAML.
func planForCfg(t *testing.T, phase Phase, name, yamlBody string) []string {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(yamlBody), &c); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	ss := For(phase, name)
	if len(ss) != 1 {
		t.Fatalf("expected exactly one stage %q in phase %d, got %d", name, phase, len(ss))
	}
	r := &run.Runner{DryRun: true, Sudo: phase == Bootstrap}
	ctx := &Context{Cfg: &c, R: r, AssumeYes: true, ConfigPath: "/tmp/config.yaml"}
	if err := ss[0].Run(ctx); err != nil {
		t.Fatalf("stage %s returned error in dry-run: %v", name, err)
	}
	return r.Plan
}

func TestSelector_KDEGating(t *testing.T) {
	// A non-kde environment makes the stage a clean no-op: nothing is written.
	plan := planForCfg(t, Bootstrap, "kde", `
desktop:
  environment: gnome
kde:
  look_and_feel: org.kde.breezedark.desktop
`)
	if joined := strings.Join(plan, "\n"); strings.Contains(joined, "kwriteconfig6") {
		t.Errorf("kde stage should be a no-op when environment=gnome, got plan:\n%s", joined)
	}

	// Empty environment preserves today's behavior: the stage runs and selects the
	// global theme. To prove the gate is keyed on the value, an explicit "kde" must
	// behave identically to empty.
	for _, env := range []string{"", "kde"} {
		body := "kde:\n  look_and_feel: org.kde.breezedark.desktop\n"
		if env != "" {
			body = "desktop:\n  environment: " + env + "\n" + body
		}
		plan := planForCfg(t, Bootstrap, "kde", body)
		mustContain(t, plan, "kwriteconfig6 --file kdeglobals --group KDE --key LookAndFeelPackage org.kde.breezedark.desktop")
	}
}

func TestSelector_AurHelper(t *testing.T) {
	// The yay stage early-returns (empty plan) when the helper is already installed.
	// Skip its plan assertions if the relevant binary is present on the test host;
	// the aur stage is host-independent and is always asserted.
	installed := func(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

	// paru: yay-stage references paru/paru-bin, aur-stage calls paru.
	if !installed("paru") {
		yayPlan := planForCfg(t, Bootstrap, "yay", "aur_helper: paru\n")
		mustContain(t, yayPlan, "paru-bin")
		if j := strings.Join(yayPlan, "\n"); strings.Contains(j, "yay-bin") {
			t.Errorf("yay stage should not reference yay-bin under aur_helper: paru:\n%s", j)
		}
	}
	aurPlan := planForCfg(t, Bootstrap, "aur", "aur_helper: paru\naur: [1password]\n")
	mustContain(t, aurPlan, "paru -S --needed --noconfirm 1password")

	// unset: default yay/yay-bin, aur-stage calls yay.
	if !installed("yay") {
		yayPlanDefault := planForCfg(t, Bootstrap, "yay", "{}\n")
		mustContain(t, yayPlanDefault, "yay-bin")
		if j := strings.Join(yayPlanDefault, "\n"); strings.Contains(j, "paru") {
			t.Errorf("default yay stage should not reference paru:\n%s", j)
		}
	}
	aurPlanDefault := planForCfg(t, Bootstrap, "aur", "aur: [1password]\n")
	mustContain(t, aurPlanDefault, "yay -S --needed --noconfirm 1password")
}

func TestSelector_FlatpakRemotes(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "flatpak", `
flatpak_remotes:
  - name: flathub
    url: https://flathub.org/repo/flathub.flatpakrepo
  - name: flathub-beta
    url: https://flathub.org/beta-repo/flathub-beta.flatpakrepo
flatpaks: [flathub-beta:com.spotify.Client]
`)
	mustContain(t, plan,
		// exactly the declared remotes — no remote is implicit
		"flatpak --user remote-add --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo",
		"flatpak --user remote-add --if-not-exists flathub-beta https://flathub.org/beta-repo/flathub-beta.flatpakrepo",
		// the app installs from its named remote
		"flatpak --user install -y --noninteractive flathub-beta com.spotify.Client",
	)
}
