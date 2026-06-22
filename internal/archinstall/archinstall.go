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

// BtrfsSubvolume mirrors archinstall's SubvolumeModification.json(): a subvolume
// name and the mountpoint it is mounted at. It populates Partition.Btrfs for the
// btrfs layout. archinstall mounts each subvolume with subvol=<name>; compression
// and other options come from the partition's mount_options.
type BtrfsSubvolume struct {
	Name       string  `json:"name"`
	Mountpoint *string `json:"mountpoint"`
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

// DiskEncryption mirrors archinstall's DiskEncryption.json(). encryption_type is
// "luks" (encrypt the single root partition) or "lvm_on_luks" (encrypt the PV
// partitions under LVM). partitions holds the encrypted partition obj_ids (the
// root partition for luks, the PV partitions for lvm_on_luks). The password is
// supplied separately as the top-level encryption_password field.
type DiskEncryption struct {
	EncryptionType string   `json:"encryption_type"` // "luks"/"lvm_on_luks"
	Partitions     []string `json:"partitions"`
	// LvmVolumes carries LV obj_ids for a per-LV (luks-on-lvm) encryption
	// topology. That topology is reserved/unused for now — no encryption_type
	// currently populates it — so it always renders as an empty list. Kept on
	// the struct so the JSON shape matches archinstall's DiskEncryption.json().
	LvmVolumes []string `json:"lvm_volumes"`
}

// DiskConfig mirrors DiskLayoutConfiguration.json(). disk_encryption is nested
// here (the canonical location); there is no top-level disk_encryption. It is a
// pointer so the unencrypted case renders as null (byte-identical to before).
type DiskConfig struct {
	ConfigType          string            `json:"config_type"` // "manual_partitioning"
	DeviceModifications []Device          `json:"device_modifications"`
	LvmConfig           *LvmConfiguration `json:"lvm_config"`
	DiskEncryption      *DiskEncryption   `json:"disk_encryption"` // null when unencrypted
}

// BootloaderConfig mirrors archinstall's modern bootloader_config object
// (args.py BootloaderConfiguration). It replaces the deprecated bare
// `bootloader: "Grub"` string. Bootloader is one of "Grub", "Systemd-boot",
// "Efistub", "Limine", "Refind", "No bootloader"; uki/removable default false.
type BootloaderConfig struct {
	Bootloader string `json:"bootloader"`
	UKI        bool   `json:"uki"`
	Removable  bool   `json:"removable"`
}

// archBootloader maps our config bootloader kind ("grub"/"systemd-boot") to
// archinstall's bootloader string. "grub" yields the exact same "Grub" value as
// before; "systemd-boot" maps to archinstall's "Systemd-boot". Any unknown kind
// falls back to "Grub" (validation already restricts the set upstream).
func archBootloader(kind string) string {
	if kind == "systemd-boot" {
		return "Systemd-boot"
	}
	return "Grub"
}

// LocaleConfig mirrors locale_config.
type LocaleConfig struct {
	KbLayout string `json:"kb_layout"`
	SysEnc   string `json:"sys_enc"`
	SysLang  string `json:"sys_lang"`
}

// Config is the top-level archinstall --config file. Encryption, when used, is
// nested under disk_config only — there is no top-level disk_encryption.
type Config struct {
	Lang             string            `json:"archinstall-language"`
	BootloaderConfig BootloaderConfig  `json:"bootloader_config"`
	Kernels          []string          `json:"kernels"`
	Hostname         string            `json:"hostname"`
	Packages         []string          `json:"packages"`
	Timezone         string            `json:"timezone"`
	Ntp              bool              `json:"ntp"`
	Swap             bool              `json:"swap"` // zram; true only for the zram swap type
	LocaleConfig     LocaleConfig      `json:"locale_config"`
	NetworkConfig    map[string]string `json:"network_config"`
	DiskConfig       DiskConfig        `json:"disk_config"`
	// EncryptionPassword is the top-level LUKS password (NOT in the creds file).
	// omitempty keeps the unencrypted render byte-identical to before.
	EncryptionPassword string `json:"encryption_password,omitempty"`
	Version            string `json:"version"`
	ConfigVersion      string `json:"config_version"`
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

// kernelBase is the baseline kernel(s) archinstall pacstraps, taken verbatim
// from cfg.Kernel.Base. Validation (config.KernelConfig) guarantees it is
// non-empty; the fallback to "linux" is defence-in-depth so a misconfigured
// caller never renders a kernel-less, unbootable system.
func kernelBase(cfg *config.Config) []string {
	if len(cfg.Kernel.Base) == 0 {
		return []string{"linux"}
	}
	return cfg.Kernel.Base
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
	// Pacstrap list: rendered verbatim from cfg.Pacstrap — nothing is prepended.
	// Copied onto a fresh slice so the caller's config is never aliased/mutated.
	pkgs := append([]string(nil), cfg.Pacstrap...)
	c := &Config{
		Lang:             "English",
		BootloaderConfig: BootloaderConfig{Bootloader: archBootloader(cfg.Bootloader.EffectiveKind()), UKI: false, Removable: false},
		Kernels:          kernelBase(cfg),
		Hostname:         cfg.System.Hostname,
		Packages:         pkgs,
		Timezone:         cfg.System.Timezone,
		Ntp:              cfg.System.NTP == nil || *cfg.System.NTP,
		Swap:             useZram,
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

	// Optional LUKS encryption, nested under disk_config (the canonical location).
	// The obj_ids are derived from the structures the builder already produced, so
	// the layoutBuilder interface is unchanged.
	if cfg.Disks.Encryption != nil {
		enc, err := buildEncryption(cfg.Disks.Encryption.Type, devices, lvm)
		if err != nil {
			return nil, nil, err
		}
		c.DiskConfig.DiskEncryption = enc
		c.EncryptionPassword = password
	}

	creds := &Creds{
		Users:        []User{{Username: cfg.User.Name, Password: password, Sudo: true}},
		RootPassword: password,
	}
	return c, creds, nil
}

// buildEncryption derives the DiskEncryption block from the already-built devices
// and lvm config. For lvm_on_luks the encrypted partitions are exactly the VG's
// LvmPvs; for luks it is the single partition mounted at "/". archinstall rejects
// LVM encryption with more than two PV partitions (device_handler.py) — this is
// guarded here as well as in config validation. Only "luks" and "lvm_on_luks"
// are accepted (config validation restricts the set); a per-LV luks-on-lvm
// topology is not implemented.
//
// VM-validation-pending: the encryption_type values, the partitions obj_id
// wiring, and the 2-PV limit are reverse-engineered from archinstall source and
// must be confirmed against a real archinstall run in a VM.
func buildEncryption(encType string, devices []Device, lvm *LvmConfiguration) (*DiskEncryption, error) {
	switch encType {
	case "lvm_on_luks":
		if lvm == nil || len(lvm.VolGroups) == 0 {
			return nil, fmt.Errorf("%s encryption requires an lvm layout", encType)
		}
		pvs := lvm.VolGroups[0].LvmPvs
		if len(pvs) > 2 {
			return nil, fmt.Errorf("%s encryption supports at most 2 PVs (archinstall limit); got %d", encType, len(pvs))
		}
		return &DiskEncryption{EncryptionType: encType, Partitions: pvs, LvmVolumes: []string{}}, nil
	case "luks":
		var rootObjID string
		for _, dev := range devices {
			for _, p := range dev.Partitions {
				if p.Mountpoint != nil && *p.Mountpoint == "/" {
					rootObjID = p.ObjID
				}
			}
		}
		if rootObjID == "" {
			return nil, fmt.Errorf("luks encryption: no partition mounted at / to encrypt")
		}
		return &DiskEncryption{EncryptionType: encType, Partitions: []string{rootObjID}, LvmVolumes: []string{}}, nil
	default:
		return nil, fmt.Errorf("unknown encryption type %q", encType)
	}
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
	case "btrfs":
		if d.Btrfs == nil {
			return nil, fmt.Errorf("disks.btrfs is required for the btrfs layout")
		}
		return &btrfsBuilder{esp: d.ESP, swap: d.Swap, btrfs: *d.Btrfs, espBytes: espBytes}, nil
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
		return nil, nil, fmt.Errorf("no LVM PV found on disk 1 (%s); expected a partition like %s", disk1, PartDev(disk1, 2))
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

	// Available VG bytes for logical volumes: total PV bytes minus per-PV
	// headroom (LVM metadata + alignment), so concrete lengths stay under the
	// VG's free extents.
	headroom := vgHeadroomPerPV * uint64(len(pvObjIDs))
	if totalPVBytes <= headroom {
		return nil, nil, fmt.Errorf("volume group too small for headroom")
	}
	available := roundDownMiB(totalPVBytes - headroom)

	volumes, err := b.volumes(available)
	if err != nil {
		return nil, nil, err
	}

	lvm := &LvmConfiguration{
		ConfigType: "default",
		VolGroups: []LvmVolumeGroup{{
			Name:    b.lvm.VG,
			LvmPvs:  pvObjIDs,
			Volumes: volumes,
		}},
	}
	return devices, lvm, nil
}

// volumes turns the configured LVM layout into the LvmVolume list, given the
// bytes available in the VG (already net of per-PV headroom).
//
// Single-LV mode (Volumes empty): one root LV consuming all of `available`,
// mounted at "/", using LV/Filesystem — byte-identical to the historical output.
//
// Multi-volume mode: one LvmVolume per configured volume. Fixed-size volumes are
// parsed from Size; the single size-less volume receives the remainder
// (available minus the sum of the fixed sizes). Volumes are emitted in declared
// order.
func (b *lvmBuilder) volumes(available uint64) ([]LvmVolume, error) {
	root := "/"

	if len(b.lvm.Volumes) == 0 {
		return []LvmVolume{{
			ObjID: newObjID(), Status: "create", Name: b.lvm.LV,
			FsType: b.lvm.Filesystem, Length: bytes(available),
			Mountpoint: &root, MountOptions: []string{}, Btrfs: []any{},
		}}, nil
	}

	// Sum the fixed-size volumes so the remainder volume can take what's left.
	var fixedTotal uint64
	for _, v := range b.lvm.Volumes {
		if v.Size == "" {
			continue
		}
		n, err := parseSize(v.Size)
		if err != nil {
			return nil, fmt.Errorf("lvm volume %q size: %w", v.Name, err)
		}
		fixedTotal += roundDownMiB(n)
	}
	if fixedTotal >= available {
		return nil, fmt.Errorf("lvm volumes (%d bytes fixed) exceed the available VG space (%d bytes)", fixedTotal, available)
	}
	restBytes := roundDownMiB(available - fixedTotal)

	out := make([]LvmVolume, 0, len(b.lvm.Volumes))
	for _, v := range b.lvm.Volumes {
		var length uint64
		if v.Size == "" {
			length = restBytes
		} else {
			n, _ := parseSize(v.Size) // already validated above
			length = roundDownMiB(n)
		}
		mp := v.Mountpoint
		out = append(out, LvmVolume{
			ObjID: newObjID(), Status: "create", Name: v.Name,
			FsType: v.Filesystem, Length: bytes(length),
			Mountpoint: &mp, MountOptions: []string{}, Btrfs: []any{},
		})
	}
	return out, nil
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
	rootFs := b.plain.Filesystem
	devices, err := singleDiskRoot(b.esp, b.swap, b.espBytes, geom, rootSpec{
		fsType:       rootFs,
		mountOptions: []string{},
		btrfs:        []any{},
	})
	if err != nil {
		return nil, nil, err
	}
	return devices, nil, nil
}

// rootSpec describes the per-layout root partition that follows the shared ESP +
// optional swap prefix on disk 1. Only these three fields differ between the
// plain and btrfs layouts; everything else (Status/Type/Start/Size/Mountpoint at
// "/"/Flags) is identical and supplied by singleDiskRoot.
type rootSpec struct {
	fsType       string
	mountOptions []string
	btrfs        []any
}

// singleDiskRoot builds the disk-1 layout shared by the plain and btrfs builders:
// the ESP partition, an optional linux-swap partition (when swap.type is
// "partition", sized from swap.size), and a root partition consuming the rest of
// disk 1. The root partition's fs_type, mount options and btrfs subvolume list
// come from spec; everything else is fixed. The newObjID() call order is ESP,
// swap (if present), then root — matching both original builders so golden renders
// stay byte-identical.
func singleDiskRoot(esp config.ESPConfig, swap config.SwapConfig, espBytes uint64, geom Geometry, spec rootSpec) ([]Device, error) {
	disk1 := esp.Device

	disk1Total, ok := geom[disk1]
	if !ok || disk1Total == 0 {
		return nil, fmt.Errorf("no geometry for disk 1 (%s)", disk1)
	}

	parts := []Partition{espPartition(espBytes)}
	offset := startOffset + espBytes

	// Optional swap partition (plain/btrfs only). Sized from swap.size.
	if swap.EffectiveType() == "partition" {
		swapBytes, err := parseSize(swap.Size)
		if err != nil {
			return nil, fmt.Errorf("swap size: %w", err)
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
		return nil, fmt.Errorf("disk 1 (%s, %d bytes) too small for ESP+swap (%d bytes)", disk1, disk1Total, used)
	}
	rootBytes := roundDownMiB(disk1Total - used)

	root := "/"
	rootFs := spec.fsType
	parts = append(parts, Partition{
		ObjID: newObjID(), Status: "create", Type: "primary",
		Start: bytes(offset), Size: bytes(rootBytes),
		FsType: &rootFs, Mountpoint: &root,
		MountOptions: spec.mountOptions, Flags: []string{}, Btrfs: spec.btrfs,
	})

	return []Device{{Device: disk1, Wipe: true, Partitions: parts}}, nil
}

// --- btrfs layout -----------------------------------------------------------

// btrfsBuilder is a single btrfs root partition carrying subvolumes (the common
// snapshot-friendly desktop layout). Only disk 1 is used. The root partition is
// mounted at "/", and each configured subvolume is emitted in the partition's
// Btrfs list so archinstall creates and mounts it. An optional swap partition
// (sized from swap.size) precedes the root partition.
//
// Compression (e.g. "zstd") becomes a compress=<v> mount option on the root
// partition. Snapper, when requested, is left to a post-install/hook step — this
// builder only shapes the partition table and subvolume set.
//
// Swapfile-on-btrfs hazard: a swapfile on a compressed/CoW btrfs corrupts. We do
// NOT emit a swapfile for btrfs; the supported on-disk swap for btrfs is a
// dedicated swap *partition* (swap.type: partition). zram and none are also fine.
type btrfsBuilder struct {
	esp      config.ESPConfig
	swap     config.SwapConfig
	btrfs    config.BtrfsLayout
	espBytes uint64
}

func (b *btrfsBuilder) build(geom Geometry) ([]Device, *LvmConfiguration, error) {
	mountOpts := []string{}
	if b.btrfs.Compress != "" {
		mountOpts = append(mountOpts, "compress="+b.btrfs.Compress)
	}

	// Subvolumes: one entry per configured subvolume, in declared order.
	subvols := make([]any, 0, len(b.btrfs.Subvolumes))
	for _, sv := range b.btrfs.Subvolumes {
		mp := sv.Mountpoint
		subvols = append(subvols, BtrfsSubvolume{Name: sv.Name, Mountpoint: &mp})
	}

	devices, err := singleDiskRoot(b.esp, b.swap, b.espBytes, geom, rootSpec{
		fsType:       "btrfs",
		mountOptions: mountOpts,
		btrfs:        subvols,
	})
	if err != nil {
		return nil, nil, err
	}
	return devices, nil, nil
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

// PartDev returns the kernel partition device for a base device and number:
// /dev/sda -> /dev/sda1, but /dev/nvme0n1 -> /dev/nvme0n1p1. It is the single
// shared definition; the stages package calls it rather than duplicating it.
func PartDev(dev string, n int) string {
	if len(dev) > 0 {
		if last := dev[len(dev)-1]; last >= '0' && last <= '9' {
			return fmt.Sprintf("%sp%d", dev, n)
		}
	}
	return fmt.Sprintf("%s%d", dev, n)
}
