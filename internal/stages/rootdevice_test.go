package stages

import (
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

// TestRootDevice_MultiVolume proves the multi-volume LVM fix: in multi-volume
// mode LVMLayout.LV is empty, so rootDevice derives the root LV from the volume
// mounted at "/" (validation guarantees exactly one such volume). The single-LV
// path is covered by TestRootDevice in swap_test.go.
func TestRootDevice_MultiVolume(t *testing.T) {
	var c config.Config
	body := `
system: {hostname: a, timezone: T, locale: en_GB.UTF-8, keymap: uk}
user: {name: adam}
kernel: {base: [linux]}
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm:
    vg: vg0
    pvs: [/dev/nvme0n1p2]
    volumes:
      - {name: rootlv, mountpoint: /,     filesystem: xfs, size: 64GiB}
      - {name: homelv, mountpoint: /home, filesystem: xfs}
`
	if err := yaml.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	got, err := rootDevice(&c)
	if err != nil {
		t.Fatalf("rootDevice: %v", err)
	}
	if want := "/dev/vg0/rootlv"; got != want {
		t.Errorf("rootDevice = %q, want %q", got, want)
	}
}
