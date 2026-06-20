package stages

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/AdamJHall/archwright/internal/archinstall"
	"github.com/AdamJHall/archwright/internal/ui"
)

// preflight is the Phase A 00-preflight equivalent: assert UEFI, validate the
// config, and confirm archinstall is present (Phase A now delegates to it),
// warning if the live ISO ships a version other than the one we modelled.
type preflight struct{}

func init() { register(preflight{}) }

func (preflight) Order() int   { return 0 }
func (preflight) Name() string { return "preflight" }
func (preflight) Phase() Phase { return Install }

func (preflight) Run(ctx *Context) error {
	if _, err := os.Stat("/sys/firmware/efi"); err != nil {
		return fmt.Errorf("not booted in UEFI mode (/sys/firmware/efi missing)")
	}
	ui.OK("UEFI boot confirmed")
	if err := ctx.Cfg.Validate(); err != nil {
		return err
	}
	ui.OK("config validated")
	checkArchinstall(ctx.R.DryRun)
	return nil
}

// checkArchinstall verifies the installer is available and flags a version
// mismatch against archinstall.Version (the schema we render is version-coupled,
// so a different version may reject our JSON). Non-fatal: warns and continues.
func checkArchinstall(dryRun bool) {
	out, err := exec.Command("archinstall", "--version").Output()
	if err != nil {
		if dryRun {
			ui.Warn("archinstall not found (dry-run; will be present on the live ISO)")
			return
		}
		ui.Warn("archinstall not found on PATH; install it before running", "expected", archinstall.Version)
		return
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, archinstall.Version) {
		ui.Warn("archinstall version differs from the one this was modelled against; JSON schema may have changed",
			"found", got, "modelled", archinstall.Version)
		return
	}
	ui.OK("archinstall %s present", got)
}
