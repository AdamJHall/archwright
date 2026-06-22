package stages

import "github.com/AdamJHall/archwright/internal/ui"

// services is the Phase B 90-services stage: enable the configured systemd units
// so they start on the next boot. It runs last (after dotfiles + setup) so any
// unit a dotfiles/setup step installs is already present to be enabled.
//
// System units are enabled as root (`systemctl enable`); user units are enabled
// unprivileged (`systemctl --user enable`). Enabling is idempotent, so the stage
// is safe to re-run. We deliberately don't `--now`-start them: the typical case
// is a login/display-manager unit (e.g. plasmalogin.service) that should take
// over on the next boot, not be started underneath the current session.
type services struct{}

func init() { register(services{}) }

func (services) Order() int   { return 90 }
func (services) Name() string { return "services" }
func (services) Phase() Phase { return Bootstrap }

func (services) Run(ctx *Context) error {
	sys := ctx.Cfg.Services.Enable
	usr := ctx.Cfg.Services.User
	if len(sys) == 0 && len(usr) == 0 {
		ui.Warn("no services in config — skipping")
		return nil
	}

	if len(sys) > 0 {
		if err := ctx.R.Root("systemctl", append([]string{"enable"}, sys...)...); err != nil {
			return err
		}
	}
	if len(usr) > 0 {
		if err := ctx.R.Cmd("systemctl", append([]string{"--user", "enable"}, usr...)...); err != nil {
			return err
		}
	}

	ui.OK("services enabled")
	return nil
}
