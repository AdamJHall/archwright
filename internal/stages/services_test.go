package stages

import (
	"strings"
	"testing"
)

// The services stage (90) enables system units as root and user units with
// `systemctl --user`, so they start on the next boot. These assert the recorded
// dry-run plan against an inline config.

func TestServices_SystemAndUser(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "services", `
services:
  enable:
    - plasmalogin.service
    - bluetooth.service
  user:
    - syncthing.service
`)
	mustContain(t, plan,
		// system units enabled in one privileged systemctl call (Sudo in Phase B)
		"sudo systemctl enable plasmalogin.service bluetooth.service",
		// user units enabled unprivileged via --user
		"systemctl --user enable syncthing.service",
	)
}

func TestServices_EmptySkips(t *testing.T) {
	// No services block: the stage is a clean no-op — nothing planned.
	plan := planForCfg(t, Bootstrap, "services", "{}\n")
	if joined := strings.Join(plan, "\n"); strings.Contains(joined, "systemctl") {
		t.Errorf("no systemctl call expected when services is unset; plan:\n%s", joined)
	}
}

func TestServices_SystemOnly(t *testing.T) {
	// Only system units set: no stray `systemctl --user` call.
	plan := planForCfg(t, Bootstrap, "services", `
services:
  enable: [docker.service]
`)
	mustContain(t, plan, "sudo systemctl enable docker.service")
	if joined := strings.Join(plan, "\n"); strings.Contains(joined, "--user") {
		t.Errorf("no --user call expected when only system units set; plan:\n%s", joined)
	}
}
