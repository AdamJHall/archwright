package archinstall

import (
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

// bootloaderYAML is a minimal lvm config (the default layout) used to
// exercise the bootloader mapping in Build. The bootloader block is appended by
// the test so both kinds reuse the same base.
const bootloaderBaseYAML = `
system:
  hostname: arch-boot
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel:
  base: [linux]
disks:
  esp:
    device: /dev/nvme0n1
    size: 1GiB
  swap:
    type: zram
  lvm:
    vg: vg0
    lv: root
    filesystem: ext4
    pvs: [/dev/nvme0n1p2]
`

func buildForBootloader(t *testing.T, extra string) *Config {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(bootloaderBaseYAML+extra), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	out, _, err := Build(&c, Geometry{"/dev/nvme0n1": 256 << 30}, "x")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return out
}

func TestBuild_BootloaderDefault(t *testing.T) {
	out := buildForBootloader(t, "")
	if got := out.BootloaderConfig.Bootloader; got != "Grub" {
		t.Errorf("default bootloader = %q, want %q", got, "Grub")
	}
}

func TestBuild_BootloaderGrubExplicit(t *testing.T) {
	out := buildForBootloader(t, "bootloader:\n  kind: grub\n")
	if got := out.BootloaderConfig.Bootloader; got != "Grub" {
		t.Errorf("grub bootloader = %q, want %q", got, "Grub")
	}
}

func TestBuild_BootloaderSystemdBoot(t *testing.T) {
	out := buildForBootloader(t, "bootloader:\n  kind: systemd-boot\n")
	if got := out.BootloaderConfig.Bootloader; got != "Systemd-boot" {
		t.Errorf("systemd-boot bootloader = %q, want %q", got, "Systemd-boot")
	}
}

// A configured plymouth theme is rendered into bootloader_config.plymouth so
// archinstall installs and configures the splash in Phase A (replacing the old
// Phase B plymouth stage). The theme string passes through verbatim.
func TestBuild_PlymouthThemeRendered(t *testing.T) {
	out := buildForBootloader(t, "plymouth:\n  theme: spinner\n")
	if got := out.BootloaderConfig.Plymouth; got != "spinner" {
		t.Errorf("plymouth theme = %q, want %q", got, "spinner")
	}
}

// With no plymouth theme the field is omitted (omitempty), keeping the rendered
// bootloader_config byte-identical to the pre-4.4 output the golden files assert.
func TestBuild_PlymouthOmittedWhenUnset(t *testing.T) {
	out := buildForBootloader(t, "")
	if got := out.BootloaderConfig.Plymouth; got != "" {
		t.Errorf("plymouth theme = %q, want empty", got)
	}
}
