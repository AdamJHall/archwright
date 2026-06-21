# Archwright — extensibility & TUI review

A review of the whole codebase with concrete recommendations for making it more
**generic, extensible, and configurable**, plus a design for converting the output
from a streaming CLI into a scrollable **TUI** (charmbracelet viewport).

The codebase is clean and well-tested, but hardcoded to one opinionated path:
UEFI + GRUB, LVM + XFS root, post-install swapfile, KDE Plasma, `yay`, `chezmoi`.
The rigidity is concentrated in a few load-bearing spots, and most of it loosens with
the same move: **a discriminator field + small strategy dispatch + degrade-to-no-op**,
rather than removing anything.

The review was done by three focused passes: **(A)** disk/archinstall rendering,
**(B)** the config/stage/runner framework, **(C)** the Phase B customization stages.

---

## The three cross-cutting wins (do these first)

Each one unlocks or cheapens everything else.

### 1. Shared stage helpers — HIGH value, LOW cost

Three patterns repeat across the Phase B stages and are the root of most duplication:

- **ensure-tool-then-use** (`LookPath` → `pacman -S` fallback):
  `flatpak.go:26`, `plymouth.go:32`, `chezmoi.go:28`, `yay.go:20`.
- **idempotent grep-guarded `sed /etc/default/grub`**:
  `plymouth.go:39-54`, `grubtheme.go:50-53`.
- **clone-to-tmp-build-cleanup** (`mktemp -d … git clone … rm -rf`):
  `yay.go:28-31`, `grubtheme.go:31-34`.

Collapse into a small `internal/stages/helpers.go`:

```go
// ensureTool installs pkg via pacman if bin isn't on PATH.
func ensureTool(ctx *Context, bin, pkg string) error {
    if _, err := exec.LookPath(bin); err == nil {
        return nil
    }
    return ctx.R.Root("pacman", "-S", "--needed", "--noconfirm", pkg)
}

// ensureKernelParam adds one token to GRUB_CMDLINE_LINUX_DEFAULT, idempotently.
func ensureKernelParam(ctx *Context, tok string) error { /* sed from plymouth.go:48-53 */ }

// cloneBuild runs an in-checkout shell snippet against a fresh shallow clone.
func cloneBuild(ctx *Context, url, shellInCheckout string) error { /* yay.go:28-31 idiom */ }
```

Low-risk: the *emitted command strings* (what `stages_test.go` asserts on) stay identical.
New stages then become a few lines — the stated goal in `CLAUDE.md`.

### 2. A first-class hooks mechanism — HIGH value, MEDIUM cost

The new `setup` stage (`internal/stages/setup.go`, order 85) already provides `clones`
+ `commands`, but at **one fixed point** (Phase B only, after everything, user-only).
Generalize it to user-defined commands keyed to lifecycle points.

```go
// config.go
type Config struct {
    // ...existing...
    Hooks []Hook `yaml:"hooks" validate:"dive"`
}

// Hook is a user-defined command run at a named lifecycle point.
type Hook struct {
    Name   string            `yaml:"name"`
    At     string            `yaml:"at"   validate:"required,hookpoint"`
    Run    string            `yaml:"run"  validate:"required_without=Script"`
    Script string            `yaml:"script" validate:"omitempty,file"`
    Root   bool              `yaml:"root"`            // Root vs Cmd
    Env    map[string]string `yaml:"env"`
    Dir    string            `yaml:"dir"`
}
```

`At` covers two flavours of lifecycle point in one field:

- **Global:** `pre-install`, `post-install`, `pre-bootstrap`, `post-bootstrap`.
- **Per-stage:** `before:<stage>` / `after:<stage>` (e.g. `after:packages`).

Fire them **centrally** in `runPhase` (`main.go:84`), so no stage needs to know about
hooks, and route through the `Runner` so they are dry-run-recorded and testable for free:

```go
// main.go runPhase
fireHooks(ctx, phasePre(p))
for _, s := range selected {
    fireHooks(ctx, "before:"+s.Name())
    ui.Header(s.Order(), s.Name())
    if err := s.Run(ctx); err != nil { return /* ... */ }
    fireHooks(ctx, "after:"+s.Name())
}
fireHooks(ctx, phasePost(p))
```

Validating `before:`/`after:` stage names needs the registry; do it in `semanticErrors()`
by passing known names into `Validate(knownStages []string)` (keeps `config`
dependency-free, avoids an import cycle). This subsumes `setup.commands` and
`Repo.Setup`, and becomes the escape hatch that means you *don't* build bespoke Go
stages for snap/cargo/gsettings/stow — they're all one-liners through a hook.

### 3. Validate the rendered archinstall config against real archinstall — HIGH value

The project's defining risk (`CLAUDE.md`) is archinstall schema drift.

**Update (decided):** the original plan — vendor `archinstall/schema.json` from the 4.3
checkout and validate golden renders against it at `go test` time — is **not viable**.
Investigation found that the shipped `schema.json` is a *stale legacy schema*: it requires
`mirror-region`, its `bootloader` enum is `systemd-bootctl/grub-install/efistub` (not the
`"Grub"` string we emit), it models `harddrives`/`keyboard-language`/`sys-encoding`, and it
is malformed JSON (trailing comma; object-form `required`). It does not describe the 4.3
dataclass JSON we render, so it cannot serve as an oracle.

**Chosen approach (Option A):** make the oracle *real archinstall itself*, exercised in the
existing end-to-end VM/container run (see [[e2e-testing-plan]]) rather than a unit test —
feed the rendered config to archinstall and assert it is accepted. This is the truthful
check and aligns with `CLAUDE.md`'s "must be validated against a real archinstall run in a
QEMU VM" rule. (Optional cheap add-on, if fast `go test` feedback is wanted later: a
self-maintained "shape" test asserting the canonical 4.3 fields are present/correct,
updated on each `Version` bump.)

The review found **two latent drifts present today** — both still work in 4.3 but are on
its *deprecated* compat path, and the e2e acceptance check should flag them when fixed in
the A4 work:

- the bare `bootloader: "Grub"` string (`archinstall.go:306`) — 4.3 wants a
  `bootloader_config` object (`args.py:184-190`);
- the top-level `disk_encryption` (`archinstall.go:160`) — canonical is nested in
  `disk_config` only (`args.py:153-156`); you currently emit both.

---

## Subsystem A — disk / archinstall rendering

The core limitation is `Build()` (`archinstall.go:190-331`): a 140-line function that
*is* the layout, with disk1-special-casing at `:207-219` (PV split), `:238-253`
(assembly), `:286-297` (single root LV).

### A1. Layout-strategy refactor — HIGH / MEDIUM

Add a discriminator and dispatch to a small internal interface. The existing LVM logic
moves wholesale into `lvmBuilder`; this is a refactor, not a rewrite, and it subsumes
every item below.

```go
// config.go
type Disks struct {
    Layout     string       `yaml:"layout" validate:"required,oneof=lvm btrfs plain"`
    ESP        ESPConfig    `yaml:"esp"`
    Swap       SwapConfig   `yaml:"swap"`
    LVM        *LVMLayout   `yaml:"lvm"`        // required when layout==lvm
    Btrfs      *BtrfsLayout `yaml:"btrfs"`      // required when layout==btrfs
    Plain      *PlainLayout `yaml:"plain"`      // required when layout==plain
    Encryption *Encryption  `yaml:"encryption"`
}

// archinstall.go
type layoutBuilder interface {
    build(geom Geometry) ([]Device, *LvmConfiguration, error)
}
```

`Build` shrinks to: parse cross-cutting fields → select a builder from `Disks.Layout`
→ call `builder.build(geom)` → wire encryption obj_ids → assemble `Config`. Cross-field
rules ("`lvm` block required when `layout: lvm`") live in `semanticErrors()`.

### A2. btrfs + subvolumes — HIGH / MEDIUM

The most-wanted modern Arch desktop layout (snapshots via snapper/timeshift). Your
`Partition.Btrfs []any` field (`archinstall.go:96`) **already serializes subvolumes** —
it is just always empty today.

```go
type BtrfsLayout struct {
    Device     string   `yaml:"device" validate:"required,startswith=/dev/"`
    Compress   string   `yaml:"compress"`                                   // zstd → mount_options
    Snapshots  string   `yaml:"snapshots" validate:"omitempty,oneof=snapper none"`
    Subvolumes []Subvol `yaml:"subvolumes" validate:"dive"`
}
type Subvol struct {
    Name       string `yaml:"name" validate:"required"`       // @, @home, @log
    Mountpoint string `yaml:"mountpoint" validate:"required"` // /, /home
}
```

Emit one root partition with `fs_type: "btrfs"`, `mountpoint: "/"`, a populated `Btrfs`
list, and add a top-level `disk_config.btrfs_options` for snapper. **Hazard:** a naive
swapfile on btrfs corrupts — a btrfs layout needs a `nocow`/no-compress swap subvolume
(see A5).

### A3. LUKS encryption — MED-HIGH / MEDIUM

Composes with LVM as `lvm_on_luks` (`EncryptionType`, `device.py:1403`). The password is
a **top-level** `encryption_password` field, not in creds.

```go
type Encryption struct {
    Type string `yaml:"type" validate:"omitempty,oneof=luks lvm_on_luks luks_on_lvm"`
}
type DiskEncryption struct {
    EncryptionType string   `json:"encryption_type"`
    Partitions     []string `json:"partitions"`   // PV partition obj_ids
    LvmVolumes     []string `json:"lvm_volumes"`
}
```

⚠️ archinstall **rejects LVM encryption with >2 partitions** (`device.py:1476`), so
`lvm_on_luks` across multiple whole-disk PVs may not validate. This is the textbook
"reverse-engineered schema, must be VM-validated" gotcha — test in a VM.

### A4. systemd-boot + canonical bootloader shape — MEDIUM / LOW-MED

`Bootloader` enum (`bootloader.py:10`): `Systemd-boot`, `Grub`, `Efistub`, `Limine`,
`Refind`, `No bootloader`. Emit the modern object form:

```go
type BootloaderConfig struct {
    Bootloader string `json:"bootloader"` // "Grub" | "Systemd-boot"
    UKI        bool   `json:"uki"`
    Removable  bool   `json:"removable"`
}
```

**Caveat:** `installKernels` (`postinstall.go:94-117`) is GRUB-specific — it edits
`/etc/default/grub` (`GRUB_TOP_LEVEL`) and runs `grub-mkconfig`. systemd-boot needs
`bootctl set-default` / loader entries and has no `grub.cfg`. Bootloader choice touches
two files, not one.

### A5. Swap options — MEDIUM / LOW

```go
type SwapConfig struct {
    Type string `yaml:"type" validate:"omitempty,oneof=swapfile zram partition none"`
    Size string `yaml:"size" validate:"omitempty,size"`
}
```

- `zram` → set `Config.Swap = true` (archinstall's `swap` *is* zram). Best desktop
  default; sidesteps both the LVM-format limitation and the btrfs-swapfile hazard.
- `swapfile` → current behaviour (the only LVM-compatible option).
- `partition` → `linux-swap` partition; valid only for `plain`/`btrfs` layouts.
- `none` → skip.

### A6. Other rendering generality — LOW-MED / LOW

- **Multiple LVs / separate `/home`:** make `LVMLayout.Volumes` a list; the "rest of VG"
  math (`archinstall.go:279-283`) generalizes (sum fixed sizes, give the one `rest`
  volume the remainder minus headroom).
- **plain ext4/xfs (no LVM):** falls out of the strategy refactor for free.
- **NTP:** add `system.ntp bool` (hardcoded `true` at `archinstall.go:311`).
- **Network:** `system.network: nm|systemd-networkd` (hardcoded `nm` at `:316`); low
  priority for a desktop tool.
- **Multiple users:** `Creds.Users` (`archinstall.go:173`) already supports a list; only
  the renderer is single-user (`:327`). Low value for a personal box.

### Skip

- **ZFS** — archinstall's model has no ZFS (`FilesystemType`, `device.py:785`); supporting
  it means hand-rolling zpool/bootloader, which the project deliberately avoids.
- **BIOS/MBR** — UEFI is hard-required (`preflight.go:25`); high effort for hardware you
  probably don't have. Hold the line.

| # | Change | Value | Cost |
|---|--------|-------|------|
| A3-test | Validate rendered config via real archinstall in the e2e run (vendored `schema.json` is stale — unusable) | **HIGH** | LOW-MED |
| A1 | Layout-strategy interface; de-special-case `Build` | **HIGH** | MED |
| A2 | btrfs + subvolumes | **HIGH** | MED |
| A4b | Canonical `bootloader_config` + nested-only encryption | **HIGH** | LOW |
| A3 | LUKS (`lvm_on_luks`) | MED-HIGH | MED |
| A5 | Swap options (zram/partition/none) | MED | LOW |
| A4 | systemd-boot | MED | LOW-MED |
| A6 | separate /home, NTP, plain layout | MED | LOW-MED |
| — | ZFS, BIOS/MBR, multi-user | LOW | skip/defer |

---

## Subsystem B — config / stage / runner framework

### B1. CLI selection: `list-stages`, `--skip`, `stages.disable` — HIGH / LOW

Today the only selection is `--only` (single stage). Add:

- `archwright list-stages` — print order/name/phase from the registry (~15 lines).
- `--skip <stage>` (repeatable) — the inverse of `--only`; "everything except kde" is
  more useful in practice.
- `stages.disable: [kde]` in config — skip a stage without emptying its config block.
- Optionally `--from`/`--to` for resuming a half-finished run.

Implement them as one selection filter in `runPhase`, with `--only` winning over the rest.

### B2. Runner gaps — MEDIUM / LOW

`run.go` is clean but minimal. Gaps that hooks/stages (and the TUI below) need:

| Gap | Where | Fix |
|---|---|---|
| no env vars | `Cmd` `run.go:31`, `Shell` `:63` | `cmd.Env = append(os.Environ(), ...)` |
| no working dir | same | `cmd.Dir` |
| no output capture | `Cmd` streams to `os.Stdout` `:39` | `Capture(name, args) (string, error)` |
| no output sink | hardcoded `os.Stdout`/`os.Stderr` `:39`,`:70` | inject an `io.Writer` (see TUI section) |

The capture gap has teeth: `preflight.go:41` already drops to raw `exec.Command`,
bypassing dry-run and the recorded plan — exactly what `CLAUDE.md` warns against. The
**output-sink** change is also the hinge for the TUI conversion.

### B3. Smaller items

- Delete `itoa` (`stages.go:61`) — hand-rolled int→string for no reason; use
  `strconv.Itoa`. Trivial.
- **Env-var substitution** in `Load()` (`config.go:163`): run raw bytes through
  `os.Expand` (error on unset) — ~8 lines, enables secrets and variants out of the
  gitignored config.
- **Keep** the integer `Order()` scheme and flat `semanticErrors()` — both scale fine;
  don't build a dependency graph or rule registry.
- **Defer** profile/overlay-file merging until you actually run >1 machine (list-merge
  semantics are the sharp edge).

---

## Subsystem C — Phase B customization stages

Principle: **gate every tool-specific block behind a selector that degrades to a clean
no-op.** The `grub.theme.source: none` early-return and the per-field empty-skip in
`kde.go` are already the right model — apply it uniformly.

### C1. Desktop-environment selector — HIGH / MEDIUM

KDE is hardcoded (`kde.go:27-31`, `plasma-apply-*`). Don't build cross-DE theming
abstractions (a Breeze color scheme and a Hyprland config share nothing) — gate the KDE
stage and route everything else through hooks + the dotfiles repo.

```go
Desktop struct {
    Environment string `yaml:"environment" validate:"omitempty,oneof=kde gnome hyprland sway none"`
} `yaml:"desktop"`
```

```go
// kde.go Run()
if de := ctx.Cfg.Desktop.Environment; de != "" && de != "kde" {
    ui.Info("desktop.environment is %q — skipping KDE stage", de)
    return nil
}
```

### C2. Package-manager genericity — MEDIUM / LOW

- **`aur_helper: yay|paru`** — paru is argument-compatible; near drop-in. `yay.go`
  parameterizes on the package name (`yay-bin` → `paru-bin`); `aur.go:23` on the binary.
- **Flatpak remotes beyond Flathub** — model remotes as a list (`flatpak.go:32-36`
  hardcodes the Flathub URL); let each app name a remote, defaulting to `flathub`.
- **snap/cargo/npm/pipx** — *don't* add as stages; they're one-liners through hooks.

### C3. Decouple kernel-cmdline from Plymouth — MEDIUM / LOW-MED

The cmdline edit lives in `plymouth.go:47-54`, but that's GRUB's concern, not Plymouth's.
Move it to a shared, bootloader-aware helper (`ensureKernelParam`, see cross-cutting #1) —
worth doing even if you never adopt systemd-boot. When/if Phase A gains systemd-boot, the
helper branches (sed `/etc/default/grub` vs edit `/etc/kernel/cmdline`) and
`grub-mkconfig` becomes `regenerateBootConfig(ctx)`. `grubtheme.go` no-ops for
systemd-boot (no theming) via its existing `case "none"` pattern.

### C4. Dotfiles manager selector — LOW-MED / LOW

`dotfiles.manager: chezmoi|yadm|bare-git|none`, keeping chezmoi's init-vs-apply
idempotency (`chezmoi.go:35-49`). yadm is near-identical; bare-git is `clone --bare` +
checkout into `$HOME`; stow → punt to hooks.

| # | Change | Value | Cost |
|---|--------|-------|------|
| Cross-1 | Shared helpers | HIGH | LOW |
| C1 | `desktop.environment` selector | HIGH | MED |
| C2a | `aur_helper: yay\|paru` | MED | LOW |
| C2b | Flatpak remotes | MED | LOW |
| C3 | Decouple cmdline edit from Plymouth | MED | LOW-MED |
| C4 | `dotfiles.manager` selector | LOW-MED | LOW |

---

## Suggested sequencing

1. **Foundations:** shared helpers ✅, `strconv.Itoa` fix ✅, Runner
   `Env`/`Dir`/`Capture`/sink ✅, config env-var substitution ✅ (Wave 0, landed). Config
   acceptance is checked by real archinstall in the e2e run, not a unit test (see §3).
2. **Headline extensibility:** hooks mechanism ✅ + `list-stages`/`--skip`/`stages.disable` ✅
   (Wave 1, landed).
3. **Disk genericity:** layout-strategy refactor + fix the two deprecated archinstall
   shapes, then btrfs / swap / LUKS / systemd-boot as independent builders, each with a
   golden fixture.
4. **Phase B selectors:** `desktop.environment` ✅, `aur_helper` ✅, flatpak remotes ✅
   (Wave 1, landed); decouple cmdline from Plymouth (C3) still pending.

Recurring theme: **prefer one good escape hatch (hooks + dotfiles repo) over many bespoke
Go stages.**

---

## Wave 1 retrospective — notes for the next agent

Wave 1 (hooks, stage selection, Phase B selectors) was built by **three parallel worktree
agents** merged into one branch. What worked, and what to fix next:

### Process that worked (reuse it)
- **Partition by file ownership, not just by feature.** `config.go` is the shared hotspot
  — every wave adds fields. Give each agent a *distinct anchor* (top-of-struct vs. a named
  sub-block vs. end-of-struct); the additive struct edits then auto-merge. The only
  hand-merges needed were `main.go`'s `runPhase` (two agents touched it) and one adjacent
  type-declaration spot.
- **New test files, never shared fixtures.** Agents added `*_test.go` files with
  self-contained config snippets instead of editing the shared `testYAML`/`testConfig` in
  `stages_test.go`. Zero test-file conflicts — keep this rule.
- **Background agents need pre-authorised tools.** They can't answer permission prompts, so
  `Edit`/`Write`/`Bash(go …)`/`Bash(git …)` must be on the allowlist or every write
  auto-denies. They also tend to sweep untracked files in via `git add -A` — integrate with
  explicit `git add <paths>` and check the net diff before pushing.
- **gofmt + build/vet/test after every merge.** Merges can leave struct-tag alignment
  unformatted (`gofmt -l` caught `config.go`). Worth a CI `gofmt -l` gate.

### Concrete follow-ups found while dogfooding (fix in a future wave)
- **Hooks `script`/`dir` don't expand `~`, and `script` is validated with `file`** (the
  path must exist at validate time). This is inconsistent with `setup`'s `expandHome`
  (`setup.go`). Recommend: run `script`/`dir` through `expandHome`, and reconsider the eager
  `file` existence check — a hook script may be produced by an *earlier* hook/stage in the
  same run, so validate-time existence is the wrong check. Until fixed, `script` must be an
  existing absolute path.
- **Env-substitution scans the whole file, comments included.** A literal `$` in any comment
  or value errors unless doubled (`$$`). This is a sharp edge for users (a `$` in a config
  comment fails the run) and it bites the remote/layered-config wave, where substitution
  composes with merge. Consider expanding only string *values* by walking the parsed YAML
  node tree, instead of `os.Expand` over raw bytes (`config.go:expandEnv`).
- **C3 (decouple the kernel cmdline from Plymouth) is still pending** — the one Phase B
  selector item not done in Wave 1. Cheap; fold it into the systemd-boot work (A4), since
  both want a bootloader-aware `regenerateBootConfig`/`ensureKernelParam` helper.

### Repo hygiene (orthogonal)
`.claude/settings.local.json` is tracked on `main` — machine-local editor/agent settings
that should be gitignored. Wave 1 deliberately left it as-is; a separate cleanup PR should
`git rm --cached` it and add the `.gitignore` entry.

---

## TUI conversion — scrollable viewport

Goal: make `archwright` feel like a TUI, with all output in a scrollable
`charmbracelet/bubbles` viewport rather than scrolling off the terminal.

### The hard constraint (read this first)

`CLAUDE.md` deliberately forbids a bubbletea spinner today, for a real reason:

> *No bubbletea/bubbles spinner anywhere, deliberately — it would swallow streamed
> pacstrap/yay output. The Runner streams stdout/stderr straight through.*

A viewport TUI **does not get to ignore this** — it has to *solve* it. bubbletea takes
over the terminal (alt-screen) and owns the render loop, so subprocess output can no
longer go straight to `os.Stdout`; it must be **captured and fed into the model** as
messages, then rendered into the viewport. The whole design below is about doing that
capture correctly so long installers (`pacstrap`, `yay` building, `archinstall --silent`)
remain fully visible and live.

This also means the constraint in `CLAUDE.md` should be **rewritten, not deleted**: the
rule becomes "output must be piped into the viewport line-stream, never written directly
to `os.Stdout` while the TUI owns the screen."

### Architecture

```
                ┌──────────────────────────────────────────────┐
   goroutine    │ runPhase(stages...)                           │
   (worker)     │   stage.Run(ctx) → ctx.R.Cmd(...)             │
                │        │ writes lines                         │
                │        ▼                                      │
                │   Runner.Out  (io.Writer)  ── program.Send ──►│ tea.Msg{outputLine}
                └──────────────────────────────────────────────┘
                                                                 │
   main thread  ┌──────────────────────────────────────────────▼─┐
   (tea loop)   │ model.Update → append line, autoscroll          │
                │ model.View   → header + viewport + footer        │
                └─────────────────────────────────────────────────┘
```

1. **Inject an output sink into the `Runner`** (the B2 change). Replace the hardcoded
   `os.Stdout`/`os.Stderr` in `Cmd`/`Shell` (`run.go:39`, `:70`) with an `io.Writer`
   field:

   ```go
   type Runner struct {
       DryRun bool
       Sudo   bool
       Out    io.Writer // defaults to os.Stdout in plain mode; a TUI pump in TUI mode
       Plan   []string
   }
   ```

   Plain/CLI mode keeps `Out = os.Stdout` and behaves exactly as today (no regression for
   non-TTY, CI, `--dry-run | less`). TUI mode sets `Out` to a writer that forwards each
   line to the bubbletea program.

2. **The pump writer** turns bytes into messages:

   ```go
   type teaWriter struct{ p *tea.Program }
   func (w teaWriter) Write(b []byte) (int, error) {
       w.p.Send(outputMsg(string(b)))   // one msg per write; split on \n in Update
       return len(b), nil
   }
   ```

   Subprocess stdout+stderr both point at this writer, so interleaving matches the
   terminal. (For clean line handling, wrap with a `bufio.Scanner` in a small goroutine,
   or buffer partial lines in the model.)

3. **Run the phase in a goroutine**; the tea program runs on the main thread:

   ```go
   p := tea.NewProgram(newModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
   ctx.R.Out = teaWriter{p}
   go func() {
       err := runStages(ctx, selected, func(s Stage){ p.Send(stageMsg{s.Order(), s.Name()}) })
       p.Send(doneMsg{err})
   }()
   _, err := p.Run()
   ```

4. **The model** is a viewport plus a header/footer:

   ```go
   type model struct {
       vp      viewport.Model
       buf     strings.Builder // full scrollback
       stage   string          // current stage header
       spin    spinner.Model   // now allowed: output no longer goes to os.Stdout
       done    bool
       err     error
       follow  bool            // auto-scroll to bottom unless the user scrolled up
   }
   ```

   - `Update`: on `outputMsg`, append to `buf`, `vp.SetContent(buf.String())`, and if
     `follow` then `vp.GotoBottom()`. On `tea.KeyMsg` (`PgUp`/`k`/mouse wheel) set
     `follow=false`; `End`/`G` re-enables follow. On `tea.WindowSizeMsg` resize the
     viewport (leave rows for header/footer). On `stageMsg` update the header; on
     `doneMsg` set `done`/`err` and stop the spinner.
   - `View`: `header (stage + spinner) + vp.View() + footer (scroll %, keybinds)`.
     Reuse the existing `lipgloss` styles from `internal/ui/ui.go` for the header banner.

5. **Interactive prompts** are the real complication: huh runs its *own* bubbletea
   program, and you can't run two at once inside the alt-screen. Two clean options:

   - **(Recommended) Collect all input before the TUI starts.** The destructive
     `ConfirmErase` and the `Password` prompt (`ui.go:57`, `:85`) already run *before*
     any long work in `archinstall.go:38-53`. Run them first, in normal terminal mode,
     then enter the alt-screen TUI for execution. Simplest and robust.
   - **(Later) Embed huh forms as states** in the model (huh integrates with bubbletea):
     the model transitions `confirm → password → running`. More work; only needed if a
     prompt must appear mid-run.

### Mode selection & fallback

- Auto-detect: use the TUI only when stdout is a TTY (`term.IsTerminal`) **and** not
  `--dry-run` piped. Add an explicit `--plain` (and/or `--tui`) flag to override.
- Plain mode is the existing code path unchanged — keep it as the fallback for CI,
  non-TTY, and `| less`. This means the `ui` package grows a "plain vs program" notion;
  `Step`/`OK`/`Info`/`Warn` either print to `os.Stderr` (today) or `program.Send` a
  styled line (TUI).

### Scope / sequencing for the TUI

1. Land the **Runner output-sink** refactor first (it's the B2 item; valuable on its own).
2. Route `ui.Step`/`OK`/`Info`/`Warn`/`Header` through the same sink abstraction so all
   output has one path.
3. Add the `internal/tui` package (model/update/view) and wire `runPhase` to it behind a
   TTY check + `--plain` flag.
4. Promote `bubbletea`/`bubbles` from indirect to direct deps in `go.mod` (already in the
   module graph via `huh`).
5. Update `CLAUDE.md`: replace the "no bubbletea" gotcha with the new rule — output must
   flow through the viewport sink, never directly to `os.Stdout` while the TUI owns the
   screen.

### Risks / watch-items

- **Output volume:** `pacstrap` and `yay` builds emit a lot; keep the full scrollback in a
  buffer but consider a cap (e.g. last N lines / bytes) if memory or render cost bites.
- **ANSI in child output:** pacman/yay emit colour and progress carriage-returns. The
  viewport renders text; `\r`-based progress bars will look messy. Either strip `\r`
  redraws or accept that progress lines append. Test with a real `pacstrap`.
- **Performance:** one `program.Send` per write can flood the loop. Batch with a scanner
  and send whole lines, or coalesce on a short tick.
- **Resize correctness:** recompute viewport height on every `WindowSizeMsg`.
- **Don't regress plain mode:** the existing streaming behaviour must remain byte-for-byte
  for non-TTY/CI — that path is what tests and `--dry-run` pipelines rely on.

---

## Remote & layered configuration (future)

Goal: point `archwright` at a config that lives in a git repo / at a URL, and let that
config pull in and **merge** other configs — so machine-specific config can be tiny and
sit on top of a shared base.

```sh
archwright install --config github.com/AdamJHall/dotfiles/archwright.desktop.yaml
```

```yaml
# archwright.desktop.yaml  (the entry point: desktop-specific)
imports:
  - archwright.base.yaml                                  # sibling file in the same repo
  - github.com/AdamJHall/dotfiles/archwright.kde.yaml     # another file / repo
  - https://example.com/teams/shared.yaml                 # raw URL

system:
  hostname: desktop-box        # overrides whatever base set
packages:
  - steam                      # added on top of base's packages
```

This **subsumes and supersedes** the deferred local-overlay idea (B3): the same deep-merge
engine powers both repeated `--config a --config b` (local overlays) and the in-file
`imports:` key (remote/relative). It also composes with env-var substitution (B3): expand
each file's `${VAR}` *after* fetch, *before* merge. (Heads-up from Wave 1: substitution
today scans raw bytes including comments — see the Wave 1 retrospective; moving to
value-only expansion is cleaner and a prerequisite for sane merging here.)

### Config-source resolution

`--config` (and each `imports:` entry) accepts three reference forms, distinguished by
shape:

| Form | Example | Resolves to |
|------|---------|-------------|
| local path | `config.yaml`, `./desktop.yaml` | filesystem read (today's behaviour) |
| github shorthand | `github.com/OWNER/REPO/path/to.yaml[@ref]` | `https://raw.githubusercontent.com/OWNER/REPO/<ref-or-default>/path/to.yaml` |
| raw URL | `https://…/file.yaml` | HTTP GET |

A bare relative path **inside an `imports:` list resolves against the importing file's
location**, not the CWD — so a sibling in the same repo is just `archwright.base.yaml`, and
a github-rooted entry point makes its relative imports github-rooted too (fetch the sibling
raw URL). This is the key ergonomic the request asks for.

### Merge semantics

Layering is **base-first, importer-wins**, applied recursively:

1. An imported file is resolved and merged *before* the file that imports it.
2. Among multiple `imports:`, **later entries override earlier** ones.
3. The importing file's own top-level keys override everything it imported.
4. Imports are processed depth-first; an imported file may itself have `imports:`.

So in the example, effective precedence (low → high) is:
`base.yaml` → `kde.yaml` → `shared.yaml` → `desktop.yaml`.

**The sharp edge is list fields.** Deep-merging maps is unambiguous; lists are not, and the
right answer differs per field:

- **Union/append** is what you want for additive string lists: `packages`, `flatpaks`,
  `aur`, `system.locales`, `user.groups`, `hooks`, `repos`. (Dedup plain string lists.)
- **Replace** is what you want for identity/layout lists where appending is nonsense:
  `disks.lvm.pvs`, `disks.btrfs.subvolumes`.

Recommended pragmatic default for a personal tool: **maps merge recursively; string-slice
fields union+dedup; structured-slice fields replace** (or, better, key-merge by `name`
where the element has one — `repos`, `hooks`, flatpak remotes — so a later layer can
override a single named entry). Provide an explicit escape hatch for the rare override:

```yaml
packages: !replace [vim, git]   # ignore inherited packages, use exactly this
```

Document the per-field strategy in one table next to the merge code, and add golden tests
for each (the existing golden harness makes this cheap). Don't try to be clever beyond
this.

### The Phase A → Phase B flattening rule (important)

Phase A runs from the live ISO (network + `curl`/`git` available, since archinstall needs
them) and **stages the binary + config into the target for Phase B** (`archinstall.go:243`,
`stageBinary`). With remote/layered config, do the fetch+merge **once** in Phase A, then
stage the **flattened, resolved config** (a single concrete YAML with no `imports:`) into
the target. Phase B then reads a plain local file and needs no network, no re-fetch, and is
guaranteed to see byte-identical config to Phase A. Never re-resolve remotely in Phase B.

(`archwright render --config <ref> -o config.flat.yaml` — a "resolve & merge, write the
result, change nothing" command — is the natural primitive here, and doubles as the
debugging tool for the merge engine.)

### Trust, pinning, caching, auth

Fetching config that drives **destructive disk operations and arbitrary hook commands**
from a URL is a real trust boundary — treat it like one:

- **Pin a ref.** Encourage `@<tag-or-sha>` on github shorthands; warn (or, with
  `--strict`, refuse) on an unpinned `main`. Optionally record the resolved commit SHA into
  the flattened config's provenance comment.
- **Show before you run.** Phase A is already gated by the `ConfirmErase` prompt; for a
  remote config, print the resolved source list + merged result (or a diff) before that
  prompt so the user sees exactly what they're about to execute.
- **Cache + `--offline`.** Cache fetched files (e.g. under `$XDG_CACHE_HOME/archwright/`)
  keyed by URL+ref; `--offline` uses the cache only. Useful when re-running on a flaky live
  ISO network.
- **Private repos.** Public raw URLs cover public dotfiles. For private, support a token via
  env (`GITHUB_TOKEN`) in the `Authorization` header, or fall back to `git clone` over the
  user's existing credentials. Keep tokens out of the config file (they belong in env, which
  the substitution feature already reads).
- **Bound recursion.** Detect import cycles (track visited canonical URLs) and cap import
  depth; fail with the cycle path, not a stack overflow.

### Implementation sketch

A small `internal/configsrc` (resolver) sitting in front of the existing `config.Load`:

```go
// Resolve fetches ref (local | github | url), expands env, recursively resolves its
// imports, and deep-merges into one *config.Config. visited guards against cycles.
func Resolve(ref string, base *url.URL, visited map[string]bool) (*config.Config, error)

// raw bytes → struct merge; layer order is base-first, importer-wins (see table).
func merge(dst, src map[string]any, strategy fieldStrategy) // generic map/list merge
```

Notes:

- Merge at the **generic `map[string]any`** level (parse each layer with `yaml.Node` /
  `map[string]any`, merge, then `yaml.Unmarshal` the result into `config.Config` and
  `Validate()` once at the end). This keeps the merge engine independent of the config
  schema, so new config fields need no merge-code changes — only the per-field list
  strategy table does.
- `imports:` is a resolver-level key, consumed and stripped before the final
  `config.Config` unmarshal (it isn't a `Config` field).
- `--config` becoming repeatable (`[]string`, last wins) is the same merge applied to
  CLI-supplied layers — implement once, reuse.

### Priority / cost

**Defer** (this is explicitly a "for later" item). It's MEDIUM value (great quality-of-life
for a multi-machine dotfiles-driven setup), MEDIUM-HIGH cost — the generic deep-merge with a
sane per-field list strategy is the bulk of the work, and the trust/pinning story must land
with it, not after. Sequence it **after** the framework foundations (env-var substitution
and the Runner/selection work), since it reuses the substitution hook and the `render`
command.
