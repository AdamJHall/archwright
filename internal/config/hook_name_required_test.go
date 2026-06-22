package config

import (
	"strings"
	"testing"
)

// TestHookNameRequired pins that every hook must carry a name. A nameless hook
// would silently flip the layered-config merge of the hooks list from
// merge-by-name to wholesale replace (see internal/configsrc/merge.go), dropping
// the base layer's hooks. Requiring a name fails validation instead, and the
// failure is reported against the indexed YAML path hooks[N].name.
func TestHookNameRequired(t *testing.T) {
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
hooks:
  - at: post-install
    run: echo hi
`
	err := validateYAML(t, y)
	if err == nil {
		t.Fatalf("want error for nameless hook, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "hooks[0].name") {
		t.Fatalf("error %q should reference hooks[0].name", msg)
	}
	if !strings.Contains(msg, "is required") {
		t.Fatalf("error %q should say the field is required", msg)
	}
}

// TestHookWithNameValid confirms a named hook still validates cleanly, so the new
// requirement doesn't break correctly-written hooks.
func TestHookWithNameValid(t *testing.T) {
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
hooks:
  - name: greet
    at: post-install
    run: echo hi
`
	if err := validateYAML(t, y); err != nil {
		t.Fatalf("want valid, got error: %v", err)
	}
}
