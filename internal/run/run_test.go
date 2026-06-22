package run

import (
	"reflect"
	"testing"
)

// These tests pin the dry-run recording seam: in dry-run every state-changing
// helper must populate .Plan with the exact line it would have executed, and must
// NOT actually run anything. (If they executed, these commands — sudo, bash -c
// "false" — would fail or mutate the host; their passing proves dry-run is honoured.)

func TestCmd_RecordsInDryRun(t *testing.T) {
	r := &Runner{DryRun: true}
	if err := r.Cmd("pacman", "-S", "--noconfirm", "git"); err != nil {
		t.Fatalf("dry-run Cmd should not error: %v", err)
	}
	want := []string{"pacman -S --noconfirm git"}
	if !reflect.DeepEqual(r.Plan, want) {
		t.Errorf("Plan = %v, want %v", r.Plan, want)
	}
}

func TestShell_RecordsInDryRun(t *testing.T) {
	r := &Runner{DryRun: true}
	// A script that would fail if executed; dry-run must not run it.
	if err := r.Shell("false && echo nope"); err != nil {
		t.Fatalf("dry-run Shell should not error: %v", err)
	}
	want := []string{"sh: false && echo nope"}
	if !reflect.DeepEqual(r.Plan, want) {
		t.Errorf("Plan = %v, want %v", r.Plan, want)
	}
}

func TestRoot_PrefixesSudoWhenSudoTrue(t *testing.T) {
	r := &Runner{DryRun: true, Sudo: true}
	if err := r.Root("systemctl", "enable", "foo.timer"); err != nil {
		t.Fatalf("dry-run Root should not error: %v", err)
	}
	want := []string{"sudo systemctl enable foo.timer"}
	if !reflect.DeepEqual(r.Plan, want) {
		t.Errorf("Plan = %v, want %v", r.Plan, want)
	}
}

func TestRoot_NoSudoWhenSudoFalse(t *testing.T) {
	r := &Runner{DryRun: true, Sudo: false}
	if err := r.Root("systemctl", "enable", "foo.timer"); err != nil {
		t.Fatalf("dry-run Root should not error: %v", err)
	}
	// Phase A is already root: the recorded line has no sudo prefix.
	want := []string{"systemctl enable foo.timer"}
	if !reflect.DeepEqual(r.Plan, want) {
		t.Errorf("Plan = %v, want %v", r.Plan, want)
	}
}

func TestRootShell_RecordsSameLineRegardlessOfSudo(t *testing.T) {
	// RootShell records the plain `sh: <script>` line in both privilege modes; the
	// sudo/no-sudo distinction is an execution detail, not a recorded one.
	for _, sudo := range []bool{true, false} {
		r := &Runner{DryRun: true, Sudo: sudo}
		if err := r.RootShell("snapper -c root create-config /"); err != nil {
			t.Fatalf("dry-run RootShell (sudo=%v) should not error: %v", sudo, err)
		}
		want := []string{"sh: snapper -c root create-config /"}
		if !reflect.DeepEqual(r.Plan, want) {
			t.Errorf("sudo=%v: Plan = %v, want %v", sudo, r.Plan, want)
		}
	}
}

func TestTry_RecordsAndIgnoresError(t *testing.T) {
	r := &Runner{DryRun: true}
	// Try has no return value; it must still record the command in dry-run.
	r.Try("udevadm", "settle")
	want := []string{"udevadm settle"}
	if !reflect.DeepEqual(r.Plan, want) {
		t.Errorf("Plan = %v, want %v", r.Plan, want)
	}
}

func TestTryRoot_RecordsWithSudoPrefix(t *testing.T) {
	r := &Runner{DryRun: true, Sudo: true}
	r.TryRoot("snapper", "-c", "root", "set-config", "NUMBER_LIMIT=50")
	want := []string{"sudo snapper -c root set-config NUMBER_LIMIT=50"}
	if !reflect.DeepEqual(r.Plan, want) {
		t.Errorf("Plan = %v, want %v", r.Plan, want)
	}
}

func TestTryRoot_NoSudoWhenSudoFalse(t *testing.T) {
	r := &Runner{DryRun: true, Sudo: false}
	r.TryRoot("snapper", "-c", "root", "set-config", "NUMBER_LIMIT=50")
	want := []string{"snapper -c root set-config NUMBER_LIMIT=50"}
	if !reflect.DeepEqual(r.Plan, want) {
		t.Errorf("Plan = %v, want %v", r.Plan, want)
	}
}

func TestChroot_RecordsArchChrootPrefix(t *testing.T) {
	r := &Runner{DryRun: true}
	if err := r.Chroot("/mnt", "pacman", "-Syu"); err != nil {
		t.Fatalf("dry-run Chroot should not error: %v", err)
	}
	want := []string{"arch-chroot /mnt pacman -Syu"}
	if !reflect.DeepEqual(r.Plan, want) {
		t.Errorf("Plan = %v, want %v", r.Plan, want)
	}
}
