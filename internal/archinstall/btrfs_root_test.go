package archinstall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
)

// btrfsRootYAML is a self-contained btrfs layout: a single NVMe disk with an ESP
// and a btrfs root carrying @ (/) and @home (/home) subvolumes. It deliberately
// does not edit any shared fixture so this regression test stands alone.
const btrfsRootYAML = `
system:
  hostname: arch-btrfs-root
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
pacstrap: [base-devel, networkmanager]
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
    subvolumes:
      - {name: "@", mountpoint: /}
      - {name: "@home", mountpoint: /home}
`

// TestBtrfsRootPartitionMountpointNull asserts the bug fix: for a btrfs layout
// the root partition itself must have a nil (null) mountpoint, while the @
// subvolume entry is what maps to "/". Otherwise archinstall installs into the
// top-level subvolume (subvolid 5) and leaves @ empty.
func TestBtrfsRootPartitionMountpointNull(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "btrfs-root.yaml")
	if err := os.WriteFile(cfgPath, []byte(btrfsRootYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}

	geom := Geometry{"/dev/nvme0n1": 256 << 30}
	c, _, err := Build(cfg, geom, "TESTPASS")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Find the btrfs root partition (the one carrying subvolumes).
	var root *Partition
	for i := range c.DiskConfig.DeviceModifications {
		for j := range c.DiskConfig.DeviceModifications[i].Partitions {
			p := &c.DiskConfig.DeviceModifications[i].Partitions[j]
			if len(p.Btrfs) > 0 {
				root = p
			}
		}
	}
	if root == nil {
		t.Fatal("no btrfs root partition found in rendered config")
	}

	if root.Mountpoint != nil {
		t.Errorf("btrfs root partition mountpoint = %q, want nil (null) so @ drives the root mount", *root.Mountpoint)
	}

	// The @ subvolume must be the one mounted at "/".
	var atRoot bool
	for _, sv := range root.Btrfs {
		bs, ok := sv.(BtrfsSubvolume)
		if !ok {
			t.Fatalf("subvolume entry has unexpected type %T", sv)
		}
		if bs.Name == "@" {
			if bs.Mountpoint == nil || *bs.Mountpoint != "/" {
				t.Errorf("@ subvolume mountpoint = %v, want %q", bs.Mountpoint, "/")
			}
			atRoot = true
		}
	}
	if !atRoot {
		t.Error("no @ subvolume mapping to / found on the btrfs root partition")
	}
}
