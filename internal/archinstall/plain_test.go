package archinstall

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

// TestBuild_BootloaderConfig asserts the canonical bootloader_config object form
// (not the deprecated bare "bootloader" string) and that there is no top-level
// disk_encryption key (encryption is nested under disk_config only).
func TestBuild_BootloaderConfig(t *testing.T) {
	cfg := plainConfig(t)
	c, _, err := Build(cfg, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.BootloaderConfig.Bootloader != "Grub" || c.BootloaderConfig.UKI || c.BootloaderConfig.Removable {
		t.Errorf("bootloader_config wrong: %+v", c.BootloaderConfig)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"bootloader_config":`) {
		t.Errorf("expected bootloader_config key, got: %s", s)
	}
	if strings.Contains(s, `"bootloader":"Grub","kernels"`) {
		t.Error("found deprecated bare top-level bootloader string")
	}
	// disk_encryption must appear only nested in disk_config, never at top level.
	if strings.Count(s, `"disk_encryption"`) != 1 {
		t.Errorf("expected exactly one (nested) disk_encryption, got %d: %s", strings.Count(s, `"disk_encryption"`), s)
	}
}

const plainYAML = `
system:
  hostname: arch-plain
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
disks:
  layout: plain
  esp:
    device: /dev/nvme0n1
    size: 1GiB
  swap:
    type: swapfile
    size: 8GiB
  plain:
    device: /dev/nvme0n1
    filesystem: ext4
`

func plainConfig(t *testing.T) *config.Config {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(plainYAML), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	return &c
}

func TestBuild_PlainLayout(t *testing.T) {
	cfg := plainConfig(t)
	geom := Geometry{"/dev/nvme0n1": 256 << 30}
	c, _, err := Build(cfg, geom, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.DiskConfig.LvmConfig != nil {
		t.Errorf("plain layout must not emit an lvm_config: %+v", c.DiskConfig.LvmConfig)
	}
	if len(c.DiskConfig.DeviceModifications) != 1 {
		t.Fatalf("want 1 device, got %d", len(c.DiskConfig.DeviceModifications))
	}
	d := c.DiskConfig.DeviceModifications[0]
	// swapfile (not partition) -> ESP + root only.
	if len(d.Partitions) != 2 {
		t.Fatalf("want ESP + root (2 partitions), got %d: %+v", len(d.Partitions), d.Partitions)
	}
	root := d.Partitions[1]
	if root.FsType == nil || *root.FsType != "ext4" || root.Mountpoint == nil || *root.Mountpoint != "/" {
		t.Errorf("root partition wrong: %+v", root)
	}
	if root.Start.Value != mib+(1<<30) {
		t.Errorf("root start = %d, want %d (ESP directly precedes root)", root.Start.Value, mib+(1<<30))
	}
}

func TestBuild_PlainSwapPartition(t *testing.T) {
	y := plainYAML
	// switch to a swap partition
	var c config.Config
	if err := yaml.Unmarshal([]byte(y), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	c.Disks.Swap.Type = "partition"
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	geom := Geometry{"/dev/nvme0n1": 256 << 30}
	out, _, err := Build(&c, geom, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	d := out.DiskConfig.DeviceModifications[0]
	if len(d.Partitions) != 3 {
		t.Fatalf("want ESP + swap + root (3 partitions), got %d", len(d.Partitions))
	}
	swap := d.Partitions[1]
	if swap.FsType == nil || *swap.FsType != "linux-swap" || !hasFlag(swap.Flags, "swap") {
		t.Errorf("swap partition wrong: %+v", swap)
	}
	if swap.Size.Value != 8<<30 {
		t.Errorf("swap size = %d, want %d", swap.Size.Value, 8<<30)
	}
	// archinstall's zram swap flag must remain off for an on-disk swap partition.
	if out.Swap {
		t.Error("Config.Swap (zram) should be false for a swap partition")
	}
}
