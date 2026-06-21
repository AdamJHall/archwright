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
//   - There is no swap partition: archinstall 4.x's LVM path formats only the
//     boot partition, so a raw swap partition is never mkswap'd yet still gets a
//     failing swapon. Swap is a post-install /swapfile instead (see the stage).
//   - An LVM physical volume is a partition carrying the LV's filesystem as its
//     fs_type (archinstall 4.x's device_handler.partition() requires a non-null
//     fs_type for parted to create any partition); the fs is never written — in
//     LVM mode archinstall formats only the boot partition and pvcreates the
//     rest. The volume group claims a PV by listing its obj_id in lvm_pvs.
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
const Version = "4.3"

// newObjID generates the unique obj_id values archinstall uses to cross-reference
// partitions from lvm_pvs. It is a package var solely so golden render tests can
// swap in a deterministic generator; production always uses random UUIDs.
var newObjID = uuid.NewString

const (
	mib = uint64(1) << 20
	// startOffset is the first usable byte; 1 MiB is the conventional alignment.
	startOffset = mib
	// endReserve is shaved off the end of every disk for the secondary (backup)
	// GPT header, which lives in the last ~33 sectors. Without it the last
	// partition on a disk runs to the device end and overlaps the backup GPT, and
	// archinstall rejects the layout ("Partition overlaps backup GPT header").
	// 1 MiB comfortably covers the backup GPT and keeps MiB alignment.
	endReserve = mib
	// vgHeadroomPerPV is shaved off each PV when sizing the root LV, to stay
	// safely under the volume group's free extents (LVM metadata + alignment).
	// A few MiB lost across a multi-disk array is irrelevant.
	vgHeadroomPerPV = 8 * mib
)

// --- archinstall JSON schema (subset we emit) -------------------------------

// SectorSize mirrors archinstall's SectorSize.json(): just {value, unit} (no
// further nesting — unlike Size, which carries a sector_size).
type SectorSize struct {
	Value uint64 `json:"value"`
	Unit  string `json:"unit"`
}

// Size mirrors archinstall's Size.json(): {value, unit, sector_size}. We always
// emit value in bytes (unit "B") to avoid unit ambiguity for computed sizes.
type Size struct {
	Value      uint64      `json:"value"`
	Unit       string      `json:"unit"`
	SectorSize *SectorSize `json:"sector_size"`
}

func bytes(n uint64) Size {
	return Size{Value: n, Unit: "B", SectorSize: &SectorSize{Value: 512, Unit: "B"}}
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

// layoutBuilder turns the live device geometry into the device modifications (and
// optional LVM configuration) for one disk-layout strategy. Build selects one by
// the configured Disks.Layout and assembles the rest of the archinstall Config
// around its output. Each builder is responsible only for the layout-specific
// partitioning; cross-cutting concerns (ESP sizing, swap, locale, packages) live
// in Build.
type layoutBuilder interface {
	build(geom Geometry) (devices []Device, lvm *LvmConfiguration, err error)
}

// espPartition builds the shared ESP partition on disk 1: it is identical across
// every layout. The PV/root partition that follows it differs per layout.
func espPartition(espBytes uint64) Partition {
	boot := "/boot"
	espFs := "fat32"
	return Partition{
		ObjID: newObjID(), Status: "create", Type: "primary",
		Start: bytes(startOffset), Size: bytes(espBytes),
		FsType: &espFs, Mountpoint: &boot,
		MountOptions: []string{}, Flags: []string{"boot", "esp"}, Btrfs: []any{},
	}
}

// Build renders cfg + probed geometry into an archinstall config and creds file.
// password is used for both the user and root (a throwaway in VM/--yes runs).
func Build(cfg *config.Config, geom Geometry, password string) (*Config, *Creds, error) {
	espBytes, err := parseSize(cfg.Disks.ESP.Size)
	if err != nil {
		return nil, nil, fmt.Errorf("esp size: %w", err)
	}

	// Select the layout strategy. Empty defaults to lvm (historical behaviour).
	builder, err := selectBuilder(cfg, espBytes)
	if err != nil {
		return nil, nil, err
	}
	devices, lvm, err := builder.build(geom)
	if err != nil {
		return nil, nil, err
	}

	// zram swap is archinstall's own `swap` flag; every other swap kind is created
	// post-install or as a partition emitted by the layout builder, so the flag is
	// left false. swapfile is the default and matches the original behaviour.
	useZram := cfg.Disks.Swap.EffectiveType() == "zram"

	sysLang, sysEnc := splitLocale(cfg.System.Locale)
	// Pacstrap list: the bootstrap minimum plus any user-requested extras (e.g.
	// microcode). Built on a fresh slice so the package-level default is never
	// mutated across Build calls.
	pkgs := append(append([]string{}, bootstrapPackages...), cfg.PacstrapExtra...)
	c := &Config{
		Lang:       "English",
		Bootloader: "Grub",
		Kernels:    []string{"linux"},
		Hostname:   cfg.System.Hostname,
		Packages:   pkgs,
		Timezone:   cfg.System.Timezone,
		Ntp:        true,
		Swap:       useZram,
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

// selectBuilder picks the layout strategy from cfg.Disks.Layout (empty -> lvm).
// The matching layout sub-block is required (also enforced in config validation,
// but checked here so Build never nil-derefs).
func selectBuilder(cfg *config.Config, espBytes uint64) (layoutBuilder, error) {
	d := cfg.Disks
	switch d.EffectiveLayout() {
	case "lvm":
		if d.LVM == nil {
			return nil, fmt.Errorf("disks.lvm is required for the lvm layout")
		}
		return &lvmBuilder{esp: d.ESP, swap: d.Swap, lvm: *d.LVM, espBytes: espBytes}, nil
	case "plain":
		if d.Plain == nil {
			return nil, fmt.Errorf("disks.plain is required for the plain layout")
		}
		return &plainBuilder{esp: d.ESP, swap: d.Swap, plain: *d.Plain, espBytes: espBytes}, nil
	default:
		return nil, fmt.Errorf("unknown disks.layout %q", d.Layout)
	}
}

// --- lvm layout -------------------------------------------------------------

// lvmBuilder is the historical ESP + LVM-on-partitions root layout. Disk 1 holds
// the ESP and the first PV partition; any further PVs are whole extra disks.
type lvmBuilder struct {
	esp      config.ESPConfig
	swap     config.SwapConfig
	lvm      config.LVMLayout
	espBytes uint64
}

func (b *lvmBuilder) build(geom Geometry) ([]Device, *LvmConfiguration, error) {
	disk1 := b.esp.Device
	espBytes := b.espBytes
	// Swap is NOT a partition: archinstall 4.x's LVM path formats only the boot
	// partition (PVs are pvcreated), so a raw swap partition would never get
	// mkswap'd yet archinstall would still swapon it and fail. Swap is instead a
	// /swapfile created post-install (see the install stage), or zram, so it is not
	// consumed here.

	// Split the configured PVs into the one that lives on disk 1 and the rest
	// (whole extra disks). The disk-1 PV path is a partition under disk1.
	var disk1PV string
	var wholeDiskPVs []string
	for _, pv := range b.lvm.PVs {
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
		return nil, nil, fmt.Errorf("no LVM PV found on disk 1 (%s); expected a partition like %s", disk1, partDev(disk1, 2))
	}

	// Disk 1: ESP + PV partition (no swap partition). Sizes computed from geometry.
	disk1Total, ok := geom[disk1]
	if !ok || disk1Total == 0 {
		return nil, nil, fmt.Errorf("no geometry for disk 1 (%s)", disk1)
	}
	used := startOffset + espBytes + endReserve
	if disk1Total <= used {
		return nil, nil, fmt.Errorf("disk 1 (%s, %d bytes) too small for ESP (%d bytes)", disk1, disk1Total, used)
	}
	pvOnDisk1 := roundDownMiB(disk1Total - used)

	// A PV partition carries the LV filesystem as its fs_type purely so parted can
	// create it (archinstall 4.x requires a non-null fs_type per partition); the
	// filesystem is never written, the partition is pvcreated.
	pvFs := b.lvm.Filesystem
	espPart := espPartition(espBytes)
	disk1PVPart := Partition{
		ObjID: newObjID(), Status: "create", Type: "primary",
		Start: bytes(startOffset + espBytes), Size: bytes(pvOnDisk1),
		FsType: &pvFs, MountOptions: []string{}, Flags: []string{}, Btrfs: []any{},
	}

	devices := []Device{{
		Device: disk1, Wipe: true,
		Partitions: []Partition{espPart, disk1PVPart},
	}}

	// Whole extra disks: one full-disk PV partition each.
	pvObjIDs := []string{disk1PVPart.ObjID}
	totalPVBytes := pvOnDisk1
	for _, dev := range wholeDiskPVs {
		total, ok := geom[dev]
		if !ok || total == 0 {
			return nil, nil, fmt.Errorf("no geometry for PV disk %s", dev)
		}
		if total <= startOffset+endReserve {
			return nil, nil, fmt.Errorf("PV disk %s (%d bytes) too small", dev, total)
		}
		size := roundDownMiB(total - startOffset - endReserve)
		pv := Partition{
			ObjID: newObjID(), Status: "create", Type: "primary",
			Start: bytes(startOffset), Size: bytes(size),
			FsType: &pvFs, MountOptions: []string{}, Flags: []string{}, Btrfs: []any{},
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
			Name:   b.lvm.VG,
			LvmPvs: pvObjIDs,
			Volumes: []LvmVolume{{
				ObjID: newObjID(), Status: "create", Name: b.lvm.LV,
				FsType: b.lvm.Filesystem, Length: bytes(lvBytes),
				Mountpoint: &root, MountOptions: []string{}, Btrfs: []any{},
			}},
		}},
	}
	return devices, lvm, nil
}

// --- plain layout -----------------------------------------------------------

// plainBuilder is a single root partition (no LVM): ESP + a full-disk root
// partition formatted ext4/xfs, optionally preceded by a swap partition. Only
// disk 1 is used.
type plainBuilder struct {
	esp      config.ESPConfig
	swap     config.SwapConfig
	plain    config.PlainLayout
	espBytes uint64
}

func (b *plainBuilder) build(geom Geometry) ([]Device, *LvmConfiguration, error) {
	disk1 := b.esp.Device
	espBytes := b.espBytes

	disk1Total, ok := geom[disk1]
	if !ok || disk1Total == 0 {
		return nil, nil, fmt.Errorf("no geometry for disk 1 (%s)", disk1)
	}

	parts := []Partition{espPartition(espBytes)}
	offset := startOffset + espBytes

	// Optional swap partition (plain/btrfs only). Sized from swap.size.
	if b.swap.EffectiveType() == "partition" {
		swapBytes, err := parseSize(b.swap.Size)
		if err != nil {
			return nil, nil, fmt.Errorf("swap size: %w", err)
		}
		swapFs := "linux-swap"
		parts = append(parts, Partition{
			ObjID: newObjID(), Status: "create", Type: "primary",
			Start: bytes(offset), Size: bytes(swapBytes),
			FsType: &swapFs, MountOptions: []string{}, Flags: []string{"swap"}, Btrfs: []any{},
		})
		offset += swapBytes
	}

	used := offset + endReserve
	if disk1Total <= used {
		return nil, nil, fmt.Errorf("disk 1 (%s, %d bytes) too small for ESP+swap (%d bytes)", disk1, disk1Total, used)
	}
	rootBytes := roundDownMiB(disk1Total - used)

	root := "/"
	rootFs := b.plain.Filesystem
	parts = append(parts, Partition{
		ObjID: newObjID(), Status: "create", Type: "primary",
		Start: bytes(offset), Size: bytes(rootBytes),
		FsType: &rootFs, Mountpoint: &root,
		MountOptions: []string{}, Flags: []string{}, Btrfs: []any{},
	})

	return []Device{{Device: disk1, Wipe: true, Partitions: parts}}, nil, nil
}

// --- helpers ----------------------------------------------------------------

// ParseSize converts a human size string ("4GiB", "64G", "512MiB", "1024") to
// bytes. Exposed so the install stage can size the post-install swapfile from
// the same cfg.Disks.Swap.Size value, with identical (binary) unit semantics.
func ParseSize(s string) (uint64, error) { return parseSize(s) }

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
