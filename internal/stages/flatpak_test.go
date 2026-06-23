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
		// exactly the declared remotes, added verbatim — per-user scope (--user)
		// so no polkit/root is needed (org.freedesktop.Flatpak.modify-repo hang).
		"flatpak --user remote-add --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo",
		"flatpak --user remote-add --if-not-exists flathub-beta https://flathub.org/beta-repo/flathub-beta.flatpakrepo",
		// each app installed from its own named remote (per-app), per-user + noninteractive
		"flatpak --user install -y --noninteractive flathub com.spotify.Client",
		"flatpak --user install -y --noninteractive flathub-beta org.mozilla.firefox",
	)

	joined := strings.Join(plan, "\n")

	// No unconditional/built-in flathub remote-add: the only flathub remote-add
	// is the one the config declared (asserted above). There must be no install
	// that pins every app to flathub regardless of its declared remote.
	if strings.Contains(joined, "flatpak --user install -y --noninteractive flathub org.mozilla.firefox") {
		t.Errorf("firefox should install from flathub-beta, not flathub; plan:\n%s", joined)
	}

	// Every invocation of the flatpak *binary* must stay unprivileged (Cmd, never
	// Root/sudo): a system-scope flatpak op as the user drops into a polkit
	// Password: prompt and bootstrap hangs forever in a headless/TTY session. We
	// match flatpak as the command (after any sudo prefix), not as an argument —
	// `sudo pacman -S … flatpak` (the ensureTool package install, only recorded
	// when flatpak is absent) is legitimately privileged and must not trip this.
	for _, line := range plan {
		cmd := strings.TrimPrefix(line, "sudo ")
		if cmd != line && strings.HasPrefix(cmd, "flatpak ") {
			t.Errorf("flatpak must run unprivileged (no sudo) to avoid a polkit hang; got: %q", line)
		}
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
