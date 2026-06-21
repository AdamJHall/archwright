# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single static Go binary (`archwright`) that rebuilds an Arch Linux machine from bare
disks to a themed KDE desktop, driven by one declarative `config.yaml`. Not NixOS: no
purity, no rollback, no DSL — plain YAML plus a binary that orchestrates archinstall and
the usual Arch tools. The README is comprehensive; read it for the user-facing workflow.

Two phases, each a sequence of numbered, individually re-runnable, dry-run-aware stages:
- **`install` (Phase A)** — live ISO, as root. Renders `config.yaml` into an archinstall
  config and lets the official installer do partitioning/LVM/pacstrap/bootloader. Only two
  stages: `preflight` and `archinstall`.
- **`bootstrap` (Phase B)** — booted system, as your user. Post-install customization:
  yay, packages, flatpaks, 1Password, Plymouth, GRUB/KDE theming, `chezmoi init --apply`.

## Commands

```sh
go build -o archwright .   # build
go test ./...              # unit tests (validation table + per-stage command plans)
go vet ./...
go test ./internal/stages/ -run TestPackages   # single test
```

Tests never touch disks: they run a stage in dry-run and assert on the recorded command
plan. There is no separate lint config beyond `go vet`.

## Architecture

```
main.go                 cobra CLI: install / bootstrap / validate + persistent flags
internal/config/        Config struct; Validate() via go-playground/validator struct tags
internal/archinstall/   render config.yaml -> archinstall config + creds JSON (Phase A core)
internal/run/           Runner: Cmd/Shell/Chroot/Root/Try, dry-run, recorded .Plan
internal/ui/            charmbracelet log + lipgloss styling + huh confirm prompts
internal/stages/        one file per stage; self-registering ordered registry
```

### Adding/editing a stage

A stage is a tiny struct implementing `Order() int`, `Name() string`, `Phase() Phase`,
`Run(ctx *Context) error` (see `internal/stages/stages.go`). It registers itself in
`init()` via `register(...)` — wiring stays local to the file, no central list. `Order` is
the numeric prefix (10, 20, …); keep it stable so `--only <number>` still works. `--only`
matches a stage by name **or** number. Look at `packages.go` for the minimal pattern.

`Run` does all side effects through `ctx.R` (the `Runner`), never `os/exec` directly —
that is what makes the stage testable and dry-run-safe. Use `Root` for privileged commands
(it adds `sudo` in Phase B and runs direct in Phase A, keyed off `Runner.Sudo`), `Cmd` for
unprivileged, `Shell` only when you genuinely need pipes/redirects/conditionals, `Try` for
best-effort steps (the `|| true` analogue), `Chroot` for Phase A arch-chroot work.

### Config = schema

Validation rules live as `validate:` struct tags in `internal/config/config.go`
(go-playground/validator). The struct *is* the schema; there is no separate spec. Errors
are mapped to YAML-path messages and joined so `validate` reports every problem at once.
Add fields with their tags, then a table case in `config_test.go`.

## Key gotchas

- **archinstall JSON is not a stable API.** Its schema changes between releases. We render
  against the pinned `Version` in `internal/archinstall/archinstall.go`; preflight only
  *warns* on mismatch. The JSON shape (LVM, swap, PV `obj_id` wiring, creds keys) was
  reverse-engineered from archinstall source and is best-effort — **it must be validated
  against a real archinstall run in a QEMU VM** before trusting on hardware. After any
  archinstall version bump, diff the schema and update this package + `Version` together.
- **No 100%FREE sentinel** in the rendered config — every size is concrete bytes, so
  `archinstall.Build` computes "rest of disk"/"rest of VG" from probed `Geometry`
  (device→bytes). Whole-disk PVs become full-disk *partitioned* PVs (accepted deviation;
  archinstall can't do raw whole-disk PVs).
- **Output must flow through the viewport sink, never to `os.Stdout`/`os.Stderr` while
  the TUI owns the screen.** In TUI mode (`internal/tui`) bubbletea takes the alt-screen, so
  every byte — subprocess stdout/stderr *and* styled `ui` lines — is pumped into the viewport
  via the `teaWriter` (set as `Runner.Out` and `ui.SetSink`). Plain mode (`--plain`, non-TTY,
  CI, `--dry-run | less`) is the unchanged `os.Stderr` path and must stay byte-for-byte
  identical. The TUI is skipped whenever a huh prompt would fire (interactive Phase A install)
  since two bubbletea programs can't share the terminal — prompts run first, in plain mode.
- **Phase A is destructive.** It erases the configured disks. `config.yaml` is gitignored.
  Every state-changing command is recorded into `.Plan` and printed under `--dry-run`
  without executing — always exercise `--dry-run` first.
- **Don't reintroduce a `yq` shell-out.** Arch's `extra/yq` is the Python jq-wrapper, not
  mikefarah Go yq. The code parses YAML directly with `gopkg.in/yaml.v3`; keep it that way.
