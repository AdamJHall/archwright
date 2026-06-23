package archinstall

import (
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

const lvmEncYAML = `
system:
  hostname: arch-luks
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel:
  base: [linux]
disks:
  layout: lvm
  esp:
    device: /dev/nvme0n1
    size: 1GiB
  swap:
    type: swapfile
    size: 4GiB
  lvm:
    vg: vg0
    lv: root
    filesystem: ext4
    pvs:
      - /dev/nvme0n1p2
  encryption:
    type: lvm_on_luks
`

const luksBtrfsYAML = `
system:
  hostname: arch-luks-btrfs
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
    subvolumes:
      - {name: "@", mountpoint: /}
  encryption:
    type: luks
`

func configFrom(t *testing.T, y string) *config.Config {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(y), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	return &c
}

func TestBuild_LvmOnLuks(t *testing.T) {
	cfg := configFrom(t, lvmEncYAML)
	c, _, err := Build(cfg, Geometry{"/dev/nvme0n1": 256 << 30}, "secret")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	enc := c.DiskConfig.DiskEncryption
	if enc == nil {
		t.Fatal("disk_encryption is nil, want lvm_on_luks block")
	}
	if enc.EncryptionType != "lvm_on_luks" {
		t.Errorf("encryption_type = %q, want lvm_on_luks", enc.EncryptionType)
	}
	if c.EncryptionPassword != "secret" {
		t.Errorf("encryption_password = %q, want secret", c.EncryptionPassword)
	}
	// partitions must equal the VG's lvm_pvs obj_ids.
	wantPVs := c.DiskConfig.LvmConfig.VolGroups[0].LvmPvs
	if len(enc.Partitions) != len(wantPVs) {
		t.Fatalf("partitions len = %d, want %d", len(enc.Partitions), len(wantPVs))
	}
	for i := range wantPVs {
		if enc.Partitions[i] != wantPVs[i] {
			t.Errorf("partitions[%d] = %q, want %q (lvm_pvs)", i, enc.Partitions[i], wantPVs[i])
		}
	}
}

func TestBuild_LuksOnBtrfsRoot(t *testing.T) {
	cfg := configFrom(t, luksBtrfsYAML)
	c, _, err := Build(cfg, Geometry{"/dev/nvme0n1": 256 << 30}, "pw")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	enc := c.DiskConfig.DiskEncryption
	if enc == nil {
		t.Fatal("disk_encryption is nil, want luks block")
	}
	if enc.EncryptionType != "luks" {
		t.Errorf("encryption_type = %q, want luks", enc.EncryptionType)
	}
	if c.EncryptionPassword != "pw" {
		t.Errorf("encryption_password = %q, want pw", c.EncryptionPassword)
	}
	if len(enc.Partitions) != 1 {
		t.Fatalf("want exactly 1 encrypted partition, got %d", len(enc.Partitions))
	}
	// The encrypted partition must be the one providing "/". For btrfs that is
	// the partition carrying the @ subvolume mapped to "/" (its own mountpoint
	// is null), not a partition mounted directly at "/".
	var rootObjID string
	for _, p := range c.DiskConfig.DeviceModifications[0].Partitions {
		if partitionIsRoot(p) {
			rootObjID = p.ObjID
		}
	}
	if rootObjID == "" || enc.Partitions[0] != rootObjID {
		t.Errorf("encrypted partition %q != root obj_id %q", enc.Partitions[0], rootObjID)
	}
}

func TestBuild_NoEncryption(t *testing.T) {
	// btrfsYAML (defined in btrfs_test.go) sets no encryption.
	cfg := btrfsConfig(t)
	c, _, err := Build(cfg, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.DiskConfig.DiskEncryption != nil {
		t.Errorf("disk_encryption should be nil when unset, got %+v", c.DiskConfig.DiskEncryption)
	}
	if c.EncryptionPassword != "" {
		t.Errorf("encryption_password should be empty when unset, got %q", c.EncryptionPassword)
	}
}
