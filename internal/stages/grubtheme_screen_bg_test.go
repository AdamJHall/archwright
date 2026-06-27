package stages

import (
	"strings"
	"testing"
)

// screen maps to the installer's -s flag.
func TestGrubTheme_Screen(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "grub-theme", `
grub:
  theme:
    source: vinceliuice
    name: tela
    screen: 4k
`)
	mustContain(t, plan, "sudo ./install.sh -t tela -s 4k")
}

// A background URL is fetched into the checkout root as background.jpg before the
// installer runs (install.sh picks it up automatically when present).
func TestGrubTheme_BackgroundURL(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "grub-theme", `
grub:
  theme:
    source: vinceliuice
    name: tela
    background: https://raw.githubusercontent.com/me/dotfiles/main/background.jpg
`)
	mustContain(t, plan,
		`curl -fsSL "https://raw.githubusercontent.com/me/dotfiles/main/background.jpg" -o background.jpg && sudo ./install.sh -t tela`,
	)
}

// A local background path is copied into the checkout root instead of fetched.
func TestGrubTheme_BackgroundLocalPath(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "grub-theme", `
grub:
  theme:
    source: vinceliuice
    name: tela
    background: /home/me/wallpapers/grub.jpg
`)
	mustContain(t, plan, `cp -f "/home/me/wallpapers/grub.jpg" background.jpg && sudo ./install.sh -t tela`)
	if j := strings.Join(plan, "\n"); strings.Contains(j, "curl") {
		t.Errorf("local path should be copied, not curled:\n%s", j)
	}
}

// With neither set, the command is unchanged from today (default-preserves-behaviour).
func TestGrubTheme_NoScreenOrBackground(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "grub-theme", `
grub:
  theme:
    source: vinceliuice
    name: tela
`)
	j := strings.Join(plan, "\n")
	mustContain(t, plan, "sudo ./install.sh -t tela")
	if strings.Contains(j, "-s ") || strings.Contains(j, "background.jpg") {
		t.Errorf("unset screen/background should not alter the install command:\n%s", j)
	}
}
