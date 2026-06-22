# Archwright

> ⚠️ **Heads up:** this is vibe coded and probably trash. It's opinionated, **not intended
> for production use** — only for personal use and messing around. There are almost certainly
> much better tools out there to handle this. Use at your own risk.

A declarative way to rebuild an Arch Linux machine from bare disks to a
themed KDE desktop. One config file (`config.yaml`) drives a single static Go binary
(`archwright`).

Phase A renders your `config.yaml` into an [archinstall](https://github.com/archlinux/archinstall)
configuration and lets the official installer do the partitioning, LVM, pacstrap and
bootloader install. Phase B then does the post-install customization (packages, flatpaks,
1Password, Plymouth, GRUB/KDE theming, dotfiles) directly.

It does **not** try to be NixOS — there's no purity, no rollback, no DSL. It's plain YAML
you can read top to bottom plus a binary that orchestrates archinstall and the usual Arch
tools (`yay`, `flatpak`, …), designed for the "I had to reinstall again" workflow.
No `bash`/`yq`/`jq` runtime dependencies — just the binary.

> **archinstall version coupling.** archinstall's JSON config is *not* a stable API; its
> schema changes between releases. We render against the version in
> [`internal/archinstall`](internal/archinstall/archinstall.go) (`Version`), and preflight
> warns if the live ISO ships a different one. Validate the generated config against a real
> archinstall run in a VM (see [Testing in a VM](#testing-in-a-vm-recommended-before-real-hardware))
> before trusting it on hardware.

## Build / install

```sh
go build -o archwright .          # local build
go install ./...                  # into $GOBIN

# stamped build (what releases do):
go build -ldflags "-s -w -X main.version=$(git describe --tags --always)" -o archwright .
```

Or grab a prebuilt binary from a GitHub release (see [Releases](#releases)).

## Commands

```
archwright install   [--dry-run] [--only <stage>] [--config <ref>]... [--yes]
archwright bootstrap [--dry-run] [--only <stage>] [--config <ref>]...
archwright validate  [--config <ref>]...
archwright render    [--config <ref>]... -o <out.yaml>
archwright --version
```

| Command     | Phase | Run as | What it does |
|-------------|-------|--------|--------------|
| `install`   | A     | root, from the Arch live ISO | (optional) pick mirrors with reflector; probe disk geometry; render an archinstall config (disk 1 = ESP+swap+LVM-PV partitions; extra disks = full-disk PVs; one VG→XFS root LV) + credentials; run `archinstall --silent`; then in the target chroot configure custom repos (e.g. CachyOS) + install custom kernels (replace stock, set GRUB default) and stage the binary+**flattened config** for Phase B |
| `bootstrap` | B     | your user, after reboot | yay, packages, flatpaks, AUR, Plymouth, GRUB theme, KDE customization, `chezmoi init --apply`, then post-dotfiles `setup` (idempotent git clones + commands) |
| `validate`  | —     | anyone | resolve + merge + validate the config, change nothing |
| `render`    | —     | anyone | resolve `--config` refs, merge their `imports:`, write the single flattened config to `-o`, change nothing — see [Remote & layered configuration](#remote--layered-configuration) |

### Flags

| Flag | Effect |
|------|--------|
| `--dry-run` | print every command instead of running it (records a full plan; runs nothing) |
| `--only <stage>` | run one stage by name or number (`--only 10`, `--only grub`) |
| `--config <ref>` | config reference (default `config.yaml`); **repeatable** — later refs override earlier (last wins). A ref is a local path, a `github.com/OWNER/REPO/path.yaml[@ref]` shorthand, or a raw URL (see [Remote & layered configuration](#remote--layered-configuration)) |
| `-o <file>` | (`render` only) where to write the flattened config |
| `--offline` | resolve remote refs from the local cache only (no network) |
| `--yes` | (`install` only) skip the destructive `ERASE` confirm + set a throwaway password — VMs only |

## Workflow

```sh
# Configure:
cp config.example.yaml config.yaml
$EDITOR config.yaml                    # set disks, hostname, user, package lists, themes

# Phase A — from the Arch live ISO (UEFI), online, as root:
./archwright validate                 # sanity-check config first
./archwright install --dry-run        # review the exact plan
./archwright install                  # type ERASE when prompted
reboot

# Phase B — after reboot, as your user (binary was staged in ~/):
./archwright bootstrap --dry-run
./archwright bootstrap
```

`config.yaml` is gitignored. **Double-check `disks:` — Phase A erases those devices.**
Always run `--dry-run` first: every destructive command is printed (and recorded as a
plan) without executing.

## Validation

Config rules are declared as `validate:` struct tags in
[`internal/config/config.go`](internal/config/config.go) (go-playground/validator) — the
struct *is* the schema. `validate` reports every problem at once with YAML-path messages:

```
$ archwright validate --config bad.yaml
disks.esp.device must start with "/dev/"
disks.lvm.filesystem must be one of: xfs ext4
disks.lvm.pvs must have at least 1 item(s)
```

## Remote & layered configuration

`--config` doesn't have to be a single local file. It can point at a config that lives in
a git repo or at a URL, and that config can pull in and **merge** other configs — so a
machine-specific config stays tiny and sits on top of a shared base.

```sh
archwright install --config github.com/AdamJHall/dotfiles/archwright.desktop.yaml@v1
```

### Reference forms

A `--config` value (and each `imports:` entry) is one of three forms, told apart by shape:

| Form | Example | Resolves to |
|------|---------|-------------|
| local path | `config.yaml`, `./desktop.yaml` | filesystem read (today's behaviour) |
| github shorthand | `github.com/OWNER/REPO/path/to.yaml[@ref]` | `raw.githubusercontent.com/OWNER/REPO/<ref-or-default>/path/to.yaml` |
| raw URL | `https://…/file.yaml` | HTTP GET |

### The `imports:` key

A config may carry a top-level `imports:` list naming other configs to merge in *underneath*
it:

```yaml
# archwright.desktop.yaml  (the entry point: desktop-specific)
imports:
  - archwright.base.yaml                                  # sibling, resolved next to THIS file
  - github.com/AdamJHall/dotfiles/archwright.kde.yaml@v1  # another file / repo, pinned
  - https://example.com/teams/shared.yaml                 # raw URL

system:
  hostname: desktop-box        # overrides whatever base set
packages:
  - steam                      # added on top of base's packages
```

A **bare relative path** inside `imports:` resolves against the **importing file's
location**, not your CWD — so a sibling in the same repo is just `archwright.base.yaml`, and
a github-rooted entry point makes its relative imports github-rooted too. `imports:` is
consumed by the resolver and stripped before validation; it is not a config field.

### Merge precedence

Layering is **base-first, importer-wins**, applied recursively:

1. An imported file is resolved and merged *before* the file that imports it.
2. Among multiple `imports:`, **later entries override earlier** ones.
3. The importing file's own top-level keys override everything it imported.
4. Imports are processed depth-first; an imported file may itself have `imports:`.

So for the example above, effective precedence (low → high) is:
`base.yaml` → `kde.yaml` → `shared.yaml` → `desktop.yaml`. Repeated `--config a --config b`
on the command line is the same merge applied to CLI layers — `b` wins over `a`.

### List fields

Maps merge recursively. Lists are merged per-field by what makes sense for that field:

| Field shape | Strategy | Examples |
|-------------|----------|----------|
| plain string lists | **union + dedup** | `packages`, `flatpaks`, `aur`, `system.locales`, `user.groups` |
| name-keyed structured lists | **merge by `name`** (a later layer overrides one entry) | `repos`, `hooks`, `flatpak_remotes` |
| identity/layout lists | **replace** | `disks.lvm.pvs`, `disks.btrfs.subvolumes` |

For the rare case where you want to drop everything inherited for one field, the `!replace`
tag is the escape hatch:

```yaml
packages: !replace [vim, git]   # ignore inherited packages, use exactly this
```

### `render` — resolve & merge, change nothing

```sh
archwright render --config github.com/AdamJHall/dotfiles/archwright.desktop.yaml@v1 \
  -o config.flat.yaml
```

`render` resolves every ref, expands `${VAR}` substitutions, merges all `imports:` and
repeated `--config` layers, and writes the single flattened config (no `imports:`) to `-o`.
It runs no stages and touches no disks — it's the way to preview exactly what a layered
config flattens to, and doubles as the debugging tool for the merge engine.

Phase A resolves and merges **once**, then stages the *flattened* config into the target for
Phase B. Phase B reads a plain local file — no network, no re-fetch — and is guaranteed to
see byte-identical config to Phase A.

### Trust, pinning & caching

Fetching config that drives **destructive disk operations and arbitrary hook commands** from
a URL is a real trust boundary — treat it like one:

- **Pin a ref.** Use `@<tag-or-sha>` on github shorthands; an unpinned `main` warns (and
  refuses under `--strict`). Phase A prints the resolved source list + merged result before
  the `ERASE` confirm, so you see exactly what you're about to run.
- **`--offline` uses the cache only.** Fetched files are cached under
  `$XDG_CACHE_HOME/archwright/` keyed by URL+ref — handy when re-running on a flaky live-ISO
  network.
- **Private repos.** Set `GITHUB_TOKEN` and it's sent as the `Authorization` header for
  github/raw fetches. Keep tokens in the environment, never in the config file (the `${VAR}`
  substitution already reads them).

## Architecture

```
main.go                       cobra CLI: install / bootstrap / validate
internal/config/              Config struct + tag-based Validate()
internal/archinstall/         render config.yaml -> archinstall config + creds JSON
internal/run/                 Runner: Cmd/Shell/Chroot/Root, dry-run, recorded .Plan
internal/ui/                  charmbracelet log + lipgloss styling + huh prompts
internal/stages/              one file per stage; self-registering ordered registry
```

Stages implement a small interface (`Order/Name/Phase/Run`) and register themselves in
`init()`. The runner records every command into `.Plan`, which is what the tests assert on.
Phase A is just two stages: `preflight` (UEFI + config + archinstall checks) and
`archinstall` (reflector → probe geometry → `internal/archinstall.Build` → write JSON → run
archinstall → **post-install in the target chroot**: custom repos + kernels → stage the
binary for Phase B). The `internal/archinstall` package is independently unit-tested: it
builds the disk/LVM JSON from a config + fake geometry and asserts the layout, the `obj_id`
wiring between PVs and the volume group, and size math — no disks required.

**Custom repos and kernels are Phase A, not Phase B.** They run in the post-archinstall
chroot so the very first boot already uses them (e.g. boots `linux-cachyos`, with stock
`linux` removed before it ever boots). The repo config is written into the target's
`pacman.conf` + keyring, so it persists and Phase B package installs resolve against it too.
archinstall must always pacstrap stock `linux` for a bootable baseline; `kernel.replace_stock`
removes it in the chroot before reboot.

## Relationship to dotfiles

This repo owns the **system**: disks, base OS, packages, boot splash, GRUB/KDE theming.
User-level dotfiles (zsh, terminal, etc.) stay in
[AdamJHall/dotfiles](https://github.com/AdamJHall/dotfiles) and are pulled in by the
`chezmoi` step.

Things the dotfiles *reference* but can't vendor — oh-my-zsh and its custom plugins, tmux's
TPM, theme repos — are listed under `setup.steps` in `config.yaml` and run by the final
`setup` stage (right after `chezmoi`, so the dotfiles' target dirs already exist).
`setup.steps` is an **ordered** list; each entry is either a `clone` or a `command`, and
they run top to bottom — so a clone that lands inside another clone's tree (e.g. the
oh-my-zsh custom plugins, which need `~/.oh-my-zsh` cloned first) is sequenced just by where
it appears. Each clone is idempotent: skipped if its `dest` already exists, or `git pull`ed
when `update: true`, so the stage is safe to re-run. A `command` is the escape hatch for the
occasional installer that isn't a git clone. See `config.example.yaml` for the full shape.

## Testing

```sh
go test ./...            # unit tests: validation table + per-stage command plans
go vet ./...
```

Tests run each stage in `--dry-run` and assert on the recorded command plan, so they
verify behavior **without touching disks**. What they cannot cover — real
partitioning/pacstrap/boot — is covered by the VM flow below.

### Testing in a VM (recommended before real hardware)

Phase A repartitions disks, so smoke-test the whole flow in QEMU with three virtual disks:

```sh
# Three disks: 100G (disk 1: ESP+swap+PV) + 2× 50G (whole-disk PVs)
qemu-img create -f qcow2 disk1.qcow2 100G
qemu-img create -f qcow2 disk2.qcow2 50G
qemu-img create -f qcow2 disk3.qcow2 50G

qemu-system-x86_64 \
  -enable-kvm -m 8G -smp 4 \
  -bios /usr/share/edk2/x64/OVMF.4m.fd \          # UEFI firmware (edk2-ovmf)
  -drive file=disk1.qcow2,if=virtio \
  -drive file=disk2.qcow2,if=virtio \
  -drive file=disk3.qcow2,if=virtio \
  -cdrom archlinux-x86_64.iso \
  -boot d
```

Inside the VM the disks appear as `/dev/vda`, `/dev/vdb`, `/dev/vdc` — set
`config.yaml` accordingly (`esp.device: /dev/vda`, PVs `/dev/vda3`, `/dev/vdb`, `/dev/vdc`).
Use `./archwright install --yes` to skip the interactive prompts during automated runs.

This VM run is also where you **validate the generated archinstall JSON** against the
version on the ISO. `install --dry-run` prints the rendered config without running anything;
a real `install` writes `/tmp/archinstall-config.json` + `/tmp/archinstall-creds.json` and
invokes `archinstall --silent`. If archinstall rejects the config after a version bump, diff
its schema and update `internal/archinstall` + the pinned `Version`.

## Releases

[goreleaser](https://goreleaser.com) builds cross-compiled static binaries:

```sh
goreleaser release --snapshot --clean        # local test, no publish
git tag v0.1.0 && git push origin v0.1.0
goreleaser release --clean                   # publish to GitHub
goreleaser check                             # validate .goreleaser.yaml
```

Config: [`.goreleaser.yaml`](.goreleaser.yaml) (linux amd64/arm64, version stamped from
the tag, `config.example.yaml` bundled in the archive).
```
