package stages

import "testing"

// TestPlan_PlymouthBootConfig proves the C3 refactor is behaviour-preserving: the
// plymouth stage still emits the kernel-param sed (now via the shared, bootloader-
// aware ensureKernelParam helper) and the GRUB regeneration (now via
// regenerateBootConfig) into the recorded .Plan. Self-contained config so it does
// not depend on the shared testYAML fixture.
func TestPlan_PlymouthBootConfig(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "plymouth", `
plymouth:
  theme: bgrt
grub:
  cmdline_extra: "quiet splash"
`)
	mustContain(t, plan,
		// each cmdline token appended to GRUB_CMDLINE_LINUX_DEFAULT, idempotently
		`grep -qE 'GRUB_CMDLINE_LINUX_DEFAULT="[^"]*\bquiet\b' /etc/default/grub || `+
			`sudo sed -i -E 's/(GRUB_CMDLINE_LINUX_DEFAULT=")/\1quiet /' /etc/default/grub`,
		`grep -qE 'GRUB_CMDLINE_LINUX_DEFAULT="[^"]*\bsplash\b' /etc/default/grub || `+
			`sudo sed -i -E 's/(GRUB_CMDLINE_LINUX_DEFAULT=")/\1splash /' /etc/default/grub`,
		// bootloader config regenerated after the cmdline/theme change
		"grub-mkconfig -o /boot/grub/grub.cfg",
	)
}
