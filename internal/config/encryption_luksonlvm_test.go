package config

import (
	"strings"
	"testing"
)

// TestEncryptionLuksOnLvmRejected pins that disks.encryption.type: luks_on_lvm is
// no longer an accepted value. The archinstall renderer never implemented the
// per-LV luks-on-lvm topology — it collapsed it into lvm_on_luks, encrypting the
// PVs (the wrong layout) silently. Removing it from the oneof set makes Validate()
// surface a clear "must be one of" error instead.
func TestEncryptionLuksOnLvmRejected(t *testing.T) {
	const y = `
system:
  hostname: arch
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel:
  base: [linux]
disks:
  esp:
    device: /dev/nvme0n1
    size: 1GiB
  swap:
    type: swapfile
    size: 4GiB
  layout: lvm
  lvm: {vg: vg0, lv: root, filesystem: ext4, pvs: [/dev/nvme0n1p2]}
  encryption: {type: luks_on_lvm}
`
	err := validateYAML(t, y)
	if err == nil {
		t.Fatalf("want error for luks_on_lvm, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "must be one of") {
		t.Fatalf("error %q does not contain %q", msg, "must be one of")
	}
	// The accepted set must be named so the user knows the valid choices.
	if !strings.Contains(msg, "luks") || !strings.Contains(msg, "lvm_on_luks") {
		t.Fatalf("error %q should list the accepted types (luks, lvm_on_luks)", msg)
	}
}
