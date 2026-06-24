# Archwright

> ⚠️ **Heads up:** this is vibe coded and probably trash. It's opinionated, **not intended
> for production use** — only for personal use and messing around. There are almost certainly
> much better tools out there to handle this. Use at your own risk.

A declarative way to rebuild an Arch Linux machine from bare disks to a themed KDE desktop.
One config file (`config.yaml`) drives a single static Go binary (`archwright`) — no
`bash`/`yq`/`jq` runtime dependencies, just the binary.

It does **not** try to be NixOS — there's no purity, no rollback, no DSL. It's plain YAML
you can read top to bottom plus a binary that orchestrates
[archinstall](https://github.com/archlinux/archinstall) and the usual Arch tools (`yay`,
`flatpak`, `chezmoi`, …), designed for the "I had to reinstall again" workflow.

## The two-phase model

Everything happens in two phases, each a sequence of numbered, individually re-runnable,
dry-run-aware stages:

- **`install` (Phase A)** — on the live ISO, as **root**. Renders your `config.yaml` into an
  archinstall config and lets the official installer do partitioning, LVM/btrfs, pacstrap and
  the bootloader. Then, in the post-install chroot, it sets up custom repos and kernels (so the
  *first* boot already uses them) and stages the binary + flattened config for Phase B.
- **`bootstrap` (Phase B)** — on the booted system, as **your user**. Post-install
  customization: the AUR helper, packages, flatpaks, snapshots, boot splash, GRUB/KDE theming,
  dotfiles, and a final user-defined `setup` step.

> **⚠️ archinstall version coupling.** archinstall's JSON config is *not* a stable API; its
> schema changes between releases. Archwright renders against the version pinned in
> [`internal/archinstall`](internal/archinstall/archinstall.go) (`Version`), and `preflight`
> only *warns* if the live ISO ships a different one. **Validate the generated config against a
> real archinstall run in a VM** (see [Testing in a VM](#testing-in-a-vm)) before trusting it on
> hardware. After any archinstall version bump, diff the schema and update that package +
> `Version` together.

## Contents

- [Quick start](#quick-start)
- [Installing the binary](#installing-the-binary)
- [Commands & flags](#commands--flags)
- [Configuration reference](#configuration-reference)
- [Remote & layered configuration](#remote--layered-configuration)
- [Stages reference](#stages-reference)
- [Validation](#validation)
- [Testing in a VM](#testing-in-a-vm)
- [For contributors](#for-contributors)

## Quick start

```sh
# 1. Configure
cp config.example.yaml config.yaml
$EDITOR config.yaml                   # set disks, hostname, user, packages, themes
./archwright validate                 # sanity-check the config first

# 2. Phase A — from the Arch live ISO (UEFI), online, as root
./archwright install --dry-run        # review the exact plan, run nothing
./archwright install                  # type ERASE when prompted, then reboot

# 3. Phase B — after reboot, as your user (the binary was staged in ~/)
./archwright bootstrap --dry-run
./archwright bootstrap
```

`config.yaml` is **gitignored**, so your real machine config stays out of the repo.
**Double-check `disks:` — Phase A erases those devices.** Always run `--dry-run` first: every
destructive command is printed (and recorded into a plan) without executing.

## Installing the binary

```sh
go build -o archwright .          # local build
go install ./...                  # into $GOBIN

# stamped build (what releases do):
go build -ldflags "-s -w -X main.version=$(git describe --tags --always)" -o archwright .
```

Or grab a prebuilt binary from a [GitHub release](#releases).

### Quick install

A one-liner to grab a release — handy on the Arch live ISO:

```sh
curl -fsSL https://raw.githubusercontent.com/AdamJHall/archwright/main/get.sh | bash
```

[`get.sh`](get.sh) lists the last few releases, lets you pick one (Enter = latest), then
downloads, checksum-verifies and untars the binary into the current directory.

## Commands & flags

```
archwright install     [flags] [--yes]
archwright bootstrap   [flags]
archwright validate    [flags]
archwright render      [flags] [-o <out.yaml>]
archwright list-stages
archwright --version
```

| Command       | Phase | Run as | What it does |
|---------------|-------|--------|--------------|
| `install`     | A     | root, from the Arch live ISO | (optionally) pick mirrors with reflector → probe disk geometry → render an archinstall config + credentials → run `archinstall --silent` → then in the target chroot configure custom repos + kernels and stage the binary + **flattened config** for Phase B |
| `bootstrap`   | B     | your user, after reboot | AUR helper, packages, flatpaks, snapper, plymouth, GRUB theme, KDE, dotfiles, then the user-defined `setup` steps |
| `validate`    | —     | anyone | resolve + merge + validate the config; change nothing |
| `render`      | —     | anyone | resolve `--config` refs, merge their `imports:`, write the single flattened config to `-o`; change nothing — see [Remote & layered configuration](#remote--layered-configuration) |
| `list-stages` | —     | anyone | print every registered stage with its order, name and phase (the source of truth for `--only`/`--skip`/`stages.disable`) |

### Global flags

These apply to `install`, `bootstrap`, `validate` and `render`:

| Flag | Effect |
|------|--------|
| `--dry-run` | print every command instead of running it (records a full plan; runs nothing) |
| `--config <ref>` | config reference (default `config.yaml`); **repeatable** — later refs override earlier (last wins). A local path, a `github.com/OWNER/REPO/path.yaml[@ref]` shorthand, or a raw URL — see [Remote & layered configuration](#remote--layered-configuration) |
| `--offline` | resolve remote refs from the local cache only (no network) |
| `--strict` | refuse unpinned github refs (require `@ref`) |
| `--no-color` | disable coloured output (`NO_COLOR` is also honoured) |

### Selecting stages

Four flags slice the stage list (all match a stage **by name or number** — see `list-stages`):

| Flag | Effect |
|------|--------|
| `--only <stage>` | run a single stage (`--only 20`, `--only packages`) |
| `--skip <stage>` | skip a stage; **repeatable** (`--skip plymouth --skip grub-theme`) |
| `--from <stage>` | resume from a stage onwards (inclusive) |
| `--to <stage>` | stop after a stage (inclusive) |

To skip stages *persistently* (without passing flags every run), list them under
[`stages.disable`](#stage-selection) in the config. A `--only` on the command line overrides
both `--skip` and `stages.disable`.

### `install`-only flag

| Flag | Effect |
|------|--------|
| `--yes` | skip the destructive `ERASE` confirmation and set a throwaway password — **VMs / automation only** |

### `render`-only flag

| Flag | Effect |
|------|--------|
| `-o`, `--output <file>` | where to write the flattened config (default stdout; `-` is also stdout) |

## Configuration reference

`config.yaml` is the whole interface. Every value may reference an environment variable with
`${VAR}` syntax (see [Environment variables](#environment-variables)). The struct in
[`internal/config/config.go`](internal/config/config.go) *is* the schema — the
[`config.example.yaml`](config.example.yaml) is an annotated, copy-ready instance of everything
below.

### `system`

Base OS identity, generated in Phase A's chroot.

```yaml
system:
  hostname: arch-box
  timezone: Australia/Adelaide   # timedatectl list-timezones
  locale: en_AU.UTF-8            # default LANG; enabled by the installer
  locales:                       # additional locales to also enable in /etc/locale.gen
    - en_US.UTF-8
  keymap: us                     # console keymap; localectl list-keymaps
  ntp: true                      # NTP time sync (defaults to true when omitted)
```

`hostname`, `timezone`, `locale` and `keymap` are required. `locales` is additive (the default
`locale` is always enabled). `ntp` is a tri-state: omit it for the default (on).

### `user`

```yaml
user:
  name: adam
  shell: /usr/bin/zsh            # must start with /
  groups: [wheel]                # supplementary groups; `wheel` grants sudo
```

`wheel` is what gives the user `sudo` (configured during install) — needed because Phase B runs
as the user and escalates with `sudo`.

### Stage selection

Skip stages without emptying their config blocks — the persisted equivalent of `--skip`. Match
by stage **name or number** (see `archwright list-stages`).

```yaml
stages:
  disable: [plymouth, grub-theme]
```

### `disks`

The destructive part, and the most configurable. `layout` is the discriminator; exactly the
matching sub-block must be present. **`layout` defaults to `lvm`** when omitted, so older configs
keep working.

| `layout` | Shape |
|----------|-------|
| `lvm` (default) | ESP + one LVM volume group spanning the listed PVs → root mounted at `/` |
| `btrfs` | ESP + a single btrfs root carrying subvolumes (snapshot-friendly) |
| `plain` | ESP + a single ext4/xfs root partition, no LVM |

The ESP is always created on disk 1 (`esp.device`); on the `lvm` layout that disk is partitioned
ESP + remainder-as-PV, and any other PVs listed are consumed as a single full-disk partition.

```yaml
disks:
  layout: lvm
  esp:
    device: /dev/sda
    size: 4GiB
  swap:
    type: swapfile               # swapfile (default) | zram | partition | none
    size: 4GiB                   # match RAM for hibernation
  lvm:
    vg: vg0
    lv: root
    filesystem: xfs              # xfs | ext4
    pvs:
      - /dev/sda2                # remainder partition of the ESP device
      - /dev/sdb                 # whole second disk
      - /dev/sdc                 # whole third disk
```

#### Swap × layout compatibility

`swap.type` defaults to `swapfile`. Not every type is valid for every layout:

| `swap.type` | What it is | Uses `size`? | Valid layouts |
|-------------|------------|--------------|---------------|
| `swapfile` (default) | post-install `/swapfile` | yes (required) | any — **the only LVM-compatible on-disk option** |
| `zram` | compressed RAM swap (archinstall's own) | no | any |
| `partition` | a real linux-swap partition | yes (required) | `plain`, `btrfs` only (**not** `lvm`) |
| `none` | no swap | no | any |

On btrfs prefer `zram` or `partition`: a swapfile needs a dedicated nocow/no-compress subvolume,
so it isn't emitted for that layout.

#### Multiple LVM volumes

Instead of a single root LV (`lv` + `filesystem`), carve several volumes in the VG — set
`volumes` **instead of** `lv`/`filesystem`, not both. Exactly one volume omits `size` (it takes
the rest of the VG) and exactly one is mounted at `/`.

```yaml
  lvm:
    vg: vg0
    pvs: [/dev/sda2, /dev/sdb]
    volumes:
      - { name: root, mountpoint: /,     filesystem: xfs,  size: 64GiB }
      - { name: home, mountpoint: /home, filesystem: ext4 }   # rest of the VG
```

#### btrfs layout

```yaml
disks:
  layout: btrfs
  esp: { device: /dev/nvme0n1, size: 4GiB }
  swap: { type: zram }
  btrfs:
    device: /dev/nvme0n1         # the single disk holding the btrfs root
    compress: zstd               # -> compress=zstd mount option (e.g. zstd or zstd:3)
    snapshots: snapper           # snapper | none (provisions the `snapper` stage in Phase B)
    subvolumes:
      - { name: "@",     mountpoint: / }
      - { name: "@home", mountpoint: /home }
      - { name: "@log",  mountpoint: /var/log }
```

`snapshots: snapper` is what activates the [`snapper` stage](#stages-reference) in Phase B (it is
a no-op for any other layout/setting).

#### plain layout

```yaml
disks:
  layout: plain
  esp: { device: /dev/nvme0n1, size: 4GiB }
  swap: { type: partition, size: 8GiB }
  plain:
    device: /dev/nvme0n1         # single disk: ESP + one root partition, no LVM
    filesystem: ext4             # ext4 | xfs
```

#### Disk encryption (LUKS)

Omit for no encryption (the default). The LUKS passphrase is the same install password
archwright already collects — it is **not** stored in the config.

```yaml
disks:
  encryption:
    type: lvm_on_luks            # encrypt the PV partitions, LVM on top
```

| `encryption.type` | Effect | Requires |
|-------------------|--------|----------|
| `lvm_on_luks` | encrypt the PV partitions, LVM on top | `lvm` layout, **≤ 2 PVs** (archinstall limit) |
| `luks` | encrypt the single root partition | `plain` or `btrfs` layout |

> The `lvm_on_luks` 2-PV limit and exact archinstall behaviour are reverse-engineered and
> **VM-validation-pending** — confirm in a VM before relying on it.

### `mirrors`

Optional. Runs `reflector` in the live ISO before archinstall so pacstrap (and the installed
system, which inherits the mirrorlist) use fast, recent mirrors. Omit the section, or set
`reflector: false`, to skip it.

```yaml
mirrors:
  reflector: true
  countries: [AU]                # --country; omit for worldwide
  latest: 20                     # --latest N most-recently-synced
  fastest: 10                    # --fastest N by measured download rate
  sort: rate                     # rate | age | score | delay | country
  protocols: [https]             # --protocol (https | http | rsync | ftp)
```

### Software: what goes where

This is the most error-prone decision, so it's worth stating plainly. Four lists install
packages at different points:

| Field | When / how | Use for |
|-------|------------|---------|
| `pacstrap` | **Phase A**, by archinstall, verbatim | the minimum the system needs to *boot and run Phase B* — base-devel/git (to build the AUR helper), the login shell, `sudo`, `networkmanager`, `efibootmgr`, CPU microcode |
| `kernel.base` | **Phase A** pacstrap | the bootable baseline kernel(s) — **official-repo only** (custom repos aren't set up yet) |
| `kernel.packages` | **Phase A** chroot, after repo setup | extra/custom kernels (e.g. `linux-cachyos`) so the first boot can run them |
| `packages` | **Phase B**, `pacman -S --needed` | everything else from the official (and custom) repos — the desktop, tools, etc. |
| `aur` | **Phase B**, via the AUR helper | AUR packages (e.g. `1password`) |
| `flatpaks` | **Phase B** | Flatpak apps |

`pacstrap` is the **complete** Phase-A set, rendered verbatim — nothing is added in code.
`preflight` only *warns* about recommended-but-absent entries; it never re-adds them.

```yaml
repos:                           # custom pacman repos (configured in Phase A's chroot)
  - name: cachyos
    setup: |                     # a root shell snippet for repos with a maintained installer
      curl -fsSL https://mirror.cachyos.org/cachyos-repo.tar.xz | tar -xJ -C /tmp
      cd /tmp/cachyos-repo && ./cachyos-repo.sh --install
  # Purely declarative repo (no script):
  # - name: chaotic-aur
  #   key: 3056513887B78AEB      # imported + locally signed via pacman-key
  #   keyserver: keyserver.ubuntu.com
  #   include: /etc/pacman.d/chaotic-mirrorlist   # written into the pacman.conf section

pacstrap:
  - base-devel                   # build the AUR helper in Phase B
  - git
  - zsh                          # the user's login shell (user.shell)
  - sudo                         # Phase B escalates via sudo
  - networkmanager               # network at first boot
  - efibootmgr
  - intel-ucode                  # CPU microcode (or amd-ucode)

kernel:
  base: [linux]                  # pacstrapped baseline; OFFICIAL-repo kernels only
  packages: [linux-cachyos, linux-cachyos-headers]   # custom kernels (installed in the chroot)
  default: linux-cachyos         # GRUB default entry; must be in base ∪ packages
  replace_stock: true            # remove stock `linux` after install (needs ≥1 packages entry)

packages: [vim, alacritty, dolphin, plasma-meta, starship, fzf]

flatpak_remotes:                 # the COMPLETE set — nothing (not even flathub) is implicit
  - { name: flathub, url: https://flathub.org/repo/flathub.flatpakrepo }
flatpaks:                        # each app is "remote:appid"; remote must be declared above
  - flathub:com.spotify.Client
  - flathub:org.mozilla.firefox

aur: [1password, 1password-cli]
aur_helper: yay                  # yay (default) | paru
```

> **Why repos and custom kernels are Phase A:** they run in the post-archinstall chroot, written
> into the target's `pacman.conf` + keyring, so the *very first* boot already uses them (boots
> `linux-cachyos`, with stock `linux` removed before it ever boots) and Phase B installs resolve
> against the custom repos too. archinstall must always pacstrap a stock `linux` for a bootable
> baseline; `kernel.replace_stock` removes it in the chroot before reboot.

### `bootloader`

```yaml
bootloader:
  kind: grub                     # grub (default) | systemd-boot
```

`grub` is the default. `systemd-boot` is reverse-engineered and **VM-validation-pending** (no
`grub.cfg`; cmdline edits go to `/etc/kernel/cmdline`, and `bootctl update` replaces
`grub-mkconfig`).

### Boot splash & theming

```yaml
plymouth:
  theme: spinner                 # passed to plymouth-set-default-theme

grub:
  cmdline_extra: "quiet splash"  # appended to GRUB_CMDLINE_LINUX_DEFAULT
  theme:
    source: vinceliuice          # vinceliuice | url | none
    name: tela                   # vinceliuice theme: tela|stylish|vimix|whitesur|slaze
    # url: https://example.com/theme.tar.gz   # used when source: url
```

### `desktop` & `kde`

`desktop.environment` selects which DE stage runs in Phase B. **Only KDE has a built-in stage**;
any other value makes the KDE stage a clean no-op — route that DE's setup through
[`hooks`](#hooks) and your dotfiles instead.

```yaml
desktop:
  environment: kde               # kde (default) | gnome | hyprland | sway | none

kde:
  look_and_feel: org.kde.breezedark.desktop
  color_scheme: BreezeDark
  cursor_theme: breeze_cursors
  # wallpaper: /usr/share/wallpapers/Next/contents/images/1920x1080.png
```

### Dotfiles

The dotfiles stage applies your dotfiles via a selectable manager. The `manager` defaults to
`chezmoi`, so it's optional; `repo` is required for any manager other than `none`.

```yaml
dotfiles:
  repo: https://github.com/AdamJHall/dotfiles
  # manager: chezmoi             # chezmoi (default) | yadm | bare-git | none
```

| `manager` | What it runs |
|-----------|--------------|
| `chezmoi` (default) | `chezmoi init --apply <repo>`, or `chezmoi apply` when already initialized |
| `yadm` | `yadm clone <repo>`, or `yadm pull` when already cloned |
| `bare-git` | classic bare repo at `~/.dotfiles` with `--work-tree=$HOME` |
| `none` | skip dotfiles entirely (clean no-op) |

### `setup` steps

Runs **after** the dotfiles stage. For the things a dotfiles repo references but can't vendor —
oh-my-zsh and its custom plugins, tmux's TPM, theme repos. `steps` is an **ordered** list (top to
bottom); each entry is either a `clone` or a `command`. Order matters: a clone that lands inside
another clone's tree (oh-my-zsh custom plugins) just has to come after it.

Each `clone` is idempotent — skipped if `dest` already exists (or `git pull`ed when
`update: true`), so the stage is safe to re-run. `~` expands to the user's home. A `command` is
the escape hatch for installers that aren't a git clone.

```yaml
setup:
  steps:
    - clone: { url: https://github.com/ohmyzsh/ohmyzsh, dest: ~/.oh-my-zsh }   # FIRST
    - clone: { url: https://github.com/zsh-users/zsh-autosuggestions, dest: ~/.oh-my-zsh/custom/plugins/zsh-autosuggestions }
    - clone: { url: https://github.com/tmux-plugins/tpm, dest: ~/.config/tmux/plugins/tpm }
    # - command: curl -sS https://starship.rs/install.sh | sh -s -- -y
```

### `services`

Runs **last** in Phase B (after `dotfiles` and `setup`). `systemctl enable`s the listed units so
they start on the **next boot** — the typical case is a login/display-manager unit that should
take over after reboot rather than be started underneath the current session, so units are
enabled, not `--now`-started. Enabling is idempotent, so the stage is safe to re-run.

`enable` is system units (enabled as root); `user` is per-user units (enabled with
`systemctl --user`). The `.service` suffix is optional. For one-off needs (or to enable *and*
start a unit now), a `hook` running `systemctl enable --now <unit>` still works.

```yaml
services:
  enable:
    - plasmalogin.service   # SDDM/Plasma login on next boot
    - bluetooth.service
  user:
    - syncthing.service
```

### `hooks`

The general escape hatch: run your own commands at lifecycle points instead of writing a Go
stage (snap/cargo/gsettings/`systemctl enable`/etc.). Each hook sets exactly one of `run` (an
inline snippet) or `script` (a path to a script file). Hooks are dry-run-aware like everything
else.

`at` is one of the four global points — `pre-install`, `post-install`, `pre-bootstrap`,
`post-bootstrap` — or a per-stage `before:<stage>` / `after:<stage>` (stage by name; see
`list-stages`).

```yaml
hooks:
  - name: rust toolchain
    at: after:packages
    run: curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
  - name: enable bluetooth
    at: post-bootstrap
    root: true                   # run privileged
    run: systemctl enable --now bluetooth
  - name: provision
    at: post-bootstrap
    script: ~/bin/provision.sh   # `~` -> $HOME; existence NOT checked at validate time
    dir: ~/work                  # working directory
    env: { PROFILE: desktop }    # extra environment variables
```

### Environment variables

Any config **value** may reference an environment variable with `${VAR}` syntax — it is
substituted from the process environment when the config loads. This keeps secrets and
per-machine values out of the (gitignored) file. Rules:

- Only **values** are expanded — keys and comments are left untouched, so a literal `$` in a
  comment is fine.
- An **unset** variable is an error (it names every missing one), not a silent blank.
- Write `$$` for a literal `$` (e.g. inside a shell snippet meant to expand at runtime).

## Remote & layered configuration

`--config` doesn't have to be a single local file. It can point at a config that lives in a git
repo or at a URL, and that config can pull in and **merge** other configs — so a machine-specific
config stays tiny and sits on top of a shared base.

```sh
archwright install --config github.com/AdamJHall/dotfiles/archwright.desktop.yaml@v1
```

### Reference forms

A `--config` value (and each `imports:` entry) is one of three forms, told apart by shape:

| Form | Example | Resolves to |
|------|---------|-------------|
| local path | `config.yaml`, `./desktop.yaml` | filesystem read |
| github shorthand | `github.com/OWNER/REPO/path/to.yaml[@ref]` | `raw.githubusercontent.com/OWNER/REPO/<ref-or-default>/path/to.yaml` |
| raw URL | `https://…/file.yaml` | HTTP GET |

### The `imports:` key

A config may carry a top-level `imports:` list naming other configs to merge in *underneath* it:

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

A **bare relative path** inside `imports:` resolves against the **importing file's location**,
not your CWD — so a sibling in the same repo is just `archwright.base.yaml`, and a github-rooted
entry point makes its relative imports github-rooted too. `imports:` is consumed by the resolver
and stripped before validation; it is not a config field.

### Merge precedence

Layering is **base-first, importer-wins**, applied recursively:

1. An imported file is resolved and merged *before* the file that imports it.
2. Among multiple `imports:`, **later entries override earlier** ones.
3. The importing file's own top-level keys override everything it imported.
4. Imports are processed depth-first; an imported file may itself have `imports:`.

So for the example above, effective precedence (low → high) is:
`base.yaml` → `kde.yaml` → `shared.yaml` → `desktop.yaml`. Repeated `--config a --config b` on
the command line is the same merge applied to CLI layers — `b` wins over `a`.

### List fields

Maps merge recursively. Lists are merged per-field by what makes sense for that field:

| Field shape | Strategy | Examples |
|-------------|----------|----------|
| plain string lists | **union + dedup** | `packages`, `flatpaks`, `aur`, `system.locales`, `user.groups` |
| name-keyed structured lists | **merge by `name`** (a later layer overrides one entry) | `repos`, `hooks`, `flatpak_remotes` |
| identity/layout lists | **replace** | `disks.lvm.pvs`, `disks.btrfs.subvolumes` |

For the rare case where you want to drop everything inherited for one field, the `!replace` tag
is the escape hatch:

```yaml
packages: !replace [vim, git]   # ignore inherited packages, use exactly this
```

### `render` — resolve & merge, change nothing

```sh
archwright render --config github.com/AdamJHall/dotfiles/archwright.desktop.yaml@v1 \
  -o config.flat.yaml
```

`render` resolves every ref, expands `${VAR}` substitutions, merges all `imports:` and repeated
`--config` layers, and writes the single flattened config (no `imports:`) to `-o`. It runs no
stages and touches no disks — the way to preview exactly what a layered config flattens to, and
the debugging tool for the merge engine.

Phase A resolves and merges **once**, then stages the *flattened* config into the target for
Phase B. Phase B reads a plain local file — no network, no re-fetch — guaranteed byte-identical
to what Phase A saw.

### Trust, pinning & caching

Fetching config that drives **destructive disk operations and arbitrary hook commands** from a
URL is a real trust boundary — treat it like one:

- **Pin a ref.** Use `@<tag-or-sha>` on github shorthands; an unpinned `main` warns (and refuses
  under `--strict`). When any remote source is in play, Phase A prints the resolved source list
  before the `ERASE` confirm; `render` prepends the same list as a
  `# Flattened by archwright from: …` provenance comment.
- **`--offline` uses the cache only.** Fetched files are cached under
  `$XDG_CACHE_HOME/archwright/` keyed by URL+ref — handy when re-running on a flaky live-ISO
  network.
- **Private repos.** Set `GITHUB_TOKEN` and it's sent as the `Authorization` header for
  github/raw fetches. Keep tokens in the environment, never in the config file (the `${VAR}`
  substitution already reads them).

## Stages reference

Every stage is numbered, individually re-runnable, and dry-run-aware. `archwright list-stages`
prints this live; the `--only`/`--skip`/`--from`/`--to` flags and `stages.disable` all match by
name **or** number.

| # | Stage | Phase | What it does |
|---|-------|-------|--------------|
| 0  | `preflight`   | A | UEFI + config + archinstall version checks (warns, doesn't block) |
| 10 | `archinstall` | A | reflector → probe geometry → render JSON → `archinstall --silent` → chroot: repos + kernels → stage the binary for Phase B |
| 10 | `yay`         | B | install the AUR helper (`aur_helper`) |
| 20 | `packages`    | B | `pacman -S --needed` the official/custom-repo packages |
| 25 | `snapper`     | B | provision Snapper (only when btrfs + `snapshots: snapper`) |
| 30 | `flatpak`     | B | register `flatpak_remotes`, install `flatpaks` |
| 40 | `aur`         | B | build/install the `aur` list via the helper |
| 50 | `plymouth`    | B | set the boot splash theme |
| 60 | `grub-theme`  | B | apply the GRUB theme + cmdline extras |
| 70 | `kde`         | B | KDE look-and-feel / colours / cursor / wallpaper (no-op for other DEs) |
| 80 | `dotfiles`    | B | apply dotfiles via the configured manager |
| 85 | `setup`       | B | run the ordered `setup.steps` (clones/commands) |
| 90 | `services`    | B | `systemctl enable` the `services` units so they start on the next boot |

(Phase A and Phase B each have their own order numbering — that's why both have a `10`.)

## Validation

Config rules are declared as `validate:` struct tags in
[`internal/config/config.go`](internal/config/config.go) (go-playground/validator), plus
cross-field "semantic" checks for things tags can't express (the right disk sub-block for the
layout, swap/layout compatibility, the `kernel.default ∈ base ∪ packages` rule, `flatpaks`
remotes, …). `validate` reports **every** problem at once with YAML-path messages:

```
$ archwright validate --config bad.yaml
disks.esp.device must start with "/dev/"
disks.lvm.filesystem must be one of: xfs ext4
disks.lvm.pvs must have at least 1 item(s)
```

## Testing in a VM

**Recommended before real hardware.** Phase A repartitions disks, so smoke-test the whole flow
in QEMU before trusting it on hardware — this is also where you **validate the generated
archinstall JSON** against the archinstall version on the ISO.

The harnesses and the full test pyramid (fast checks → loopback integration → interactive VM →
automated VM e2e) live in [CONTRIBUTING.md](CONTRIBUTING.md#testing). In short, `task vm-fresh`
boots the live ISO with clean disks and shares the repo over 9p, and `task vm-e2e` runs the
headless install → bootstrap → validate flow unattended.

## For contributors

### Architecture

```
main.go                 cobra CLI: install / bootstrap / validate / render / list-stages + flags
internal/config/        Config struct; Validate() via go-playground/validator struct tags
internal/configsrc/     resolve remote/layered config: --config refs, imports: recursion,
                        ${VAR} expand, deep-merge -> flattened config
internal/archinstall/   render config.yaml -> archinstall config + creds JSON (Phase A core)
internal/run/           Runner: Cmd/Shell/Chroot/Root/Try, dry-run, recorded .Plan
internal/ui/            stderr-bound lipgloss renderer + log + huh prompts
internal/stages/        one file per stage; self-registering ordered registry
```

Stages implement a small interface (`Order`/`Name`/`Phase`/`Run`) and register themselves in
`init()`. `Run` does all side effects through `ctx.R` (the `Runner`), never `os/exec` directly —
that's what makes a stage testable and dry-run-safe. The runner records every command into
`.Plan`, which is what the tests assert on. `internal/archinstall` is independently unit-tested:
it builds the disk/LVM JSON from a config + fake geometry and asserts the layout, the `obj_id`
wiring between PVs and the volume group, and the size math — no disks required.

### Running the tests

```sh
go build -o archwright .   # build
go test ./...              # unit tests: validation table + per-stage command plans
go vet ./...
```

Tests run each stage in `--dry-run` and assert on the recorded command plan, so they verify
behavior **without touching disks**. What they cannot cover — real partitioning/pacstrap/boot —
is covered by the [VM flow](#testing-in-a-vm).

### Relationship to dotfiles

This repo owns the **system**: disks, base OS, packages, boot splash, GRUB/KDE theming.
User-level dotfiles (zsh, terminal, etc.) stay in
[AdamJHall/dotfiles](https://github.com/AdamJHall/dotfiles) and are pulled in by the dotfiles
stage. Things the dotfiles *reference* but can't vendor (oh-my-zsh + plugins, tmux's TPM, theme
repos) are listed under [`setup.steps`](#setup-steps) and run by the final `setup` stage, right
after dotfiles so their target dirs already exist.

### Releases

[goreleaser](https://goreleaser.com) builds cross-compiled static binaries:

```sh
goreleaser release --snapshot --clean        # local test, no publish
git tag v0.1.0 && git push origin v0.1.0
goreleaser release --clean                   # publish to GitHub
goreleaser check                             # validate .goreleaser.yaml
```

Config: [`.goreleaser.yaml`](.goreleaser.yaml) (linux amd64/arm64, version stamped from the tag,
`config.example.yaml` bundled in the archive).
</content>
</invoke>
