package config

import (
	"strings"
	"testing"
)

// TestValidate_LVMVolumes exercises the lvmVolumeErrors rules: single-LV vs
// multi-volume mode are mutually exclusive, and multi-volume mode needs exactly
// one rest (size-less) volume and exactly one volume mounted at "/".
func TestValidate_LVMVolumes(t *testing.T) {
	cases := []struct {
		name    string
		disks   string
		wantErr []string // substrings; empty means must be valid
	}{
		{
			name: "single root LV is valid",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`,
		},
		{
			name: "multi-volume root+home is valid",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: root, mountpoint: /, filesystem: xfs, size: 64GiB}
      - {name: home, mountpoint: /home, filesystem: ext4}
`,
		},
		{
			name: "both single and volumes set is rejected",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    lv: root
    filesystem: xfs
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: home, mountpoint: /home, filesystem: ext4}
`,
			wantErr: []string{"must set either lv+filesystem (single root LV) or volumes, not both"},
		},
		{
			name: "neither single nor volumes set is rejected",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm: {vg: vg0, pvs: [/dev/nvme0n1p2]}
`,
			wantErr: []string{"must set either lv+filesystem (single root LV) or volumes"},
		},
		{
			name: "two size-less volumes rejected",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: root, mountpoint: /, filesystem: xfs}
      - {name: home, mountpoint: /home, filesystem: ext4}
`,
			wantErr: []string{"exactly one volume without a size"},
		},
		{
			name: "no size-less volume rejected",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: root, mountpoint: /, filesystem: xfs, size: 64GiB}
      - {name: home, mountpoint: /home, filesystem: ext4, size: 32GiB}
`,
			wantErr: []string{"exactly one volume without a size"},
		},
		{
			name: "no root mountpoint rejected",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: data, mountpoint: /data, filesystem: xfs, size: 64GiB}
      - {name: home, mountpoint: /home, filesystem: ext4}
`,
			wantErr: []string{"exactly one volume mounted at / (the root)"},
		},
		{
			name: "two root mountpoints rejected",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: root, mountpoint: /, filesystem: xfs, size: 64GiB}
      - {name: other, mountpoint: /, filesystem: ext4}
`,
			wantErr: []string{"exactly one volume mounted at / (the root)"},
		},
		{
			name: "volume missing required name/filesystem",
			disks: `
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {mountpoint: /, size: 64GiB}
      - {name: home, mountpoint: /home, filesystem: ext4}
`,
			wantErr: []string{"disks.lvm.volumes[0].name is required", "disks.lvm.volumes[0].filesystem is required"},
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
