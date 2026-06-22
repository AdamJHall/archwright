package stages

import (
	"strings"
	"testing"
)

// The snapper stage (25) provisions Snapper for btrfs roots. It is a clean no-op
// unless the effective layout is btrfs AND btrfs.snapshots == "snapper". These
// tests assert the recorded command plan in dry-run. snapper is not on the CI
// PATH, so ensureTool records the pacman install branch (host-independent).

func TestSnapper_ActivePlan(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "snapper", `
disks:
  layout: btrfs
  btrfs:
    device: /dev/sda
    snapshots: snapper
`)
	j := strings.Join(plan, "\n")

	// snap-pac is always installed (it has no binary, so it goes through Root).
	mustContain(t, plan, "sudo pacman -S --needed --noconfirm snap-pac")
	// Root config creation runs as root via RootShell (recorded as a plain
	// `sh: ...` line, no inner sudo) and is grep-guarded so a re-run with an
	// existing config does not fail.
	if !strings.Contains(j, "sh: snapper list-configs 2>/dev/null | grep -qw root || snapper -c root create-config /") {
		t.Errorf("plan should create the root snapper config via RootShell, got:\n%s", j)
	}
	// set-config runs best-effort as root (TryRoot) — recorded with the sudo
	// prefix in Phase B.
	mustContain(t, plan, "sudo snapper -c root set-config")
	// Timers are enabled.
	mustContain(t,
		plan,
		"sudo systemctl enable --now snapper-timeline.timer",
		"sudo systemctl enable --now snapper-cleanup.timer",
	)
}

func TestSnapper_SnapshotsNoneIsNoOp(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "snapper", `
disks:
  layout: btrfs
  btrfs:
    device: /dev/sda
    snapshots: none
`)
	if len(plan) != 0 {
		t.Errorf("snapshots: none should record no commands, got plan:\n%s", strings.Join(plan, "\n"))
	}
}

func TestSnapper_SnapshotsUnsetIsNoOp(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "snapper", `
disks:
  layout: btrfs
  btrfs:
    device: /dev/sda
`)
	if len(plan) != 0 {
		t.Errorf("unset snapshots should record no commands, got plan:\n%s", strings.Join(plan, "\n"))
	}
}

func TestSnapper_NonBtrfsLayoutIsNoOp(t *testing.T) {
	// Even with snapshots set, a non-btrfs layout must skip (the field is inert).
	plan := planForCfg(t, Bootstrap, "snapper", `
disks:
  layout: lvm
  btrfs:
    device: /dev/sda
    snapshots: snapper
`)
	if len(plan) != 0 {
		t.Errorf("non-btrfs layout should record no commands, got plan:\n%s", strings.Join(plan, "\n"))
	}
}

func TestSnapper_NilBtrfsDoesNotPanic(t *testing.T) {
	// btrfs layout selected but no btrfs block: must not panic, must skip cleanly.
	plan := planForCfg(t, Bootstrap, "snapper", `
disks:
  layout: btrfs
`)
	if len(plan) != 0 {
		t.Errorf("nil btrfs block should record no commands, got plan:\n%s", strings.Join(plan, "\n"))
	}
}
