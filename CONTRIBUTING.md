# Contributing to archwright

archwright is a single static Go binary that rebuilds an Arch box from bare disks to a themed
KDE desktop from one declarative `config.yaml` (see the [README](README.md) for the user
workflow and [CLAUDE.md](CLAUDE.md) for the deep architecture + the parallel-development "wave"
playbook). This guide is the practical how-to: **add → test → commit → PR.**

## Setup

```sh
mise install                 # provisions the pinned Go + Task (see mise.toml)
go build -o archwright .     # or: task build
```

For the VM/loopback test harnesses you also need QEMU, OVMF, etc. — see the dependency table in
[`test/e2e/vm/README.md`](test/e2e/vm/README.md#requirements). In short, on Arch:

```sh
sudo pacman -S --needed qemu-base edk2-ovmf libarchive git curl
sudo usermod -aG kvm "$USER"   # re-login afterwards
```

## Project layout (where things live)

```
main.go                 cobra CLI: install / bootstrap / validate / render / list-stages
internal/config/        Config struct + Validate() (go-playground/validator struct tags)
internal/configsrc/     resolve + deep-merge remote/layered --config refs and imports:
internal/archinstall/   render config.yaml -> archinstall JSON + creds (Phase A core)
internal/run/           Runner: Cmd/Shell/Chroot/Root/Try, dry-run, recorded .Plan
internal/stages/        one file per stage; self-registering ordered registry
test/e2e/disks.sh       loopback (losetup) integration harness — Phase A render vs real archinstall
test/vm.sh              interactive QEMU (boot ISO / installed disk by hand)
test/e2e/vm/            automated, headless QEMU e2e harness (install -> bootstrap -> validate)
docs/bugs/              open findings from the e2e harness, ready to pick up
```

## Adding things

### A Phase B stage (the common case)

A stage is a tiny struct in its own file under `internal/stages/` implementing
`Order() int`, `Name() string`, `Phase() Phase`, `Run(ctx *Context) error`, and registering
itself in `init()` via `register(...)`. Copy `internal/stages/packages.go` for the minimal
pattern. Rules:

- `Order` is the numeric prefix (10, 20, …); keep existing ones stable so `--only <number>`
  and `--from/--to` keep working. `task build && ./archwright list-stages` shows the order.
- **All** side effects go through `ctx.R` (the `Runner`), never `os/exec` directly — that's
  what makes the stage dry-run-safe and testable. Use `Root` for privileged, `Cmd` for
  unprivileged, `Shell` only when you need pipes/redirects, `Try` for best-effort, `Chroot`
  for Phase A arch-chroot work.
- **Degrade to a no-op when unconfigured**, so existing configs/goldens are unaffected.
- Add a `*_test.go` that runs the stage in dry-run and asserts on the recorded `.Plan`
  (self-contained config snippet — don't edit shared fixtures).
- ⚠️ Don't let a stage block on an interactive prompt during `bootstrap` (pass `-y` /
  `--noninteractive` to anything that might ask) — see
  [`docs/bugs/flatpak-system-remote-add-polkit-hang.md`](docs/bugs/flatpak-system-remote-add-polkit-hang.md).

### A config option / schema field

The `Config` struct in `internal/config/config.go` **is** the schema. Add the field with its
`yaml:` + `validate:` tags; put cross-field rules in `semanticErrors()` (not struct tags);
add a table case in `config_test.go`. If the option changes Phase A output, also update
`internal/archinstall/` and add a golden case (see below).

### An automated e2e descriptor

To grow VM coverage, add a `matrix/<family>.py` + `configs/<name>.yaml` under `test/e2e/vm/`
— **new files only**, following the descriptor contract in
[`test/e2e/vm/README.md`](test/e2e/vm/README.md). Keep configs trimmed/cheap.

## Testing

Work up the pyramid — fast checks first, VMs last.

### 1. Fast checks (always, no disks)

```sh
go build -o archwright .   # task build
go test ./...              # task test  — validation table + per-stage dry-run command plans
go vet ./...               # task vet
gofmt -l .                 # must print nothing
```

These run every stage in `--dry-run` and assert on the recorded command plan, so they verify
behavior without touching disks. `internal/archinstall` is unit-tested against fake geometry
(layout, PV↔VG `obj_id` wiring, size math).

**Schema-shape changes** (anything that alters the rendered archinstall JSON) use **two
commits**: first the behavior-preserving refactor (goldens unchanged), then the shape change
that regenerates them:

```sh
go test ./internal/archinstall/ -run TestRenderGolden -update   # regenerate goldens
```
Keep the diff reviewable, and remember **archinstall's JSON is not a stable API** — after an
archinstall version bump, diff its schema and update `internal/archinstall` + the pinned
`Version` together, then re-validate in a VM (see CLAUDE.md "Key gotchas").

### 2. Loopback integration (root, no boot)

Proves the rendered archinstall JSON is accepted by a *real* archinstall against `losetup`
loop devices — the cheapest way to catch schema drift:

```sh
task e2e-disks-light                       # archinstall --dry-run validation (fast, no network)
task e2e-disks-light LAYOUT=single-disk-lvm FS=ext4
task e2e-disks-full                        # real partition/format/pacstrap, then assert layout
```

### 3. Manual interactive VM (`test/vm.sh`)

Use this to **poke by hand** — try a brand-new layout before codifying it, watch a desktop
actually render, or explore a confusing failure with a live shell. It boots a *graphical* QEMU
and shares the repo in over 9p, so your freshly built binary shows up in the VM.

```sh
cp config.example.yaml config.yaml    # gitignored; set devices to /dev/vda, /dev/vdb, /dev/vdc
task build                            # rebuild on the host; the 9p share picks it up
task vm-fresh                         # boot the live ISO with clean disks
# inside the VM (repo auto-mounts at /mnt/host):
#   cp /mnt/host/archwright /root/ && cp /mnt/host/config.yaml /root/
#   /root/archwright install --dry-run     # inspect the rendered archinstall JSON
#   /root/archwright install --yes         # DESTRUCTIVE: wipes vda/vdb/vdc, installs
task vm-disk                          # reboot into the installed system to poke around
```

`task vm` is the same without wiping disks; disk sizes are env-overridable
(`DISK1=40G … task vm`). This is interactive only — for unattended pass/fail use the harness
below.

### 4. Automated VM e2e (`task vm-e2e`)

The full last-mile proof: boots the ISO headless, runs `install --yes`, reboots, runs
`bootstrap`, and asserts the installed system — no human interaction. This is what you run to
confirm a change works end-to-end across layouts/features.

```sh
task vm-e2e -- lvm-multi          # one descriptor
task vm-e2e -- features-min       # the cheap stage-coverage bundle
task vm-e2e -- --jobs 5           # the whole matrix, 5 VMs at once
task vm-e2e-list                  # list descriptors
```

See [`test/e2e/vm/README.md`](test/e2e/vm/README.md) for how it works, the descriptor
contract, and per-run logs (`.e2e/runs/<name>/serial.log`). Known open findings it has
surfaced live in [`docs/bugs/`](docs/bugs/) — good first contributions.

### What to run for a given change

- Stage / config logic → **1** (and a VM run of an affected descriptor if behavior is
  disk/boot-visible).
- Anything touching `internal/archinstall` / Phase A layout → **1 + 2**, then **4** for the
  affected layout (CLAUDE.md's archinstall-drift rule: validate against a real run).
- New `disks.*` layout, swap, bootloader, encryption → **4** with a matching descriptor.

## Committing

- **Branch off `main`** — never commit straight to `main`.
- **[Conventional Commits](https://www.conventionalcommits.org/):** `feat:`, `fix(scope):`,
  `refactor:`, `chore:`, `docs:`, `test:` (match the existing `git log` style, e.g.
  `feat: add services stage to enable systemd units in Phase B`).
- Keep commits focused; use the two-commit split for schema-shape changes (above).
- Before pushing: `go build ./... && go vet ./... && go test ./... && gofmt -l .` (clean).
- `config.yaml` and `.vm/` / `.e2e/` / `.iso/` are gitignored — never commit them. Stage
  explicit paths; don't `git add -A`.

## Opening a PR

```sh
git switch -c feat/my-change            # or fix/…, docs/…
# … commits …
git push -u origin feat/my-change
gh pr create --base main --fill         # then edit title/body
```

In the PR description: what changed and why, which test tiers you ran (paste the `task vm-e2e`
PASS line for any layout you exercised), and call out any archinstall schema change (with the
regenerated goldens in their own commit). CI runs the Go suite and the loopback
`e2e-disks-light` check on every PR; the VM harness is run locally (it needs `/dev/kvm`).
