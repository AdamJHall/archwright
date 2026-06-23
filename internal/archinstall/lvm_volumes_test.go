package archinstall

import (
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
)

// TestMultiVolumeLVMPVFsTypeNonEmpty guards the multi-volume LVM PV-partition
// regression: in multi-volume mode lvm.filesystem is empty by schema, yet every
// PV partition must still carry a non-empty fs_type or archinstall 4.x aborts
// with "File system type is not set". The PV partitions inherit the first
// volume's filesystem.
func TestMultiVolumeLVMPVFsTypeNonEmpty(t *testing.T) {
	defer setDeterministicObjIDs()()

	b := &lvmBuilder{
		esp:      config.ESPConfig{Device: "/dev/nvme0n1", Size: "1GiB"},
		swap:     config.SwapConfig{Type: "swapfile", Size: "8GiB"},
		espBytes: 1 << 30,
		lvm: config.LVMLayout{
			VG:  "vg0",
			PVs: []string{"/dev/nvme0n1p2", "/dev/sda"},
			Volumes: []config.LVMVolume{
				{Name: "root", Mountpoint: "/", Filesystem: "xfs", Size: "50GiB"},
				{Name: "home", Mountpoint: "/home", Filesystem: "ext4"},
			},
		},
	}
	geom := Geometry{
		"/dev/nvme0n1": 256 << 30,
		"/dev/sda":     512 << 30,
	}

	devices, lvm, err := b.build(geom)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if lvm == nil {
		t.Fatal("expected an LvmConfiguration")
	}

	var pvParts int
	for _, dev := range devices {
		for _, p := range dev.Partitions {
			// PV partitions are the non-ESP partitions (no mountpoint).
			if p.Mountpoint != nil {
				continue
			}
			pvParts++
			if p.FsType == nil || *p.FsType == "" {
				t.Errorf("PV partition on %s has empty fs_type; archinstall would abort", dev.Device)
			}
		}
	}
	if pvParts == 0 {
		t.Fatal("expected at least one PV partition")
	}
}
