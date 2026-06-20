package archinstall

import (
	"encoding/json"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

const testYAML = `
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
  shell: /usr/bin/zsh
  groups: [wheel, video]
disks:
  esp:
    device: /dev/nvme0n1
    size: 4GiB
  swap:
    size: 64GiB
  lvm:
    vg: vg0
    lv: root
    filesystem: xfs
    pvs: [/dev/nvme0n1p3, /dev/sda, /dev/sdb]
packages: [git, firefox]
`

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(testYAML), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	return &c
}

// geometry: disk1 512 GiB, two extra disks 1 TiB each.
func testGeom() Geometry {
	return Geometry{
		"/dev/nvme0n1": 512 * (1 << 30),
		"/dev/sda":     1 << 40,
		"/dev/sdb":     1 << 40,
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]uint64{
		"4GiB":   4 << 30,
		"64GiB":  64 << 30,
		"512MiB": 512 << 20,
		"1024":   1024,
		"4G":     4 << 30, // go-units treats G as binary
	}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil {
			t.Errorf("parseSize(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := parseSize("notasize"); err == nil {
		t.Error("expected error for invalid size")
	}
}

func TestBuild_DiskLayout(t *testing.T) {
	cfg, geom := testConfig(t), testGeom()
	c, _, err := Build(cfg, geom, "throwaway")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if c.DiskConfig.ConfigType != "manual_partitioning" {
		t.Errorf("config_type = %q", c.DiskConfig.ConfigType)
	}
	if c.Bootloader != "Grub" {
		t.Errorf("bootloader = %q, want Grub", c.Bootloader)
	}
	if c.Swap {
		t.Error("swap (zram) should be false; we use a swap partition")
	}

	// disk 1 must have exactly ESP + swap + PV, in order.
	d1 := c.DiskConfig.DeviceModifications[0]
	if d1.Device != "/dev/nvme0n1" || !d1.Wipe || len(d1.Partitions) != 3 {
		t.Fatalf("disk1 modification wrong: %+v", d1)
	}
	esp, swap, pv := d1.Partitions[0], d1.Partitions[1], d1.Partitions[2]
	if esp.FsType == nil || *esp.FsType != "fat32" || esp.Mountpoint == nil || *esp.Mountpoint != "/boot" {
		t.Errorf("ESP wrong: %+v", esp)
	}
	if !hasFlag(esp.Flags, "boot") || !hasFlag(esp.Flags, "esp") {
		t.Errorf("ESP flags = %v", esp.Flags)
	}
	if swap.FsType == nil || *swap.FsType != "linux-swap" || !hasFlag(swap.Flags, "swap") {
		t.Errorf("swap wrong: %+v", swap)
	}
	if pv.FsType != nil {
		t.Errorf("PV partition must be unformatted (fs_type null), got %v", *pv.FsType)
	}

	// ESP size honored; swap follows ESP; PV follows swap (1 MiB aligned start).
	if esp.Size.Value != 4<<30 {
		t.Errorf("ESP size = %d, want %d", esp.Size.Value, 4<<30)
	}
	if swap.Start.Value != mib+(4<<30) {
		t.Errorf("swap start = %d, want %d", swap.Start.Value, mib+(4<<30))
	}
	if pv.Start.Value != mib+(4<<30)+(64<<30) {
		t.Errorf("PV start = %d", pv.Start.Value)
	}

	// Two extra whole disks -> 3 device modifications total.
	if len(c.DiskConfig.DeviceModifications) != 3 {
		t.Fatalf("want 3 device modifications, got %d", len(c.DiskConfig.DeviceModifications))
	}
	for _, d := range c.DiskConfig.DeviceModifications[1:] {
		if len(d.Partitions) != 1 || d.Partitions[0].FsType != nil {
			t.Errorf("whole-disk PV %s wrong: %+v", d.Device, d.Partitions)
		}
		if d.Partitions[0].Start.Value != mib {
			t.Errorf("whole-disk PV %s start = %d, want %d", d.Device, d.Partitions[0].Start.Value, mib)
		}
	}
}

func TestBuild_PVWiring(t *testing.T) {
	cfg, geom := testConfig(t), testGeom()
	c, _, err := Build(cfg, geom, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Collect every PV partition obj_id (the unformatted partitions).
	pvIDs := map[string]bool{}
	for _, d := range c.DiskConfig.DeviceModifications {
		for _, p := range d.Partitions {
			if p.FsType == nil {
				pvIDs[p.ObjID] = true
			}
		}
	}
	if len(pvIDs) != 3 {
		t.Fatalf("want 3 PV partitions, got %d", len(pvIDs))
	}

	vg := c.DiskConfig.LvmConfig.VolGroups[0]
	if vg.Name != "vg0" {
		t.Errorf("vg name = %q", vg.Name)
	}
	if len(vg.LvmPvs) != 3 {
		t.Fatalf("vg should reference 3 PVs, got %d", len(vg.LvmPvs))
	}
	// Every lvm_pvs entry must be a real PV partition obj_id.
	for _, id := range vg.LvmPvs {
		if !pvIDs[id] {
			t.Errorf("lvm_pvs references unknown obj_id %q", id)
		}
	}
	if len(vg.Volumes) != 1 {
		t.Fatalf("want 1 LV, got %d", len(vg.Volumes))
	}
	lv := vg.Volumes[0]
	if lv.Name != "root" || lv.FsType != "xfs" || lv.Mountpoint == nil || *lv.Mountpoint != "/" {
		t.Errorf("root LV wrong: %+v", lv)
	}
	// LV length must fit inside the sum of PV sizes.
	var sumPV uint64
	for _, d := range c.DiskConfig.DeviceModifications {
		for _, p := range d.Partitions {
			if p.FsType == nil {
				sumPV += p.Size.Value
			}
		}
	}
	if lv.Length.Value > sumPV {
		t.Errorf("LV length %d exceeds total PV bytes %d", lv.Length.Value, sumPV)
	}
}

func TestBuild_Creds(t *testing.T) {
	cfg, geom := testConfig(t), testGeom()
	_, creds, err := Build(cfg, geom, "s3cret")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(creds.Users) != 1 || creds.Users[0].Username != "adam" || !creds.Users[0].Sudo {
		t.Errorf("user wrong: %+v", creds.Users)
	}
	if creds.Users[0].Password != "s3cret" || creds.RootPassword != "s3cret" {
		t.Errorf("password not propagated: %+v", creds)
	}
}

func TestBuild_Locale(t *testing.T) {
	cfg, geom := testConfig(t), testGeom()
	c, _, _ := Build(cfg, geom, "x")
	if c.LocaleConfig.SysLang != "en_GB" || c.LocaleConfig.SysEnc != "UTF-8" || c.LocaleConfig.KbLayout != "uk" {
		t.Errorf("locale wrong: %+v", c.LocaleConfig)
	}
}

// The whole config must serialize to JSON cleanly (archinstall reads JSON).
func TestBuild_JSONRoundtrips(t *testing.T) {
	cfg, geom := testConfig(t), testGeom()
	c, creds, _ := Build(cfg, geom, "x")
	if _, err := json.MarshalIndent(c, "", "  "); err != nil {
		t.Fatalf("config marshal: %v", err)
	}
	if _, err := json.Marshal(creds); err != nil {
		t.Fatalf("creds marshal: %v", err)
	}
}

func TestBuild_MissingGeometryErrors(t *testing.T) {
	cfg := testConfig(t)
	if _, _, err := Build(cfg, Geometry{}, "x"); err == nil {
		t.Error("expected error with no geometry")
	}
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
