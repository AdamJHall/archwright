// Package archinstall renders our config.yaml into the JSON that the official
// Arch installer (archinstall) consumes, so Phase A can delegate partitioning,
// LVM, pacstrap and bootloader install to a maintained tool instead of driving
// sgdisk/lvm/pacstrap/grub by hand.
//
// The schema here mirrors the json() output of archinstall's own dataclasses
// (archinstall/lib/models/device.py on the pinned version below). That schema is
// NOT a stable public API — it changes between archinstall releases — so:
//
//   - Version is the archinstall release this was modelled against; the install
//     stage warns if the live ISO ships a different one.
//   - The exact byte layout (sector_size nesting, credentials keys) must be
//     validated against a real archinstall run in a VM before trusting it on
//     hardware. See the repo README "Testing in a VM".
//
// Key facts that shaped this:
//   - config_type "manual_partitioning": we hand archinstall explicit partitions.
//   - A swap partition is fs_type "linux-swap" with flag "swap".
//   - An LVM physical volume is just an unformatted partition (no fs_type/flag);
//     the volume group claims it by listing the partition's obj_id in lvm_pvs.
//   - There is NO "100%FREE"/percent size — every Size is concrete bytes, so we
//     compute the "rest of disk" / "rest of VG" sizes from live device geometry.
package archinstall

import (
	"fmt"
	"strings"

	"github.com/AdamJHall/archwright/internal/config"
	units "github.com/docker/go-units"
	"github.com/google/uuid"
)

// Version is the archinstall release this schema was modelled against.
const Version = "3.0.9"

// newObjID generates the unique obj_id values archinstall uses to cross-reference
// partitions from lvm_pvs. It is a package var solely so golden render tests can
// swap in a deterministic generator; production always uses random UUIDs.
var newObjID = uuid.NewString

const (
	mib = uint64(1) << 20
	// startOffset is the first usable byte; 1 MiB is the conventional alignment.
	startOffset = mib
	// vgHeadroomPerPV is shaved off each PV when sizing the root LV, to stay
	// safely under the volume group's free extents (LVM metadata + alignment).
	// A few MiB lost across a multi-disk array is irrelevant.
	vgHeadroomPerPV = 8 * mib
)

// --- archinstall JSON schema (subset we emit) -------------------------------

// Size mirrors archinstall's Size.json(): {value, unit, sector_size}. We always
// emit value in bytes (unit "B") to avoid unit ambiguity for computed sizes.
type Size struct {
	Value      uint64 `json:"value"`
	Unit       string `json:"unit"`
	SectorSize *Size  `json:"sector_size"`
}

func bytes(n uint64) Size {
	return Size{Value: n, Unit: "B", SectorSize: &Size{Value: 512, Unit: "B"}}
}

// Partition mirrors PartitionModification.json().
type Partition struct {
	ObjID        string   `json:"obj_id"`
	Status       string   `json:"status"` // "create"
	Type         string   `json:"type"`   // "primary"
	Start        Size     `json:"start"`
	Size         Size     `json:"size"`
	FsType       *string  `json:"fs_type"` // "fat32"/"linux-swap"; null for a PV
	Mountpoint   *string  `json:"mountpoint"`
	MountOptions []string `json:"mount_options"`
	Flags        []string `json:"flags"`
	DevPath      *string  `json:"dev_path"` // null: not created yet
	Btrfs        []any    `json:"btrfs"`
}

// Device mirrors DeviceModification.json().
type Device struct {
	Device     string      `json:"device"`
	Wipe       bool        `json:"wipe"`
	Partitions []Partition `json:"partitions"`
}

// LvmVolume mirrors LvmVolume.json().
type LvmVolume struct {
	ObjID        string   `json:"obj_id"`
	Status       string   `json:"status"` // "create"
	Name         string   `json:"name"`
	FsType       string   `json:"fs_type"` // "xfs"
	Length       Size     `json:"length"`
	Mountpoint   *string  `json:"mountpoint"`
	MountOptions []string `json:"mount_options"`
	Btrfs        []any    `json:"btrfs"`
}

// LvmVolumeGroup mirrors LvmVolumeGroup.json(). lvm_pvs references the PV
// partitions by their obj_id.
type LvmVolumeGroup struct {
	Name    string      `json:"name"`
	LvmPvs  []string    `json:"lvm_pvs"`
	Volumes []LvmVolume `json:"volumes"`
}

// LvmConfiguration mirrors LvmConfiguration.json().
type LvmConfiguration struct {
	ConfigType string           `json:"config_type"` // "default"
	VolGroups  []LvmVolumeGroup `json:"vol_groups"`
}

// DiskConfig mirrors DiskLayoutConfiguration.json().
type DiskConfig struct {
	ConfigType          string            `json:"config_type"` // "manual_partitioning"
	DeviceModifications []Device          `json:"device_modifications"`
	LvmConfig           *LvmConfiguration `json:"lvm_config"`
	DiskEncryption      any               `json:"disk_encryption"` // null
}

// LocaleConfig mirrors locale_config.
type LocaleConfig struct {
	KbLayout string `json:"kb_layout"`
	SysEnc   string `json:"sys_enc"`
	SysLang  string `json:"sys_lang"`
}

// Config is the top-level archinstall --config file.
type Config struct {
	Lang           string            `json:"archinstall-language"`
	Bootloader     string            `json:"bootloader"`
	Kernels        []string          `json:"kernels"`
	Hostname       string            `json:"hostname"`
	Packages       []string          `json:"packages"`
	Timezone       string            `json:"timezone"`
	Ntp            bool              `json:"ntp"`
	Swap           bool              `json:"swap"` // zram; false — we use a swap partition
	LocaleConfig   LocaleConfig      `json:"locale_config"`
	NetworkConfig  map[string]string `json:"network_config"`
	DiskConfig     DiskConfig        `json:"disk_config"`
	DiskEncryption any               `json:"disk_encryption"` // null
	Version        string            `json:"version"`
	ConfigVersion  string            `json:"config_version"`
}

// User mirrors a credentials-file user entry.
type User struct {
	Username string `json:"username"`
	Password string `json:"!password"`
	Sudo     bool   `json:"sudo"`
}

// Creds is the top-level archinstall --creds file (secrets kept out of --config).
type Creds struct {
	Users        []User `json:"users"`
	RootPassword string `json:"!root-password"`
}

// Geometry maps each device path to its total size in bytes (probed at install
// time via `blockdev --getsize64`). Build needs it because archinstall sizes are
// concrete, not "rest of disk".
type Geometry map[string]uint64

// bootstrapPackages: the minimum extras archinstall installs so Phase B can run
// (yay needs base-devel+git; the user's login shell). The full package list
// stays in Phase B's packages stage.
var bootstrapPackages = []string{"base-devel", "git", "zsh", "sudo", "networkmanager", "efibootmgr"}

// Build renders cfg + probed geometry into an archinstall config and creds file.
// password is used for both the user and root (a throwaway in VM/--yes runs).
func Build(cfg *config.Config, geom Geometry, password string) (*Config, *Creds, error) {
	disk1 := cfg.Disks.ESP.Device

	espBytes, err := parseSize(cfg.Disks.ESP.Size)
	if err != nil {
		return nil, nil, fmt.Errorf("esp size: %w", err)
	}
	swapBytes, err := parseSize(cfg.Disks.Swap.Size)
	if err != nil {
		return nil, nil, fmt.Errorf("swap size: %w", err)
	}

	// Split the configured PVs into the one that lives on disk 1 and the rest
	// (whole extra disks). The disk-1 PV path is a partition under disk1.
	var disk1PV string
	var wholeDiskPVs []string
	for _, pv := range cfg.Disks.LVM.PVs {
		if strings.HasPrefix(pv, disk1) {
			if disk1PV != "" {
				return nil, nil, fmt.Errorf("more than one PV on disk 1 (%s and %s)", disk1PV, pv)
			}
			disk1PV = pv
		} else {
			wholeDiskPVs = append(wholeDiskPVs, pv)
		}
	}
	if disk1PV == "" {
		return nil, nil, fmt.Errorf("no LVM PV found on disk 1 (%s); expected a partition like %s", disk1, partDev(disk1, 3))
	}

	// Disk 1: ESP + swap + PV partition. Sizes computed from disk1 geometry.
	disk1Total, ok := geom[disk1]
	if !ok || disk1Total == 0 {
		return nil, nil, fmt.Errorf("no geometry for disk 1 (%s)", disk1)
	}
	used := startOffset + espBytes + swapBytes
	if disk1Total <= used {
		return nil, nil, fmt.Errorf("disk 1 (%s, %d bytes) too small for ESP+swap (%d bytes)", disk1, disk1Total, used)
	}
	pvOnDisk1 := roundDownMiB(disk1Total - used)

	boot := "/boot"
	espFs, swapFs := "fat32", "linux-swap"
	espPart := Partition{
		ObjID: newObjID(), Status: "create", Type: "primary",
		Start: bytes(startOffset), Size: bytes(espBytes),
		FsType: &espFs, Mountpoint: &boot,
		MountOptions: []string{}, Flags: []string{"boot", "esp"}, Btrfs: []any{},
	}
	swapPart := Partition{
		ObjID: newObjID(), Status: "create", Type: "primary",
		Start: bytes(startOffset + espBytes), Size: bytes(swapBytes),
		FsType: &swapFs, MountOptions: []string{}, Flags: []string{"swap"}, Btrfs: []any{},
	}
	disk1PVPart := Partition{
		ObjID: newObjID(), Status: "create", Type: "primary",
		Start: bytes(startOffset + espBytes + swapBytes), Size: bytes(pvOnDisk1),
		FsType: nil, MountOptions: []string{}, Flags: []string{}, Btrfs: []any{},
	}

	devices := []Device{{
		Device: disk1, Wipe: true,
		Partitions: []Partition{espPart, swapPart, disk1PVPart},
	}}

	// Whole extra disks: one full-disk PV partition each.
	pvObjIDs := []string{disk1PVPart.ObjID}
	totalPVBytes := pvOnDisk1
	for _, dev := range wholeDiskPVs {
		total, ok := geom[dev]
		if !ok || total == 0 {
			return nil, nil, fmt.Errorf("no geometry for PV disk %s", dev)
		}
		if total <= startOffset {
			return nil, nil, fmt.Errorf("PV disk %s (%d bytes) too small", dev, total)
		}
		size := roundDownMiB(total - startOffset)
		pv := Partition{
			ObjID: newObjID(), Status: "create", Type: "primary",
			Start: bytes(startOffset), Size: bytes(size),
			FsType: nil, MountOptions: []string{}, Flags: []string{}, Btrfs: []any{},
		}
		devices = append(devices, Device{Device: dev, Wipe: true, Partitions: []Partition{pv}})
		pvObjIDs = append(pvObjIDs, pv.ObjID)
		totalPVBytes += size
	}

	// Root LV: consume (almost) all of the VG. Shave headroom per PV so the
	// concrete byte length stays under the VG's free extents.
	headroom := vgHeadroomPerPV * uint64(len(pvObjIDs))
	if totalPVBytes <= headroom {
		return nil, nil, fmt.Errorf("volume group too small for headroom")
	}
	lvBytes := roundDownMiB(totalPVBytes - headroom)

	root := "/"
	lvm := &LvmConfiguration{
		ConfigType: "default",
		VolGroups: []LvmVolumeGroup{{
			Name:   cfg.Disks.LVM.VG,
			LvmPvs: pvObjIDs,
			Volumes: []LvmVolume{{
				ObjID: newObjID(), Status: "create", Name: cfg.Disks.LVM.LV,
				FsType: cfg.Disks.LVM.Filesystem, Length: bytes(lvBytes),
				Mountpoint: &root, MountOptions: []string{}, Btrfs: []any{},
			}},
		}},
	}

	sysLang, sysEnc := splitLocale(cfg.System.Locale)
	c := &Config{
		Lang:       "English",
		Bootloader: "Grub",
		Kernels:    []string{"linux"},
		Hostname:   cfg.System.Hostname,
		Packages:   bootstrapPackages,
		Timezone:   cfg.System.Timezone,
		Ntp:        true,
		Swap:       false,
		LocaleConfig: LocaleConfig{
			KbLayout: cfg.System.Keymap, SysEnc: sysEnc, SysLang: sysLang,
		},
		NetworkConfig: map[string]string{"type": "nm"},
		DiskConfig: DiskConfig{
			ConfigType:          "manual_partitioning",
			DeviceModifications: devices,
			LvmConfig:           lvm,
		},
		Version:       Version,
		ConfigVersion: Version,
	}

	creds := &Creds{
		Users:        []User{{Username: cfg.User.Name, Password: password, Sudo: true}},
		RootPassword: password,
	}
	return c, creds, nil
}

// --- helpers ----------------------------------------------------------------

// parseSize converts a human size ("4GiB", "64G", "512MiB", "1024") to bytes.
// go-units treats K/M/G/T as binary (1024-based), matching GiB disk semantics.
func parseSize(s string) (uint64, error) {
	n, err := units.RAMInBytes(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative size %q", s)
	}
	return uint64(n), nil
}

func roundDownMiB(n uint64) uint64 { return (n / mib) * mib }

// splitLocale turns "en_GB.UTF-8" into ("en_GB", "UTF-8"); a bare locale gets
// "UTF-8".
func splitLocale(locale string) (lang, enc string) {
	if i := strings.IndexByte(locale, '.'); i >= 0 {
		return locale[:i], locale[i+1:]
	}
	return locale, "UTF-8"
}

// partDev mirrors stages.partDev for the disk-1 PV error hint.
func partDev(dev string, n int) string {
	if len(dev) > 0 {
		if last := dev[len(dev)-1]; last >= '0' && last <= '9' {
			return fmt.Sprintf("%sp%d", dev, n)
		}
	}
	return fmt.Sprintf("%s%d", dev, n)
}
