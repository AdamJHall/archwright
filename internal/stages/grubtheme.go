package stages

import (
	"fmt"

	"github.com/AdamJHall/archwright/internal/ui"
)

// grubtheme is the Phase B 60-grub-theme equivalent: install and select a GRUB
// theme. source: vinceliuice | url | none.
type grubtheme struct{}

func init() { register(grubtheme{}) }

func (grubtheme) Order() int   { return 60 }
func (grubtheme) Name() string { return "grub-theme" }
func (grubtheme) Phase() Phase { return Bootstrap }

func (grubtheme) Run(ctx *Context) error {
	t := ctx.Cfg.GRUB.Theme
	switch t.Source {
	case "", "none":
		ui.Warn("grub.theme.source is none — skipping GRUB theme")
		return nil

	case "vinceliuice":
		if t.Name == "" {
			return fmt.Errorf("grub.theme.name required for source 'vinceliuice'")
		}
		// Clone + run the upstream installer (it sets GRUB_THEME and regenerates).
		if err := cloneBuild(ctx,
			"--depth 1 https://github.com/vinceliuice/grub2-themes",
			fmt.Sprintf("sudo ./install.sh -t %s", t.Name)); err != nil {
			return err
		}
		ui.OK("GRUB theme %q installed", t.Name)
		return nil

	case "url":
		if t.Name == "" || t.URL == "" {
			return fmt.Errorf("grub.theme.name and grub.theme.url required for source 'url'")
		}
		dest := "/boot/grub/themes/" + t.Name
		if err := ctx.R.Shell(fmt.Sprintf(
			`tmp="$(mktemp -d)" && curl -fsSL %[1]q -o "$tmp/theme.tar.gz" && `+
				`sudo mkdir -p %[2]q && sudo tar -xf "$tmp/theme.tar.gz" -C %[2]q --strip-components=1 && rm -rf "$tmp"`,
			t.URL, dest)); err != nil {
			return err
		}
		if err := ctx.R.Shell(fmt.Sprintf(
			`sudo sed -i '/^#\?GRUB_THEME=/d' /etc/default/grub && `+
				`echo 'GRUB_THEME="%s/theme.txt"' | sudo tee -a /etc/default/grub >/dev/null && `+
				`sudo grub-mkconfig -o /boot/grub/grub.cfg`, dest)); err != nil {
			return err
		}
		ui.OK("GRUB theme %q installed from url", t.Name)
		return nil

	default:
		return fmt.Errorf("unknown grub.theme.source: %s", t.Source)
	}
}
