package stages

import "github.com/AdamJHall/archwright/internal/ui"

// snapper is the Phase B 25-snapper stage: provision Snapper for a btrfs root.
//
// It is a clean no-op (the project's uniform degrade-to-no-op pattern, mirroring
// dotfiles.go's selector) unless the effective disk layout is btrfs AND
// btrfs.snapshots is "snapper". When active it installs snapper + snap-pac,
// creates the root config, applies a sane retention policy, and enables the
// timeline/cleanup timers.
//
// Order 25 lands after packages (20) — so snapper itself can come from the
// configured package set — and well before dotfiles (80)/setup (85).
type snapper struct{}

func init() { register(snapper{}) }

func (snapper) Order() int   { return 25 }
func (snapper) Name() string { return "snapper" }
func (snapper) Phase() Phase { return Bootstrap }

// active reports whether Snapper should be provisioned: a btrfs layout with
// btrfs.snapshots == "snapper". The nil-Btrfs guard makes a btrfs layout with no
// btrfs block a clean skip rather than a panic.
func (snapper) active(ctx *Context) bool {
	d := ctx.Cfg.Disks
	if d.EffectiveLayout() != "btrfs" || d.Btrfs == nil {
		return false
	}
	return d.Btrfs.Snapshots == "snapper"
}

func (s snapper) Run(ctx *Context) error {
	if !s.active(ctx) {
		ui.Info("snapper not requested for this layout — skipping")
		return nil
	}

	// snapper provides the `snapper` binary; ensureTool skips the install when it
	// is already on PATH. snap-pac is a pacman/yay hook with no binary, so it is
	// always (idempotently) installed directly.
	if err := ensureTool(ctx, "snapper", "snapper"); err != nil {
		return err
	}
	if err := ctx.R.Root("pacman", "-S", "--needed", "--noconfirm", "snap-pac"); err != nil {
		return err
	}

	// Create the root config only when it does not already exist — create-config
	// errors out on an existing config, so grep-guard it for idempotent re-runs.
	if err := ctx.R.RootShell(
		`snapper list-configs 2>/dev/null | grep -qw root || ` +
			`snapper -c root create-config /`,
	); err != nil {
		return err
	}

	// Sane retention: keep the timeline tidy and cap the number of snapshots.
	// Best-effort (Try) — a freshly-created config has these set, and a re-run
	// must not fail the stage if set-config is fussy about an already-present key.
	ctx.R.TryRoot("snapper", "-c", "root", "set-config",
		"TIMELINE_LIMIT_HOURLY=5",
		"TIMELINE_LIMIT_DAILY=7",
		"TIMELINE_LIMIT_WEEKLY=4",
		"TIMELINE_LIMIT_MONTHLY=6",
		"TIMELINE_LIMIT_YEARLY=0",
		"NUMBER_LIMIT=50",
	)

	// Enable the periodic timeline + cleanup timers.
	if err := ctx.R.Root("systemctl", "enable", "--now", "snapper-timeline.timer"); err != nil {
		return err
	}
	if err := ctx.R.Root("systemctl", "enable", "--now", "snapper-cleanup.timer"); err != nil {
		return err
	}

	ui.OK("snapper provisioned for btrfs root")
	return nil
}
