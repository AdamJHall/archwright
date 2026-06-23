package archinstall

import (
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

const btrfsYAML = `
system:
  hostname: arch-btrfs
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel:
  base: [linux]
disks:
  layout: btrfs
  esp:
    device: /dev/nvme0n1
    size: 1GiB
  swap:
    type: zram
  btrfs:
    device: /dev/nvme0n1
    compress: zstd
    snapshots: snapper
    subvolumes:
      - {name: "@", mountpoint: /}
      - {name: "@home", mountpoint: /home}
`

func btrfsConfig(t *testing.T) *config.Config {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(btrfsYAML), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	return &c
}

func TestBuild_BtrfsLayout(t *testing.T) {
	cfg := btrfsConfig(t)
	c, _, err := Build(cfg, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.DiskConfig.LvmConfig != nil {
		t.Errorf("btrfs layout must not emit lvm_config")
	}
	// zram -> archinstall's own swap flag is on; no swapfile/partition.
	if !c.Swap {
		t.Error("zram swap should set Config.Swap true")
	}
	if len(c.DiskConfig.DeviceModifications) != 1 {
		t.Fatalf("want 1 device, got %d", len(c.DiskConfig.DeviceModifications))
	}
	parts := c.DiskConfig.DeviceModifications[0].Partitions
	if len(parts) != 2 {
		t.Fatalf("want ESP + btrfs root, got %d", len(parts))
	}
	root := parts[1]
	// The btrfs root partition's own mountpoint is null: the @ subvolume below
	// (mountpoint "/") is what archinstall mounts at /, so the system lands
	// inside @ rather than the top-level subvolume.
	if root.FsType == nil || *root.FsType != "btrfs" || root.Mountpoint != nil {
		t.Errorf("btrfs root wrong: %+v", root)
	}
	if len(root.MountOptions) != 1 || root.MountOptions[0] != "compress=zstd" {
		t.Errorf("compress mount option wrong: %v", root.MountOptions)
	}
	if len(root.Btrfs) != 2 {
		t.Fatalf("want 2 subvolumes, got %d", len(root.Btrfs))
	}
	sv0, ok := root.Btrfs[0].(BtrfsSubvolume)
	if !ok {
		t.Fatalf("subvolume 0 is %T, want BtrfsSubvolume", root.Btrfs[0])
	}
	if sv0.Name != "@" || sv0.Mountpoint == nil || *sv0.Mountpoint != "/" {
		t.Errorf("subvolume 0 wrong: %+v", sv0)
	}
	sv1 := root.Btrfs[1].(BtrfsSubvolume)
	if sv1.Name != "@home" || sv1.Mountpoint == nil || *sv1.Mountpoint != "/home" {
		t.Errorf("subvolume 1 wrong: %+v", sv1)
	}
}

func TestBuild_BtrfsNoCompress(t *testing.T) {
	var c config.Config
	if err := yaml.Unmarshal([]byte(btrfsYAML), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	c.Disks.Btrfs.Compress = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	out, _, err := Build(&c, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := out.DiskConfig.DeviceModifications[0].Partitions[1]
	if len(root.MountOptions) != 0 {
		t.Errorf("expected no mount options without compress, got %v", root.MountOptions)
	}
}
