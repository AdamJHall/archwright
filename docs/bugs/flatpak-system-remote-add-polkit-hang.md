# Bug: the flatpak stage hangs on a polkit password prompt (system-wide `flatpak remote-add`/install as a non-root user)

**Status:** open — found by the automated VM e2e harness (`test/e2e/vm/`, descriptor `features-flatpak`), 2026-06-23
**Area:** `internal/stages/flatpak.go`
**Severity:** high — Phase B `bootstrap` **hangs indefinitely** at the flatpak stage in any
non-graphical session (first-boot TTY, SSH, the e2e harness), because a system-wide flatpak
operation run as a normal user requires polkit authentication and there is no agent to answer it.

## Summary

The flatpak stage runs `flatpak remote-add` (and would then run `flatpak install`) as the
**unprivileged user** against the **system** flatpak installation (the default scope). That
needs the polkit action `org.freedesktop.Flatpak.modify-repo`, which prompts for a password.
With no graphical polkit agent (and stdin not a usable tty), the command blocks forever on
`Password:` and `bootstrap` never completes.

## Evidence (from the `features-flatpak` VM run)

Phase B serial, at the flatpak stage:

```
━━ [4/11] 30 · flatpak ━━━...
→ flatpak remote-add --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo
Note that the directories '/var/lib/flatpak/exports/share' ... are not in the search path ...
==== AUTHENTICATING FOR org.freedesktop.Flatpak.modify-repo ====
Authentication is required to modify a system repository
Authenticating as: e2e
Password:
```

…then nothing — the run hit the harness Phase-B timeout (`timed out waiting for
E2E_RESULT`). The `→` prefix shows the command ran **unprivileged** (the runner's `Cmd`, not
`Root`), so it dropped into polkit auth.

## Reproduction

1. A config with a flatpak remote + app, e.g. `test/e2e/vm/configs/features-flatpak.yaml`
   (flathub + `com.github.tchx84.Flatseal`).
2. Run Phase B `archwright bootstrap` in a **non-graphical** session (TTY/SSH/headless) as the
   user — i.e. the normal first-boot situation before a desktop/polkit agent is running.
3. The flatpak stage blocks on the polkit `Password:` prompt for
   `org.freedesktop.Flatpak.modify-repo`.

(`task vm-e2e -- features-flatpak` reproduces it; the stage hangs until the 2700s Phase-B
timeout.)

## Expected behavior

The flatpak stage completes unattended: remotes are added and apps installed without an
interactive polkit prompt, in a plain TTY/headless session.

## Actual behavior

`flatpak remote-add` (system scope, as the user) blocks on a polkit `Password:` prompt;
`bootstrap` hangs.

## Fix direction

Pick one of:

- **Per-user scope:** run `flatpak --user remote-add …` and `flatpak --user install …`. The
  `--user` installation needs no polkit/root. This is usually the right default for a
  single-user desktop and matches running Phase B as the user. (Note: app launchers/exports
  differ slightly for `--user`.)
- **Privileged scope:** run the system-wide operations via the runner's `Root` (sudo), e.g.
  `sudo flatpak remote-add …` / `sudo flatpak install -y …`. Root skips the polkit prompt.
- Either way, pass non-interactive flags so a later `flatpak install` can't prompt:
  `--noninteractive` (or `-y/--assumeyes`).

Decide the intended scope (`--user` vs system) deliberately — it changes where apps land and
how they're exported. Whichever is chosen, the stage must be non-interactive.

## Validation after a fix

`task vm-e2e -- features-flatpak` reaches `E2E_RESULT=PASS` with the configured app present
(`flatpak list` shows `com.github.tchx84.Flatseal`). `lib/features.sh`'s `flatpak` token
checks exactly that.

## Related

The harness's 2700s Phase-B timeout means a polkit hang wastes the full window. Independent of
this bug, archwright stages that shell out should never be able to block on an interactive
prompt during `bootstrap` — worth auditing other stages (e.g. anything piping to a tool that
might prompt) for the same hazard.
