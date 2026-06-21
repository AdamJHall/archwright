package stages

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/AdamJHall/archwright/internal/archinstall"
	"github.com/AdamJHall/archwright/internal/config"
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
	for _, w := range pacstrapAdvisories(ctx.Cfg) {
		ui.Warn(w)
	}
	checkArchinstall(ctx.R.DryRun)
	return nil
}

// pacstrapAdvisories returns advisory messages about recommended-but-absent
// pacstrap entries, conditioned on what the rest of the config implies. It is a
// guardrail, never an injection: it changes no installed bytes, it only reports.
// Returned as a slice (rather than printing directly) so it is unit-testable
// without capturing logger output.
//
//   - base-devel/git absent AND an AUR helper/list is configured -> warn (the
//     Phase-B yay/paru build needs them).
//   - networkmanager absent -> warn (the rendered network is NetworkManager, so
//     without it there is no first-boot network).
//   - no CPU microcode (*-ucode) present -> info.
//   - no kernel package present AND no baseline kernel declared elsewhere -> warn
//     (the system may not boot).
func pacstrapAdvisories(cfg *config.Config) []string {
	var out []string
	has := func(pkg string) bool { return slices.Contains(cfg.Pacstrap, pkg) }

	wantsAUR := cfg.AurHelper != "" || len(cfg.AUR) > 0
	if wantsAUR && (!has("base-devel") || !has("git")) {
		out = append(out, "pacstrap is missing base-devel and/or git, but an AUR helper is configured — the Phase B yay/paru build will fail")
	}

	if !has("networkmanager") {
		out = append(out, "pacstrap is missing networkmanager — the installed system uses NetworkManager, so there may be no network at first boot")
	}

	hasUcode := false
	for _, p := range cfg.Pacstrap {
		if strings.HasSuffix(p, "-ucode") {
			hasUcode = true
			break
		}
	}
	if !hasUcode {
		out = append(out, "pacstrap has no CPU microcode (intel-ucode/amd-ucode) — recommended so the bootloader folds it into the early initramfs")
	}

	if !hasKernel(cfg.Pacstrap) && len(cfg.Kernel.Packages) == 0 {
		out = append(out, "pacstrap has no kernel package and no kernel.packages are declared — the system may not boot")
	}

	return out
}

// hasKernel reports whether the pacstrap list contains a likely kernel package
// ("linux" or any "linux-*" such as linux-lts/linux-zen).
func hasKernel(pkgs []string) bool {
	for _, p := range pkgs {
		if p == "linux" || strings.HasPrefix(p, "linux-") {
			return true
		}
	}
	return false
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
