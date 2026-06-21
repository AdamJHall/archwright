package config

import (
	"os"
	"path/filepath"
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
setup:
  steps:
    - clone:
        url: https://github.com/tmux-plugins/tpm
        dest: ~/.config/tmux/plugins/tpm
    - command: curl -sS https://starship.rs/install.sh | sh -s -- -y
`

func load(t *testing.T, y string) *Config {
	t.Helper()
	var c Config
	if err := yaml.Unmarshal([]byte(y), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &c
}

func TestLoad_EnvSubstitution(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("substitutes ${VAR} and $VAR", func(t *testing.T) {
		t.Setenv("AW_USER", "adam")
		t.Setenv("AW_REPO", "https://example.com/dotfiles")
		cfg, err := Load(write(t, "user:\n  name: ${AW_USER}\nchezmoi:\n  repo: $AW_REPO\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.User.Name != "adam" {
			t.Errorf("user.name = %q, want adam", cfg.User.Name)
		}
		if cfg.Chezmoi.Repo != "https://example.com/dotfiles" {
			t.Errorf("chezmoi.repo = %q", cfg.Chezmoi.Repo)
		}
	})

	t.Run("unset variable errors", func(t *testing.T) {
		_, err := Load(write(t, "user:\n  name: ${AW_DOES_NOT_EXIST}\n"))
		if err == nil {
			t.Fatal("expected error for unset variable, got nil")
		}
		if !strings.Contains(err.Error(), "AW_DOES_NOT_EXIST") {
			t.Errorf("error should name the missing var, got: %v", err)
		}
	})

	t.Run("$$ is a literal dollar", func(t *testing.T) {
		cfg, err := Load(write(t, "user:\n  name: a$$b\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.User.Name != "a$b" {
			t.Errorf("user.name = %q, want a$b", cfg.User.Name)
		}
	})
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
				"disks.lvm is required when disks.layout is lvm",
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
		{
			name: "setup clone bad url",
			yaml: strings.Replace(validYAML, "url: https://github.com/tmux-plugins/tpm", "url: not a url", 1),
			want: []string{"setup.steps[0].clone.url must be a valid URL"},
		},
		{
			name: "setup clone missing dest",
			yaml: strings.Replace(validYAML, "        dest: ~/.config/tmux/plugins/tpm\n", "", 1),
			want: []string{"setup.steps[0].clone.dest is required"},
		},
		{
			name: "setup step with neither command nor clone",
			yaml: strings.Replace(validYAML, "    - command: curl -sS https://starship.rs/install.sh | sh -s -- -y", "    - {}", 1),
			want: []string{"setup.steps[1] must set either command or clone"},
		},
		{
			name: "setup step with both command and clone",
			yaml: strings.Replace(validYAML,
				"    - command: curl -sS https://starship.rs/install.sh | sh -s -- -y",
				"    - command: echo hi\n      clone:\n        url: https://github.com/x/y\n        dest: ~/y", 1),
			want: []string{"setup.steps[1] must set exactly one of command or clone, not both"},
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
