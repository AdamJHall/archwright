package stages

import (
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

func mustNotContain(t *testing.T, plan []string, subs ...string) {
	t.Helper()
	joined := strings.Join(plan, "\n")
	for _, s := range subs {
		if strings.Contains(joined, s) {
			t.Errorf("plan unexpectedly contains %q.\nfull plan:\n%s", s, joined)
		}
	}
}

func cfgFromYAML(t *testing.T, y string) *config.Config {
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

const lvmZramYAML = `
system: {hostname: arch-zram, timezone: Europe/London, locale: en_GB.UTF-8, keymap: uk}
user: {name: adam}
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel: {base: [linux]}
disks:
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: zram}
  lvm: {vg: vg0, lv: root, filesystem: xfs, pvs: [/dev/nvme0n1p2]}
`

// zram swap must not create a /swapfile (archinstall handles zram); the LVM root
// is still mounted for post-install chroot work.
func TestPlan_Archinstall_ZramSwap(t *testing.T) {
	plan := planForCfg(t, Install, "archinstall", lvmZramYAML)
	mustContain(t, plan, "mount /dev/vg0/root /mnt")
	mustNotContain(t, plan, "of=/mnt/swapfile", "mkswap /mnt/swapfile")
}

const plainSwapPartYAML = `
system: {hostname: arch-plain, timezone: Europe/London, locale: en_GB.UTF-8, keymap: uk}
user: {name: adam}
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel: {base: [linux]}
disks:
  layout: plain
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: partition, size: 8GiB}
  plain: {device: /dev/nvme0n1, filesystem: ext4}
`

// plain layout with a swap partition: no /swapfile, and the root is partition 3
// (ESP=1, swap=2, root=3) of disk 1.
func TestPlan_Archinstall_PlainSwapPartition(t *testing.T) {
	plan := planForCfg(t, Install, "archinstall", plainSwapPartYAML)
	mustContain(t, plan, "mount /dev/nvme0n1p3 /mnt")
	mustNotContain(t, plan, "of=/mnt/swapfile")
}

const plainNoSwapYAML = `
system: {hostname: arch-plain, timezone: Europe/London, locale: en_GB.UTF-8, keymap: uk}
user: {name: adam}
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel: {base: [linux]}
disks:
  layout: plain
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: none}
  plain: {device: /dev/nvme0n1, filesystem: ext4}
`

// plain layout, no swap: root is partition 2, no swapfile.
func TestPlan_Archinstall_PlainNoSwap(t *testing.T) {
	plan := planForCfg(t, Install, "archinstall", plainNoSwapYAML)
	mustContain(t, plan, "mount /dev/nvme0n1p2 /mnt")
	mustNotContain(t, plan, "of=/mnt/swapfile")
}

func TestRootDevice(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"lvm", lvmZramYAML, "/dev/vg0/root"},
		{"plain swap partition", plainSwapPartYAML, "/dev/nvme0n1p3"},
		{"plain no swap", plainNoSwapYAML, "/dev/nvme0n1p2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rootDevice(cfgFromYAML(t, tc.yaml))
			if err != nil {
				t.Fatalf("rootDevice: %v", err)
			}
			if got != tc.want {
				t.Errorf("rootDevice = %q, want %q", got, tc.want)
			}
		})
	}
}
