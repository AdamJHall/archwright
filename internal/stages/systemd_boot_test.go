package stages

import "testing"

// TestPlan_PlymouthSystemdBoot proves the A4 systemd-boot branch: with
// bootloader.kind: systemd-boot, the plymouth stage's ensureKernelParam edits
// /etc/kernel/cmdline (not /etc/default/grub) and regenerateBootConfig runs
// `bootctl update` (not grub-mkconfig). Self-contained inline config.
func TestPlan_PlymouthSystemdBoot(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "plymouth", `
bootloader:
  kind: systemd-boot
plymouth:
  theme: bgrt
grub:
  cmdline_extra: "quiet splash"
`)
	mustContain(t, plan,
		// each cmdline token appended to /etc/kernel/cmdline, idempotently. The
		// whole pipeline runs as root via RootShell, so no inner per-command sudo.
		`grep -qE '\bquiet\b' /etc/kernel/cmdline 2>/dev/null || `+
			`{ touch /etc/kernel/cmdline && `+
			`sed -i -E '$ s/$/ quiet/' /etc/kernel/cmdline; }`,
		`grep -qE '\bsplash\b' /etc/kernel/cmdline 2>/dev/null || `+
			`{ touch /etc/kernel/cmdline && `+
			`sed -i -E '$ s/$/ splash/' /etc/kernel/cmdline; }`,
		// systemd-boot refresh, no grub.cfg regeneration
		"bootctl update",
	)
	mustNotContain(t, plan,
		"grub-mkconfig -o /boot/grub/grub.cfg",
		`GRUB_CMDLINE_LINUX_DEFAULT`,
	)
}

// TestPlan_PlymouthGrubDefault proves the default (empty bootloader) path still
// emits the GRUB sed + grub-mkconfig, byte-identical to today.
func TestPlan_PlymouthGrubDefault(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "plymouth", `
plymouth:
  theme: bgrt
grub:
  cmdline_extra: "quiet splash"
`)
	mustContain(t, plan,
		`grep -qE 'GRUB_CMDLINE_LINUX_DEFAULT="[^"]*\bquiet\b' /etc/default/grub || `+
			`sed -i -E 's/(GRUB_CMDLINE_LINUX_DEFAULT=")/\1quiet /' /etc/default/grub`,
		`grep -qE 'GRUB_CMDLINE_LINUX_DEFAULT="[^"]*\bsplash\b' /etc/default/grub || `+
			`sed -i -E 's/(GRUB_CMDLINE_LINUX_DEFAULT=")/\1splash /' /etc/default/grub`,
		"grub-mkconfig -o /boot/grub/grub.cfg",
	)
	mustNotContain(t, plan,
		"bootctl update",
		"/etc/kernel/cmdline",
	)
}
