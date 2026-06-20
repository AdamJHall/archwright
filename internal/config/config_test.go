package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const validYAML = `
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
  shell: /usr/bin/zsh
  groups: [wheel]
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
    pvs: [/dev/nvme0n1p3, /dev/sda]
mirrors:
  reflector: true
  countries: [GB, DE]
  latest: 20
  sort: rate
  protocols: [https]
repos:
  - name: cachyos
    setup: "cachyos-repo.sh --install"
packages: [git]
kernel:
  packages: [linux-cachyos, linux-cachyos-headers]
  default: linux-cachyos
  replace_stock: true
flatpaks: [com.spotify.Client]
aur: [1password]
plymouth:
  theme: bgrt
grub:
  cmdline_extra: "quiet splash"
  theme:
    source: vinceliuice
    name: tela
kde:
  look_and_feel: org.kde.breezedark.desktop
chezmoi:
  repo: https://github.com/AdamJHall/dotfiles
`

func load(t *testing.T, y string) *Config {
	t.Helper()
	var c Config
	if err := yaml.Unmarshal([]byte(y), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &c
}

func TestValidate_Valid(t *testing.T) {
	if err := load(t, validYAML).Validate(); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidate_Errors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string // substrings that must all appear in the joined error
	}{
		{
			name: "empty reports all required + pvs",
			yaml: "{}",
			want: []string{
				"system.hostname is required",
				"user.name is required",
				"disks.esp.device is required",
				"disks.lvm.pvs must have at least 1 item(s)",
			},
		},
		{
			name: "bad filesystem",
			yaml: strings.Replace(validYAML, "filesystem: xfs", "filesystem: zfs", 1),
			want: []string{"disks.lvm.filesystem must be one of: xfs ext4"},
		},
		{
			name: "device without /dev prefix",
			yaml: strings.Replace(validYAML, "device: /dev/nvme0n1", "device: nvme0n1", 1),
			want: []string{`disks.esp.device must start with "/dev/"`},
		},
		{
			name: "bad size string",
			yaml: strings.Replace(validYAML, "size: 4GiB", "size: huge", 1),
			want: []string{"disks.esp.size must be a size like 64GiB"},
		},
		{
			name: "invalid hostname",
			yaml: strings.Replace(validYAML, "hostname: arch-box", "hostname: bad host", 1),
			want: []string{"system.hostname must be a valid hostname"},
		},
		{
			name: "empty pvs list",
			yaml: strings.Replace(validYAML, "pvs: [/dev/nvme0n1p3, /dev/sda]", "pvs: []", 1),
			want: []string{"disks.lvm.pvs must have at least 1 item(s)"},
		},
		{
			name: "bad grub theme source",
			yaml: strings.Replace(validYAML, "source: vinceliuice", "source: bananas", 1),
			want: []string{"grub.theme.source must be one of: vinceliuice url none"},
		},
		{
			name: "non-/dev PV is rejected by dive rule",
			yaml: strings.Replace(validYAML, "pvs: [/dev/nvme0n1p3, /dev/sda]", "pvs: [/dev/nvme0n1p3, sda]", 1),
			want: []string{`disks.lvm.pvs[1] must start with "/dev/"`},
		},
		{
			name: "replace_stock without kernel packages",
			yaml: strings.Replace(validYAML,
				"  packages: [linux-cachyos, linux-cachyos-headers]\n  default: linux-cachyos\n  replace_stock: true",
				"  replace_stock: true", 1),
			want: []string{"kernel.replace_stock requires at least one kernel.packages"},
		},
		{
			name: "default kernel not in packages",
			yaml: strings.Replace(validYAML, "default: linux-cachyos", "default: linux-zen", 1),
			want: []string{`kernel.default "linux-zen" must be one of kernel.packages`},
		},
		{
			name: "bad reflector sort",
			yaml: strings.Replace(validYAML, "sort: rate", "sort: bogus", 1),
			want: []string{"mirrors.sort must be one of: rate age score delay country"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := load(t, tc.yaml).Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("missing %q in error:\n%s", w, err)
				}
			}
		})
	}
}
