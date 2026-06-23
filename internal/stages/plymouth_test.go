package stages

import "testing"

// Regression coverage for docs/bugs/plymouth-bootctl-update-fails-systemd-boot.md:
// the plymouth stage must (a) be a clean no-op when no plymouth theme is configured
// and (b) refresh systemd-boot via the graceful, best-effort `bootctl update
// --graceful` rather than a plain `bootctl update` that aborts bootstrap when the
// loader is already current. The grub path stays byte-identical. Self-contained
// inline configs — no shared fixtures touched.

// With no plymouth: block the stage installs nothing and never touches the
// bootloader, even on a systemd-boot config (the default-config case that broke
// every systemd-boot bootstrap).
func TestPlan_PlymouthUnconfiguredIsNoop(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "plymouth", `
bootloader:
  kind: systemd-boot
`)
	if len(plan) != 0 {
		t.Errorf("plymouth stage should be a no-op when unconfigured, got plan:\n%v", plan)
	}
	mustNotContain(t, plan,
		"plymouth",
		"bootctl",
		"grub-mkconfig",
		"/etc/kernel/cmdline",
		"plymouth-set-default-theme",
	)
}

// A configured theme on systemd-boot regenerates boot config via the graceful,
// best-effort invocation and never the plain failing form.
func TestPlan_PlymouthSystemdBootGraceful(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "plymouth", `
bootloader:
  kind: systemd-boot
plymouth:
  theme: bgrt
`)
	// best-effort step still goes through Root semantics (sudo in Phase B).
	mustContain(t, plan,
		"sudo bootctl update --graceful",
	)
	// the plain, non-graceful form (which exits non-zero when already current and
	// aborted bootstrap) must be gone, as must any grub regeneration.
	if hasExact(plan, "sudo bootctl update") || hasExact(plan, "bootctl update") {
		t.Errorf("plan must not contain a plain `bootctl update`; plan:\n%v", plan)
	}
	mustNotContain(t, plan, "grub-mkconfig -o /boot/grub/grub.cfg")
}

// A configured theme on a grub config regenerates grub.cfg unchanged.
func TestPlan_PlymouthGrubUnchanged(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "plymouth", `
plymouth:
  theme: bgrt
`)
	mustContain(t, plan, "grub-mkconfig -o /boot/grub/grub.cfg")
	mustNotContain(t, plan, "bootctl")
}

// hasExact reports whether any recorded plan line equals line exactly (as opposed
// to mustContain's substring match, which `bootctl update --graceful` would
// satisfy for the substring `bootctl update`).
func hasExact(plan []string, line string) bool {
	for _, p := range plan {
		if p == line {
			return true
		}
	}
	return false
}
