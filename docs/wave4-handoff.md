# Wave 4 — handoff / resume notes

Status: **planning complete, not yet started.** Paused at 91% usage limit before launching
agents. Resume by launching the three worktree agents below, merging into one branch, and
opening a PR. Everything below is the result of reviewing `docs/extensibility-review.md` and
the current `main` (Wave 3 landed, commit `7d97bdd`).

## What "next wave" means (from the review doc)

`extensibility-review.md` "Next wave starts here" names three candidates:
1. **TUI conversion** (scrollable viewport) — big, fully designed in the doc's TUI section.
2. **Remote & layered configuration** — big, but **explicitly deferred** ("Defer until you
   actually run >1 machine"). **Excluded from Wave 4** — respect the deferral.
3. **Snapper wiring** for `btrfs.snapshots: snapper` — carried in config since Wave 2 but
   not provisioned (archinstall.go:626 leaves it "to a post-install/hook step").

## Chosen Wave 4 scope (3 parallel worktree agents → 1 branch → PR)

Partitioned by **file ownership** per the Wave 1–3 retrospective rule (distinct anchors in
shared hotspots; new test files only, never shared fixtures; explicit `git add <paths>`).

### Agent A — TUI conversion (headline)
- **New** `internal/tui/` package: bubbletea model/update/view (viewport + header/footer),
  `teaWriter` pump that forwards Runner output as `tea.Msg`. Design is fully spec'd in
  `extensibility-review.md` → "TUI conversion" section (model fields, follow/autoscroll,
  WindowSizeMsg resize, doneMsg/stageMsg).
- Route `internal/ui/ui.go` output through a sink so Step/OK/Info/Warn/Header can go to the
  viewport in TUI mode and to os.Stderr in plain mode.
- Wire `runPhase` (main.go) to run the phase in a goroutine under `tea.Program`, behind a
  TTY check + new `--plain`/`--tui` flag. **Collect prompts (ConfirmErase/Password) BEFORE
  entering the alt-screen** (recommended option in the doc).
- Promote `bubbletea`/`bubbles` from indirect → direct in `go.mod` (already in module graph
  via huh; Runner `Out io.Writer` sink already exists from Wave 0 — `internal/run/run.go:26`).
- Plain mode must stay **byte-for-byte unchanged** for non-TTY/CI/`--dry-run | less`.
- Update `CLAUDE.md`: replace the "no bubbletea spinner" gotcha with "output must flow
  through the viewport sink, never directly to os.Stdout while the TUI owns the screen."
- **Owns:** `internal/tui/*` (new), `internal/ui/ui.go`, `main.go` (runPhase + `--plain`
  flag), `go.mod`/`go.sum`, `CLAUDE.md` gotcha line.
- **Tests:** table-driven model Update tests (outputMsg appends + autoscroll; key toggles
  follow; resize), plain-mode-unchanged assertion. New `internal/tui/*_test.go`.

### Agent B — Snapper provisioning stage (closes Wave 2 gap)
- **New** `internal/stages/snapper.go` + `internal/stages/snapper_test.go`. Phase B stage.
- No-op unless `ctx.Cfg.Disks.EffectiveLayout() == "btrfs"` **and**
  `ctx.Cfg.Disks.Btrfs.Snapshots == "snapper"` (degrade-to-clean-no-op, the project's
  uniform pattern — mirror `dotfiles.go`'s selector style).
- When active: `ensureTool` snapper + install `snap-pac`; create root config
  (`snapper -c root create-config /`), set sane retention, enable
  `snapper-timeline.timer` + `snapper-cleanup.timer` (all via `ctx.R.Root`, idempotent /
  grep-guarded where needed — reuse `internal/stages/helpers.go`).
- Pick an Order that fits Phase B (e.g. after packages, before dotfiles — check current
  orders via `list-stages`; keep stable).
- **Owns:** only the two new files. **Zero** overlap with A or C.
- **Tests:** dry-run plan assertions (active → expected commands; snapshots:none and
  non-btrfs layouts → empty/skip). New test file, self-contained inline config.

### Agent C — `--from` / `--to` stage resume (B1 remaining ergonomic)
- Add `--from <stage>` / `--to <stage>` persistent flags (name-or-number, like `--only`).
- Implement as a **post-filter applied AFTER `stages.Select(...)`** in runPhase — do NOT
  change the `Select(...)` signature/call line (that line is in runPhase which Agent A
  rewrites; keeping it intact avoids a guaranteed conflict). e.g. add
  `stages.Within(selected, from, to) []Stage` in `stages.go` and call it on the result.
- `--only` still wins over everything; `--from/--to` compose with `--skip`/`disable`.
- **Owns:** `internal/stages/stages.go` (new `Within` func), `main.go` (flag declarations
  block + one added filter line in runPhase), new `internal/stages/fromto_test.go`.
- **Tests:** table-driven `Within` (from only, to only, both, inverted bounds, unknown
  stage). New test file.

### Known merge points (orchestrator hand-merges — expected, per retrospectives)
- `main.go`: A and C both add persistent flags (distinct lines) and both touch runPhase
  (A rewrites execution; C adds one filter line). Hand-merge the flag block + runPhase.
- Everything else auto-merges (distinct files / distinct regions).

## Execution plan (resume here)

1. Launch agents A, B, C in parallel via the **Agent tool with `isolation: "worktree"`**
   (NOT hand-rolled `git worktree` — Wave 2 retro: external worktrees are outside the
   sandbox and get denied). Run foreground or background; each returns its final summary.
2. Each agent: **TDD red-green-refactor**, idiomatic Go (`golang-patterns`), table-driven
   tests (`golang-testing`), `gofmt`/`go vet`/`go build`/`go test ./...` clean before
   reporting done. Conventional-commit messages (subject ≤50 chars, body only when "why"
   isn't obvious), each commit trailer:
   `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
3. Create `wave4/tui-snapper-fromto` off `main`. Merge each agent branch/worktree in.
   Hand-merge `main.go`. Run `gofmt -l .` (must be empty), `go vet ./...`,
   `go build -o archwright .`, `go test ./...` — **execute tests, confirm green**.
4. Fix any cross-file follow-ups centrally (Wave 3 pattern: e.g. an unowned file affected
   by a feature — agents should *report* such, orchestrator fixes + adds a regression test).
5. Update `docs/extensibility-review.md`: add a **Wave 4 retrospective** and flip the TUI /
   snapper items to ✅; note remaining deferred items (remote/layered config; VM-validation
   caveats carried from Waves 2–3).
6. Open PR with `gh` into `main`. PR body ends with:
   `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
7. **Loop until complete** (the user's instruction): if tests fail or review surfaces
   issues, iterate before declaring done.

## Grounding facts gathered (so resume needs no re-discovery)

- Runner already has `Out io.Writer` sink + `Env`/`Dir`/`Capture` (Wave 0) —
  `internal/run/run.go:19-101`. TUI just needs to set `Out` to the pump writer.
- `bubbletea` v1.3.6 + `bubbles` v0.21.x already in `go.mod` as indirect (via huh).
- `ui` output all goes to `os.Stderr` today (`internal/ui/ui.go`); `Header/Step/OK` use
  lipgloss, `Info/Warn/Error` use charmbracelet/log, prompts use huh.
- Config paths: `ctx.Cfg.Disks DisksConfig` (`config.go:46`), `DisksConfig.EffectiveLayout()`
  (`config.go:139`), `Disks.Btrfs *BtrfsLayout` with `Snapshots` field validated
  `oneof=snapper none` (`config.go:205-208`). Example config documents
  `snapshots: snapper   # ... (snapper setup itself is left to a hook)` (config.example.yaml:88).
- Stage pattern reference: `internal/stages/dotfiles.go` (selector + degrade-to-no-op),
  shared helpers in `internal/stages/helpers.go` (`ensureTool`, grep-guarded sed, etc.).
- Stage selection: `stages.Select(p, only, skip, disable)` (`stages.go:66`); `For`,
  `All` also there. `list-stages` command already exists (main.go:85).
- Tests never touch disks: run a stage in dry-run, assert on `ctx.R.Plan`. `go test ./...`,
  `go vet ./...`, `go build -o archwright .`.

## Excluded from Wave 4 (deliberate)
- **Remote/layered config** — explicitly deferred in the doc; big merge-engine + trust
  story; do after >1 machine need.
- **VM-validation caveats** (Waves 2–3 reverse-engineered archinstall shapes) — need a real
  archinstall 4.3 run in QEMU; tracked in `docs/e2e-testing-plan.md`, not a code wave.
