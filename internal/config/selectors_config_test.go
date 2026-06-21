package config

import (
	"strings"
	"testing"
)

// selectorBase is a minimal-but-valid config used to exercise the Phase B
// selector fields in isolation, independent of the shared validYAML table.
const selectorBase = `
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
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
    pvs: [/dev/sda]
`

func TestSelectors_Valid(t *testing.T) {
	y := selectorBase + `
desktop:
  environment: gnome
aur_helper: paru
flatpak_remotes:
  - name: flathub-beta
    url: https://flathub.org/beta-repo/flathub-beta.flatpakrepo
`
	if err := load(t, y).Validate(); err != nil {
		t.Fatalf("expected valid selector config, got: %v", err)
	}
}

func TestSelectors_Errors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "bad desktop.environment",
			yaml: selectorBase + "desktop:\n  environment: windows\n",
			want: []string{"desktop.environment must be one of: kde gnome hyprland sway none"},
		},
		{
			name: "bad aur_helper",
			yaml: selectorBase + "aur_helper: pacman\n",
			want: []string{"aur_helper must be one of: yay paru"},
		},
		{
			name: "flatpak_remote missing url",
			yaml: selectorBase + "flatpak_remotes:\n  - name: extra\n",
			want: []string{"flatpak_remotes[0].url is required"},
		},
		{
			name: "flatpak_remote missing name",
			yaml: selectorBase + "flatpak_remotes:\n  - url: https://example.com/repo.flatpakrepo\n",
			want: []string{"flatpak_remotes[0].name is required"},
		},
		{
			name: "flatpak_remote bad url",
			yaml: selectorBase + "flatpak_remotes:\n  - name: extra\n    url: not a url\n",
			want: []string{"flatpak_remotes[0].url must be a valid URL"},
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
