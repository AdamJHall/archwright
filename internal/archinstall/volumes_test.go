package archinstall

import (
	"fmt"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

// lvmVolumesYAML carves a fixed-size root LV plus a /home LV that takes the
// remainder of the VG.
const lvmVolumesYAML = `
system:
  hostname: arch-lvm
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
disks:
  layout: lvm
  esp:
    device: /dev/nvme0n1
    size: 1GiB
  swap:
    type: zram
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: root, mountpoint: /, filesystem: xfs, size: 64GiB}
      - {name: home, mountpoint: /home, filesystem: ext4}
`

func lvmVolumesConfig(t *testing.T) *config.Config {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(lvmVolumesYAML), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	return &c
}

func TestBuild_LVMMultipleVolumes(t *testing.T) {
	cfg := lvmVolumesConfig(t)
	c, _, err := Build(cfg, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.DiskConfig.LvmConfig == nil {
		t.Fatal("lvm layout must emit lvm_config")
	}
	vols := c.DiskConfig.LvmConfig.VolGroups[0].Volumes
	if len(vols) != 2 {
		t.Fatalf("want 2 volumes, got %d", len(vols))
	}

	root := vols[0]
	if root.Name != "root" || root.FsType != "xfs" || root.Mountpoint == nil || *root.Mountpoint != "/" {
		t.Errorf("root volume wrong: %+v", root)
	}
	wantRoot := roundDownMiB(64 << 30)
	if root.Length.Value != wantRoot {
		t.Errorf("root length = %d, want %d (fixed 64GiB)", root.Length.Value, wantRoot)
	}

	home := vols[1]
	if home.Name != "home" || home.FsType != "ext4" || home.Mountpoint == nil || *home.Mountpoint != "/home" {
		t.Errorf("home volume wrong: %+v", home)
	}

	// The remainder volume (/home) consumes the available VG minus the fixed root.
	disk1Total := uint64(256 << 30)
	pvBytes := roundDownMiB(disk1Total - (startOffset + (1 << 30) + endReserve))
	available := roundDownMiB(pvBytes - vgHeadroomPerPV)
	wantHome := roundDownMiB(available - wantRoot)
	if home.Length.Value != wantHome {
		t.Errorf("home length = %d, want %d (rest of VG)", home.Length.Value, wantHome)
	}
}

// TestBuild_LVMVolumesRootRemainder checks the remainder can be the root volume
// (size-less /), with /home fixed-size.
func TestBuild_LVMVolumesRootRemainder(t *testing.T) {
	yamlSrc := `
system:
  hostname: arch-lvm
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
disks:
  layout: lvm
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: home, mountpoint: /home, filesystem: ext4, size: 32GiB}
      - {name: root, mountpoint: /, filesystem: xfs}
`
	var c config.Config
	if err := yaml.Unmarshal([]byte(yamlSrc), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	out, _, err := Build(&c, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	vols := out.DiskConfig.LvmConfig.VolGroups[0].Volumes
	if len(vols) != 2 {
		t.Fatalf("want 2 volumes, got %d", len(vols))
	}
	home := vols[0]
	wantHome := roundDownMiB(32 << 30)
	if home.Name != "home" || home.Length.Value != wantHome {
		t.Errorf("home volume wrong: %+v (want length %d)", home, wantHome)
	}
	root := vols[1]
	if root.Name != "root" || root.Mountpoint == nil || *root.Mountpoint != "/" {
		t.Errorf("root volume wrong: %+v", root)
	}

	disk1Total := uint64(256 << 30)
	pvBytes := roundDownMiB(disk1Total - (startOffset + (1 << 30) + endReserve))
	available := roundDownMiB(pvBytes - vgHeadroomPerPV)
	wantRoot := roundDownMiB(available - wantHome)
	if root.Length.Value != wantRoot {
		t.Errorf("root length = %d, want %d (rest of VG)", root.Length.Value, wantRoot)
	}
}

// TestBuild_LVMSingleVolumeUnchanged proves an empty-Volumes config still emits
// exactly one root LV (the historical single-LV path).
func TestBuild_LVMSingleVolumeUnchanged(t *testing.T) {
	yamlSrc := `
system:
  hostname: arch-lvm
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
disks:
  layout: lvm
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    lv: root
    filesystem: xfs
    pvs: [/dev/nvme0n1p2]
`
	var c config.Config
	if err := yaml.Unmarshal([]byte(yamlSrc), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	out, _, err := Build(&c, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	vols := out.DiskConfig.LvmConfig.VolGroups[0].Volumes
	if len(vols) != 1 {
		t.Fatalf("single-LV layout must emit exactly one volume, got %d", len(vols))
	}
	v := vols[0]
	if v.Name != "root" || v.FsType != "xfs" || v.Mountpoint == nil || *v.Mountpoint != "/" {
		t.Errorf("root LV wrong: %+v", v)
	}
}

func TestBuild_NTP(t *testing.T) {
	base := `
system:
  hostname: arch-lvm
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk%s
user:
  name: adam
disks:
  layout: lvm
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`
	cases := []struct {
		name    string
		ntpLine string
		want    bool
	}{
		{"unset defaults true", "", true},
		{"explicit true", "\n  ntp: true", true},
		{"explicit false", "\n  ntp: false", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c config.Config
			src := []byte(fmt.Sprintf(base, tc.ntpLine))
			if err := yaml.Unmarshal(src, &c); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := c.Validate(); err != nil {
				t.Fatalf("config invalid: %v", err)
			}
			out, _, err := Build(&c, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if out.Ntp != tc.want {
				t.Errorf("Ntp = %v, want %v", out.Ntp, tc.want)
			}
		})
	}
}
