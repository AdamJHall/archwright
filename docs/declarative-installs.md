# Declarative install sources — config is the source of truth

A change spec for making **the config file the complete, explicit record of everything
installed**, removing the hardcoded package/remote/kernel defaults baked into the binary.
Today the code silently prepends a base package set to pacstrap, always registers the
Flathub remote, and always pacstraps the stock `linux` kernel — so `config.yaml` is *not*
the full truth of what lands on disk. This spec closes that gap.

## Principle (and its one deliberate exception)

> Nothing is installed unless `config.yaml` explicitly says so.

**Decision (confirmed):** this applies to the explicit **lists** — packages, flatpak
remotes, the baseline kernel. It does **not** apply to **feature-implied tooling**: when you
set `dotfiles.manager: chezmoi`, installing `chezmoi` is the *consequence of an explicit
choice*, so `ensureTool` stays; likewise `yay` may install `git`/`base-devel` to build the
AUR helper you asked for. The feature selection **is** the declaration. We keep those two
(`ensureTool`, the yay build deps) exactly as they are — see "Out of scope" below.

## Inventory — every place code installs something the config doesn't list

| # | Site | What it injects today | In scope |
|---|------|-----------------------|----------|
| D1 | `archinstall.go:233` `bootstrapPackages` + `cfg.PacstrapExtra` (`archinstall.go:285`) | `base-devel git zsh sudo networkmanager efibootmgr` always prepended to pacstrap | **yes — headline** |
| D2 | `flatpak.go:28` (hardcoded Flathub remote) + `:37` (`install … flathub …`) | the `flathub` remote, always added; every app installed *from* `flathub` | **yes — headline** |
| D3 | `archinstall.go:289` `Kernels: []string{"linux"}` | stock `linux` kernel always pacstrapped (then maybe removed by `kernel.replace_stock`) | **yes** |
| D4 | `yay.go:27` `pacman -S … git base-devel` | build deps for the AUR helper | no (feature-implied) |
| D5 | `helpers.go:20` `ensureTool` | a feature's tool (chezmoi/flatpak/snapper/plymouth…) when its binary is absent | no (feature-implied) |

---

## D1 — explicit `pacstrap` list (replaces `pacstrap_extra` + `bootstrapPackages`)

**Today:** `Build` does `pkgs := append(bootstrapPackages, cfg.PacstrapExtra...)`
(`archinstall.go:285`). The base six packages live in code; the user can only *add*.

**Change:** a single required `pacstrap` list that is rendered verbatim. Nothing prepended.

```yaml
# config.example.yaml
pacstrap:                          # the COMPLETE Phase-A pacstrap set (nothing added in code)
  - base-devel                     # needed by Phase B to build the AUR helper
  - git                            # same
  - zsh                            # the user's login shell (system.shell)
  - sudo                           # Phase B runs as the user via sudo
  - networkmanager                 # network at first boot (see system.network)
  - efibootmgr                     # UEFI boot entry management
  - intel-ucode                    # CPU microcode (or amd-ucode) — folded into initramfs
```

```go
// config.go — replace PacstrapExtra
Pacstrap []string `yaml:"pacstrap" validate:"required,min=1,dive,required"`
// DELETE: PacstrapExtra []string `yaml:"pacstrap_extra"`
```

```go
// archinstall.go Build — replace the append; DELETE the bootstrapPackages var
Packages: append([]string(nil), cfg.Pacstrap...),
```

### Hazard + guardrail (advisory, never injection)
Omitting `base-devel`/`git` breaks the Phase-B yay build; omitting `networkmanager` (with
`network: nm`) means no first-boot network; omitting microcode/kernel/`efibootmgr` are
boot-quality footguns. We do **not** silently re-add anything. Instead, **preflight emits a
warning** listing recommended-but-absent packages, conditioned on what the rest of the
config implies:
- `base-devel`/`git` absent **and** `aur:`/`aur_helper` set → warn (yay won't build).
- `networkmanager` absent **and** `system.network` is `nm`/unset → warn.
- no microcode (`*-ucode`) present → info.
- no kernel package present in `pacstrap` **and** `kernel.base` empty (see D3) → warn
  (system may not boot).

This is a guardrail, not a default — it changes no installed bytes, only prints. Lives in
`preflight.go` (Phase A) so it's seen before the destructive step.

### Migration
`pacstrap_extra` → `pacstrap`, and the user folds the old six base packages in explicitly.
A one-line note in the release/PR and the example config covers it. Optionally: keep
accepting `pacstrap_extra` for one release as a deprecated alias that errors with a clear
"rename to `pacstrap` and add the base set" message (recommended — `pacstrap_extra` with no
`pacstrap` is unambiguous to detect).

---

## D2 — explicit `flatpak_remotes` + per-app remote (no built-in Flathub)

**Today:** `flatpak.go` always runs `remote-add … flathub …` and then installs **every** app
from `flathub` (`:37`). `flatpak_remotes` only *adds* extras; Flathub is implicit. An app
from a non-Flathub remote is impossible to express.

**Change:**
1. `flatpak_remotes` is the **complete** remote list — nothing added in code. If you install
   from Flathub you list Flathub.
2. Each flatpak names the remote it comes from, so the install isn't pinned to `flathub`.
   Use a `remote:appid` ref (matches `flatpak install <remote> <appid>`), keeping the list
   flat and the migration mechanical.

```yaml
flatpak_remotes:                   # COMPLETE list; flathub is no longer implicit
  - { name: flathub, url: https://flathub.org/repo/flathub.flatpakrepo }

flatpaks:                          # each app names its remote (remote must be declared above)
  - flathub:com.spotify.Client
  - flathub:org.mozilla.firefox
  - flathub:com.stremio.Stremio
```

```go
// flatpak.go Run — no hardcoded remote-add; add exactly the declared remotes,
// then install each app from its named remote.
for _, rem := range ctx.Cfg.FlatpakRemotes {
    if err := ctx.R.Cmd("flatpak", "remote-add", "--if-not-exists", rem.Name, rem.URL); err != nil {
        return err
    }
}
for _, app := range ctx.Cfg.Flatpaks {
    remote, appid, ok := strings.Cut(app, ":")
    if !ok { /* validation guarantees this; defensive */ }
    if err := ctx.R.Cmd("flatpak", "install", "-y", "--noninteractive", remote, appid); err != nil {
        return err
    }
}
```

> Note: this installs apps one-per-command instead of one batched `flatpak install … <apps>`.
> That's the cost of per-app remotes; acceptable (flatpak installs are already slow and the
> per-app command keeps remote attribution unambiguous). If batching matters, group apps by
> remote and emit one `install` per remote — a pure optimization, same observable result.

### Validation (`config.go` semanticErrors)
- every `flatpaks` entry must be `remote:appid` (contains exactly one `:`, both halves
  non-empty);
- its `remote` must be a `name` in `flatpak_remotes` (catches typos / undeclared remotes at
  `validate` time, before anything runs).

`FlatpakRemote` struct is unchanged (still `{name, url}`); only the doc comment ("the
built-in flathub remote is always added") is corrected.

### Migration
`com.spotify.Client` → `flathub:com.spotify.Client`, and add the Flathub remote line. The
validator's error message names the fix.

---

## D3 — explicit baseline kernel (`Kernels` no longer hardcoded `linux`)

**Today:** `Build` hardcodes `Kernels: []string{"linux"}` (`archinstall.go:289`). The only
control is `kernel.replace_stock`, which *removes* stock `linux` post-pacstrap after a custom
kernel is installed in the chroot — i.e. you pacstrap a kernel you didn't ask for, then rip
it out. Not declarative, and wasteful.

**Change:** add `kernel.base` — the kernel(s) archinstall pacstraps. Render it into the
archinstall `Kernels` field. This makes the baseline explicit and removes the
pacstrap-then-remove dance for the common "custom kernel only" case.

```yaml
kernel:
  base: [linux]                    # what archinstall pacstraps for a bootable baseline
  packages: [linux-cachyos, linux-cachyos-headers]   # extra kernels added in the chroot
  default: linux-cachyos           # GRUB/loader default; must be in base ∪ packages
  # replace_stock is now redundant for most cases: set base: [linux-cachyos-...] directly.
```

```go
// archinstall.go Build
Kernels: kernelBase(cfg),          // cfg.Kernel.Base, validated non-empty
```

### Validation
- `kernel.base` required, `min=1` (a system with no kernel doesn't boot);
- `kernel.default`, when set, must be in `kernel.base ∪ kernel.packages` (extend the existing
  `kernel.default ∈ packages` check at `config.go:427`).

### Compatibility
`replace_stock` stays (still valid: pacstrap `linux`, install cachyos in chroot, remove
`linux`) but the example steers people to `base: [<their kernel>]` instead. Keeping
`replace_stock` avoids forcing a flag-day for configs that rely on it. **Caveat:** a kernel
in `base` must be in the official repos (archinstall pacstraps from the live ISO's repos,
before the custom `repos:` are configured in the chroot) — so `linux-cachyos` belongs in
`kernel.packages` (chroot, post-repo-setup), not `base`. Document this; the validator can't
know repo membership.

---

## Out of scope (kept by decision — feature-implied, not list-driven)

- **D4 `yay` build deps** (`git base-devel`) and **D5 `ensureTool`** (chezmoi/flatpak/
  snapper/plymouth…): these install a tool **because the config explicitly enabled the
  feature that needs it**. The feature selection is the declaration, so they stay as-is. No
  change. (If the strict-everywhere stance is ever wanted, the move is: `ensureTool` →
  `ensurePresent` that errors instead of installing, and require the tools in `pacstrap`/
  `packages`. Not doing that now.)
- **`packages` (Phase B)** already has no code-side additions — it's rendered verbatim by
  `packages.go`. No change; it's the model the others move toward.

---

## Summary

| # | Change | Files | Value | Cost |
|---|--------|-------|-------|------|
| D1 | `pacstrap` replaces `pacstrap_extra` + drop `bootstrapPackages`; advisory preflight warnings | `config.go`, `archinstall.go`, `preflight.go`, example | **HIGH** | LOW-MED |
| D2 | `flatpak_remotes` complete (no built-in flathub) + per-app `remote:appid` + validation | `config.go`, `flatpak.go`, example | **HIGH** | LOW-MED |
| D3 | `kernel.base` replaces hardcoded `Kernels: [linux]`; validation + compat | `config.go`, `archinstall.go`, example | MED | LOW |
| D4/D5 | feature-implied installs (yay deps, ensureTool) | — | — | **keep, no change** |

## Tests (TDD; mirror the existing harness)
- **D1:** golden render — `pacstrap: [a,b,c]` renders `Packages` == exactly `[a,b,c]` (no
  prepend); `config_test.go` table case for `required,min=1`; `preflight` advisory tests
  (warn when base-devel+aur set; warn when no kernel) asserting on a captured warning, not
  `.Plan`. Update the existing golden fixtures (their `Packages` field changes) — do it as a
  separate commit so the diff is purely the removed base set, per the Wave 2 golden discipline.
- **D2:** `flatpak.go` dry-run plan — declared remotes added, each app installed from its
  named remote, **no** unconditional flathub `remote-add`; `config_test.go` cases for the
  `remote:appid` shape and the undeclared-remote error.
- **D3:** golden render — `Kernels` reflects `kernel.base`; `config_test.go` for `base`
  `min=1` and the extended `default ∈ base ∪ packages` rule.

All via `go test ./...` in dry-run (no disk touches), `go vet ./...`,
`go build -o archwright .`, `gofmt -l .` clean.

## Sequencing note
D1/D2/D3 are independent and touch the now-familiar shared hotspots (`config.go`,
`archinstall.go`, the example) in disjoint regions — they fit the parallel-worktree model
(one agent each, distinct anchors, new `*_test.go` files only). Could land as its own wave or
fold into Wave 4 alongside the snapper/`--from`-`--to` work; the TUI agent doesn't touch any
of these files, so there's no contention with it.
