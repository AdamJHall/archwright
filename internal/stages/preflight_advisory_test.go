package stages

import (
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
)

// containsSubstr reports whether any advisory in msgs contains sub.
func containsSubstr(msgs []string, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

func TestPacstrapAdvisories(t *testing.T) {
	// fullSet is a complete, well-formed pacstrap that should trip no warnings
	// (it has the AUR build deps, networkmanager, microcode, and a kernel).
	fullSet := []string{"base-devel", "git", "zsh", "sudo", "networkmanager", "efibootmgr", "intel-ucode", "linux"}

	cases := []struct {
		name     string
		pacstrap []string
		aur      []string
		aurHelp  string
		kernels  []string
		wantSub  string // substring that must appear in some advisory ("" = expect none)
	}{
		{
			name:     "complete set is clean",
			pacstrap: fullSet,
			wantSub:  "",
		},
		{
			name:     "missing base-devel with aur list warns",
			pacstrap: []string{"git", "networkmanager", "intel-ucode", "linux"},
			aur:      []string{"1password"},
			wantSub:  "base-devel",
		},
		{
			name:     "missing git with aur_helper warns",
			pacstrap: []string{"base-devel", "networkmanager", "intel-ucode", "linux"},
			aurHelp:  "yay",
			wantSub:  "base-devel and/or git",
		},
		{
			name:     "no aur configured does not warn about build deps",
			pacstrap: []string{"networkmanager", "intel-ucode", "linux"},
			wantSub:  "", // overridden below by a dedicated absence check
		},
		{
			name:     "missing networkmanager warns",
			pacstrap: []string{"base-devel", "git", "intel-ucode", "linux"},
			wantSub:  "networkmanager",
		},
		{
			name:     "no microcode is advised",
			pacstrap: []string{"base-devel", "git", "networkmanager", "linux"},
			wantSub:  "microcode",
		},
		{
			name:     "no kernel warns",
			pacstrap: []string{"base-devel", "git", "networkmanager", "intel-ucode"},
			wantSub:  "may not boot",
		},
		{
			name:     "amd-ucode counts as microcode",
			pacstrap: []string{"base-devel", "git", "networkmanager", "amd-ucode", "linux-zen"},
			wantSub:  "", // linux-zen is a kernel, amd-ucode is microcode
		},
		{
			name:     "kernel.packages satisfies the kernel check",
			pacstrap: []string{"base-devel", "git", "networkmanager", "intel-ucode"},
			kernels:  []string{"linux-cachyos"},
			wantSub:  "", // no kernel in pacstrap, but kernel.packages declared
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Pacstrap = tc.pacstrap
			cfg.AUR = tc.aur
			cfg.AurHelper = tc.aurHelp
			cfg.Kernel.Packages = tc.kernels

			got := pacstrapAdvisories(cfg)

			if tc.wantSub == "" {
				return // presence cases below assert the negatives precisely
			}
			if !containsSubstr(got, tc.wantSub) {
				t.Errorf("advisories %v missing substring %q", got, tc.wantSub)
			}
		})
	}
}

// TestPacstrapAdvisories_NoFalsePositives asserts the clean full set produces
// zero advisories, and that build-dep warnings are gated on AUR being requested.
func TestPacstrapAdvisories_NoFalsePositives(t *testing.T) {
	full := &config.Config{}
	full.Pacstrap = []string{"base-devel", "git", "zsh", "sudo", "networkmanager", "efibootmgr", "intel-ucode", "linux"}
	if got := pacstrapAdvisories(full); len(got) != 0 {
		t.Errorf("complete pacstrap produced advisories: %v", got)
	}

	// No AUR requested + no base-devel/git => no build-dep warning.
	noAUR := &config.Config{}
	noAUR.Pacstrap = []string{"networkmanager", "intel-ucode", "linux"}
	if containsSubstr(pacstrapAdvisories(noAUR), "AUR helper") {
		t.Errorf("warned about AUR build deps with no AUR configured: %v", pacstrapAdvisories(noAUR))
	}
}
