package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func validateYAML(t *testing.T, y string) error {
	t.Helper()
	var c Config
	if err := yaml.Unmarshal([]byte(y), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return c.Validate()
}

func TestEncryptionValidation(t *testing.T) {
	const base = `
system:
  hostname: arch
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
kernel:
  base: [linux]
disks:
  esp:
    device: /dev/nvme0n1
    size: 1GiB
  swap:
    type: swapfile
    size: 4GiB
`
	cases := []struct {
		name    string
		disks   string
		wantErr string // substring; "" means must validate cleanly
	}{
		{
			name: "lvm_on_luks with lvm ok",
			disks: `
  layout: lvm
  lvm: {vg: vg0, lv: root, filesystem: ext4, pvs: [/dev/nvme0n1p2]}
  encryption: {type: lvm_on_luks}
`,
		},
		{
			name: "lvm_on_luks requires lvm layout",
			disks: `
  layout: plain
  plain: {device: /dev/nvme0n1, filesystem: ext4}
  encryption: {type: lvm_on_luks}
`,
			wantErr: "requires the lvm layout",
		},
		{
			name: "luks requires plain or btrfs",
			disks: `
  layout: lvm
  lvm: {vg: vg0, lv: root, filesystem: ext4, pvs: [/dev/nvme0n1p2]}
  encryption: {type: luks}
`,
			wantErr: "requires the plain or btrfs layout",
		},
		{
			name: "luks on plain ok",
			disks: `
  layout: plain
  plain: {device: /dev/nvme0n1, filesystem: ext4}
  encryption: {type: luks}
`,
		},
		{
			name: "lvm_on_luks rejects >2 pvs",
			disks: `
  layout: lvm
  lvm: {vg: vg0, lv: root, filesystem: ext4, pvs: [/dev/nvme0n1p2, /dev/sda, /dev/sdb]}
  encryption: {type: lvm_on_luks}
`,
			wantErr: "at most 2 PVs",
		},
		{
			name: "bad encryption type",
			disks: `
  layout: lvm
  lvm: {vg: vg0, lv: root, filesystem: ext4, pvs: [/dev/nvme0n1p2]}
  encryption: {type: bogus}
`,
			wantErr: "must be one of",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateYAML(t, base+tc.disks)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
