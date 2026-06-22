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
main.go                 cobra CLI: install / bootstrap / validate / render + flags
internal/config/        Config struct; Validate() via go-playground/validator struct tags
internal/configsrc/     resolve remote/layered config: --config refs (local/github/url),
                        imports: recursion, ${VAR} expand, deep-merge -> flattened config
internal/archinstall/   render config.yaml -> archinstall config + creds JSON (Phase A core)
internal/run/           Runner: Cmd/Shell/Chroot/Root/Try, dry-run, recorded .Plan
internal/ui/            stderr-bound lipgloss renderer (TTY/NO_COLOR aware) + log +
                        huh prompts; run banner/[i/n] stage headers/timing/summary
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
- **No bubbletea/bubbles spinner** anywhere, deliberately — it would swallow streamed
  pacstrap/yay output. The `Runner` streams stdout/stderr straight through.
- **Phase A is destructive.** It erases the configured disks. `config.yaml` is gitignored.
  Every state-changing command is recorded into `.Plan` and printed under `--dry-run`
  without executing — always exercise `--dry-run` first.
- **Don't reintroduce a `yq` shell-out.** Arch's `extra/yq` is the Python jq-wrapper, not
  mikefarah Go yq. The code parses YAML directly with `gopkg.in/yaml.v3`; keep it that way.
- **Resolve remote/layered config once, in Phase A.** `internal/configsrc` fetches +
  deep-merges `imports:`/multiple `--config` refs into one *flattened* config (no `imports:`
  key). Phase A stages those flattened bytes (`Context.FlatConfig`) into the target, so Phase
  B reads a single concrete local file — **never re-fetch or re-merge in Phase B.** Merge
  rules: maps recurse, string lists union+dedup, name-keyed structured lists merge by `name`,
  `!replace` forces wholesale replace. `configsrc.Load` returns the merged config *and* the
  flattened bytes; `render` writes them out without running stages.

## Building features with parallel agents (wave playbook)

This codebase's extensibility was built across six "waves," each a fan-out of worktree
subagents merged into one branch + PR. The hard-won process rules (distilled from the wave
retrospectives — keep them, they paid off every time):

- **Partition by file *region* ownership, not just by feature.** The shared hotspots are
  `config.go` (every feature adds a struct field) and `main.go`'s `runPhase`/`renderConfig`.
  Give each agent a **distinct anchor** in a shared file (top-of-struct vs. a named sub-block
  vs. end-of-struct; different functions in `main.go`). Additive edits at disjoint regions
  **auto-merge** — most waves needed zero hand-merges.
- **Additive work fans out in parallel; layered work is sequenced into rounds.** Independent
  struct fields/stages → one parallel round. A genuine dependency chain (e.g. configsrc API ←
  CLI wiring) → **Round 1 builds the foundation alone, Round 2 fans out off the integrated
  branch.** Forcing layered work parallel just creates stub-and-reconcile churn.
- **Agree the API contract up front.** When Round 2 codes against a Round 1 package, put the
  exact signatures in *both* prompts — zero interface drift.
- **The orchestrator does mechanical cross-cutting edits centrally.** A signature migration
  (e.g. adding a return value to a function called from N sites) is done once by the
  orchestrator with `_` placeholders; each Round-2 agent then flips only *its own* `_`→var in
  *its own* function, so the shared file still auto-merges.
- **New test files, never shared fixtures.** Agents add `*_test.go` with self-contained config
  snippets instead of editing the shared `testYAML`/`testConfig`/golden cases. This is the
  rule that keeps test files conflict-free. (When two agents *must* both make a field
  `required`, every shared fixture conflicts — union the hunks, but check each "theirs" side
  for a key the *other* agent deleted.)
- **Default-preserves-behaviour gating.** Every new feature must degrade to today's output when
  its config is unset, so all existing golden snapshots pass *without* `-update`. New behaviour
  is proven by inline-config field-assertion tests, never by touching the goldens. For a schema
  shape change, use **two commits**: the behaviour-preserving refactor (goldens unchanged) then
  the shape change (regenerate goldens) — the diff stays reviewable.
- **Unowned-file consequences: agent reports, orchestrator fixes.** If a feature has a knock-on
  effect in a file no agent owns (a registry expectation, a helper keyed off a now-empty field),
  have the agent flag it rather than reach outside its region; the orchestrator fixes it
  centrally and adds the regression test.
- **Worktree mechanics.** Use the Agent tool's built-in `isolation: "worktree"` (rooted under
  `.claude/worktrees/`, inside the sandbox) — never hand-roll external `git worktree add` dirs,
  they're unreachable from the agent sandbox. Each worktree branches off `main`'s HEAD, so a
  Round-2 agent must `git reset --hard <integration-branch>` first — **tell it the base branch
  in the prompt.** `.claude/worktrees/` is gitignored; never `git add -A` (it sweeps in the
  worktree checkouts as embedded repos and untracked files — stage explicit paths).
- **After every merge:** `gofmt -l .` (catches struct-tag realignment), `go build/vet/test
  ./...`. Background agents can't answer permission prompts, so pre-authorise
  `Edit`/`Write`/`Bash(go …)`/`Bash(git …)` or every write auto-denies.
- **The TUI is a known dead-end.** A scrollable bubbletea viewport was built and reverted: the
  alt-screen owns stdin, so the arbitrary interactive subprocess prompts archwright runs
  (`repos[].setup`, e.g. `cachyos-repo.sh`) can't be answered. Don't re-attempt without a PTY
  multiplexer plan and up-front VM testing — see the "no bubbletea spinner" gotcha above.
