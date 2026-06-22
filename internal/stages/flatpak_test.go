package stages

import (
	"strings"
	"testing"
)

// The flatpak stage (30) registers exactly the declared remotes (nothing
// implicit) and installs each app from its named remote, parsed from the
// "remote:appid" reference. These assert the recorded dry-run plan.

func TestFlatpak_RemotesAndPerAppInstall(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "flatpak", `
flatpak_remotes:
  - name: flathub
    url: https://flathub.org/repo/flathub.flatpakrepo
  - name: flathub-beta
    url: https://flathub.org/beta-repo/flathub-beta.flatpakrepo
flatpaks:
  - flathub:com.spotify.Client
  - flathub-beta:org.mozilla.firefox
`)
	mustContain(t, plan,
		// exactly the declared remotes, added verbatim
		"flatpak remote-add --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo",
		"flatpak remote-add --if-not-exists flathub-beta https://flathub.org/beta-repo/flathub-beta.flatpakrepo",
		// each app installed from its own named remote (per-app)
		"flatpak install -y --noninteractive flathub com.spotify.Client",
		"flatpak install -y --noninteractive flathub-beta org.mozilla.firefox",
	)

	// No unconditional/built-in flathub remote-add: the only flathub remote-add
	// is the one the config declared (asserted above). There must be no install
	// that pins every app to flathub regardless of its declared remote.
	joined := strings.Join(plan, "\n")
	if strings.Contains(joined, "flatpak install -y --noninteractive flathub org.mozilla.firefox") {
		t.Errorf("firefox should install from flathub-beta, not flathub; plan:\n%s", joined)
	}
}

func TestFlatpak_NoRemotesNoImplicitFlathub(t *testing.T) {
	// With no remotes and no apps the stage skips entirely — and in particular
	// never adds a built-in flathub remote.
	plan := planForCfg(t, Bootstrap, "flatpak", "{}\n")
	if joined := strings.Join(plan, "\n"); strings.Contains(joined, "remote-add") {
		t.Errorf("no flatpak remote should be added when none are declared; plan:\n%s", joined)
	}
}
