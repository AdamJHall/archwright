package config

import (
	"strings"
	"testing"
)

// baseSystemUser is the minimal valid system+user block the disk-layout fixtures
// build on, kept separate from config_test.go's shared validYAML so these tests
// own their own inputs.
const baseSystemUser = `
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel:
  base: [linux]
`

func TestValidate_DiskLayouts(t *testing.T) {
	cases := []struct {
		name    string
		disks   string
		wantErr []string // substrings; empty means must be valid
	}{
		{
			name: "lvm default (no layout key) is valid",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {size: 8GiB}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`,
		},
		{
			name: "explicit lvm without lvm block",
			disks: `
disks:
  layout: lvm
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {size: 8GiB}
`,
			wantErr: []string{"disks.lvm is required when disks.layout is lvm"},
		},
		{
			name: "plain layout valid",
			disks: `
disks:
  layout: plain
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {size: 8GiB}
  plain: {device: /dev/nvme0n1, filesystem: ext4}
`,
		},
		{
			name: "plain layout without plain block",
			disks: `
disks:
  layout: plain
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {size: 8GiB}
`,
			wantErr: []string{"disks.plain is required when disks.layout is plain"},
		},
		{
			name: "bad layout value",
			disks: `
disks:
  layout: zfs
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {size: 8GiB}
`,
			wantErr: []string{"disks.layout must be one of: lvm btrfs plain"},
		},
		{
			name: "swap partition rejected on lvm",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: partition, size: 8GiB}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`,
			wantErr: []string{"disks.swap.type partition is not supported with the lvm layout"},
		},
		{
			name: "swap partition allowed on plain",
			disks: `
disks:
  layout: plain
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: partition, size: 8GiB}
  plain: {device: /dev/nvme0n1, filesystem: ext4}
`,
		},
		{
			name: "swapfile requires size",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: swapfile}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`,
			wantErr: []string{"disks.swap.size is required when disks.swap.type is swapfile"},
		},
		{
			name: "zram needs no swap size",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`,
		},
		{
			name: "none needs no swap size",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: none}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`,
		},
		{
			name: "bad swap type",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: hibernate, size: 8GiB}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`,
			wantErr: []string{"disks.swap.type must be one of: swapfile zram partition none"},
		},
		{
			name: "btrfs layout valid",
			disks: `
disks:
  layout: btrfs
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  btrfs:
    device: /dev/nvme0n1
    compress: zstd
    snapshots: snapper
    subvolumes:
      - {name: "@", mountpoint: /}
      - {name: "@home", mountpoint: /home}
`,
		},
		{
			name: "btrfs subvolume missing mountpoint",
			disks: `
disks:
  layout: btrfs
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  btrfs:
    device: /dev/nvme0n1
    subvolumes:
      - {name: "@"}
`,
			wantErr: []string{"disks.btrfs.subvolumes[0].mountpoint is required"},
		},
		{
			name: "btrfs layout without btrfs block",
			disks: `
disks:
  layout: btrfs
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
`,
			wantErr: []string{"disks.btrfs is required when disks.layout is btrfs"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := load(t, baseSystemUser+tc.disks).Validate()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("expected valid config, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("missing %q in error:\n%s", w, err)
				}
			}
		})
	}
}
