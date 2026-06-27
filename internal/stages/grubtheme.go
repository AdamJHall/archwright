package stages

import (
	"fmt"
	"strings"

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
		// cloneBuild runs as the user (it mktemp/git-clones into the user's tree),
		// so install.sh keeps its inner `sudo` to gain root for the system writes.
		install := fmt.Sprintf("sudo ./install.sh -t %s", t.Name)
		if t.Screen != "" {
			install += " -s " + t.Screen
		}
		// install.sh uses a file named background.jpg in the checkout root as the
		// background, if present. Fetch (URL) or copy (local path) it into place
		// before running the installer; cloneBuild runs us from the checkout root.
		if t.Background != "" {
			var fetch string
			if strings.HasPrefix(t.Background, "http://") || strings.HasPrefix(t.Background, "https://") {
				fetch = fmt.Sprintf("curl -fsSL %q -o background.jpg", t.Background)
			} else {
				fetch = fmt.Sprintf("cp -f %q background.jpg", t.Background)
			}
			install = fetch + " && " + install
		}
		if err := cloneBuild(ctx,
			"--depth 1 https://github.com/vinceliuice/grub2-themes", install); err != nil {
			return err
		}
		ui.OK("GRUB theme %q installed", t.Name)
		return nil

	case "url":
		if t.Name == "" || t.URL == "" {
			return fmt.Errorf("grub.theme.name and grub.theme.url required for source 'url'")
		}
		dest := "/boot/grub/themes/" + t.Name
		// Whole pipeline runs as root via RootShell, so no inner per-command sudo.
		if err := ctx.R.RootShell(fmt.Sprintf(
			`tmp="$(mktemp -d)" && curl -fsSL %[1]q -o "$tmp/theme.tar.gz" && `+
				`mkdir -p %[2]q && tar -xf "$tmp/theme.tar.gz" -C %[2]q --strip-components=1 && rm -rf "$tmp"`,
			t.URL, dest)); err != nil {
			return err
		}
		if err := ctx.R.RootShell(fmt.Sprintf(
			`sed -i '/^#\?GRUB_THEME=/d' /etc/default/grub && `+
				`echo 'GRUB_THEME="%s/theme.txt"' >>/etc/default/grub && `+
				`grub-mkconfig -o /boot/grub/grub.cfg`, dest)); err != nil {
			return err
		}
		ui.OK("GRUB theme %q installed from url", t.Name)
		return nil

	default:
		return fmt.Errorf("unknown grub.theme.source: %s", t.Source)
	}
}
