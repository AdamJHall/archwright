package archinstall

import (
	"slices"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

// TestBuild_KernelsFromBase asserts the rendered archinstall Kernels field
// reflects kernel.base verbatim (no longer the hardcoded ["linux"]).
func TestBuild_KernelsFromBase(t *testing.T) {
	const y = `
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
disks:
  esp:
    device: /dev/nvme0n1
    size: 1GiB
  swap:
    size: 8GiB
  lvm:
    vg: vg0
    lv: root
    filesystem: xfs
    pvs: [/dev/nvme0n1p2]
kernel:
  base: [linux-lts, linux-zen]
  packages: [linux-cachyos]
`
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}

	c, _, err := Build(&cfg, Geometry{"/dev/nvme0n1": 256 << 30}, "TESTPASS")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []string{"linux-lts", "linux-zen"}
	if !slices.Equal(c.Kernels, want) {
		t.Errorf("Kernels = %v, want %v (kernel.base; not kernel.packages)", c.Kernels, want)
	}
}
