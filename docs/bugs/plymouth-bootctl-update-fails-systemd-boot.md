# Bug: Phase B `regenerateBootConfig` runs `bootctl update` on systemd-boot and fails, aborting bootstrap (via the always-on plymouth stage)

**Status:** resolved 2026-06-30 — Plymouth moved to archinstall (Phase A) on the 4.4 bump.
The Phase B `plymouth` stage, `regenerateBootConfig`/`ensureKernelParam` helpers, and the
`bootctl update --graceful` workaround described below were all removed; archinstall now installs
and configures Plymouth (hook, `quiet splash`, theme) during the install, so no Phase B step ever
runs `bootctl update`. The failure path no longer exists. (Originally found by the automated VM e2e
harness `test/e2e/vm/`, descriptor `sdboot-lvm`, 2026-06-23.)
**Area:** `internal/stages/helpers.go` (`regenerateBootConfig`); surfaced via `internal/stages/plymouth.go`
**Severity:** high — Phase B `bootstrap` **fails on any systemd-boot system** at the plymouth
stage (and any other stage that regenerates boot config), even with a default config.

## Summary

On a systemd-boot install, the Phase B `plymouth` stage aborts with
`ERRO stage plymouth: sudo: exit status 1`. The failing command is **`sudo bootctl update`**,
run by `regenerateBootConfig` for the systemd-boot bootloader. Because the plymouth stage
runs **unconditionally** (it defaults the theme to `bgrt` when none is configured), this
breaks `bootstrap` for systemd-boot configs out of the box. The same `regenerateBootConfig`
is also called by the grub-theme stage and the kernel path, so they would hit it too.

The install itself is fine: the e2e validation that runs afterward passes (root/ESP/LVM,
`bootctl is-installed` → "systemd-boot installed", packages, yay). Only the boot-config
regeneration command fails.

## Root cause

`internal/stages/helpers.go`:

```go
func regenerateBootConfig(ctx *Context) error {
	if ctx.Cfg.Bootloader.EffectiveKind() == "systemd-boot" {
		return ctx.R.Root("bootctl", "update")     // <-- returns exit status 1 here
	}
	return ctx.R.Root("grub-mkconfig", "-o", "/boot/grub/grub.cfg")
}
```

`bootctl update` re-installs the systemd-boot binary into the ESP **only if** the bundled
version is newer than the installed one; when archinstall already installed the current
version it has nothing to do and exits non-zero (rather than a no-op success). archwright
treats that non-zero exit as a stage failure and aborts `bootstrap`.

`internal/stages/plymouth.go` makes this reachable on every run:

```go
func (plymouth) Run(ctx *Context) error {
	theme := ctx.Cfg.Plymouth.Theme
	if theme == "" {
		theme = "bgrt"          // <-- stage is NOT gated off when unconfigured
	}
	...
	return regenerateBootConfig(ctx)
}
```

So even a config with no `plymouth:` block installs plymouth, edits the cmdline, and runs
`bootctl update`.

## Evidence (from the `sdboot-lvm` VM run)

```
→ [6/11] 50 ⟫ plymouth
  → sudo pacman -S --needed --noconfirm plymouth        (ok)
  → sed ... /etc/mkinitcpio.conf  (add plymouth hook)   (ok)
  → ... /etc/kernel/cmdline  (add quiet / splash)        (ok)
  → sudo plymouth-set-default-theme -R bgrt              (ok, rebuilds initramfs)
  → sudo bootctl update                                  (FAILS)
ERRO stage plymouth: sudo: exit status 1
E2E_BOOTSTRAP_RC=1
```

(The post-bootstrap validation still ran and reported `0 failures`, including
`OK: systemd-boot installed` — so the system is healthy; only the stage command failed.)

## Reproduction

1. Install any systemd-boot config (e.g. `task vm-e2e -- sdboot-lvm`, or set
   `bootloader.kind: systemd-boot` on any layout).
2. Run Phase B `archwright bootstrap`.
3. It aborts at the plymouth stage; the failing command is `sudo bootctl update`.

## Expected behavior

`bootstrap` completes on systemd-boot. Regenerating boot config should be a no-op-tolerant
success when there is nothing to update.

## Actual behavior

`bootstrap` aborts at the plymouth (or any boot-config-regenerating) stage because
`bootctl update` exits non-zero when the loader is already current.

## Fix direction

- In `regenerateBootConfig`, make the systemd-boot path tolerant of the "already current"
  case — e.g. `bootctl update --graceful`, or treat the no-update exit code as success
  (best-effort via `ctx.R.Try`), or skip `bootctl update` entirely (the loader was just
  installed by archinstall; cmdline changes go to `/etc/kernel/cmdline` which systemd-boot
  reads directly, so a forced binary update isn't needed for archwright's edits). Confirm the
  exact `bootctl update` exit semantics against the installed systemd version.
- Consider whether the **plymouth stage should be gated** when no `plymouth:` config is
  present, instead of always defaulting to `bgrt` — running it unconditionally is what makes
  every systemd-boot bootstrap hit this path. (Decide intended behavior; if "plymouth on by
  default" is desired, keep it but make the boot-config step robust.)
- This sits squarely in the reverse-engineered / VM-validation-pending systemd-boot path
  (`CLAUDE.md`): validate the fix with `task vm-e2e -- sdboot-lvm` (and `sdboot-plain`)
  reaching `E2E_RESULT=PASS` with `E2E_BOOTSTRAP_RC=0`.
