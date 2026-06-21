// Command archwright is the single-binary, declarative Arch Linux installer.
//
//	archwright install     [--dry-run] [--only <stage>] [--skip <stage>] [--config <file>] [--yes]
//	archwright bootstrap   [--dry-run] [--only <stage>] [--skip <stage>] [--config <file>]
//	archwright validate    [--config <file>]
//	archwright list-stages
//
// Phase A (install) runs from the Arch live ISO as root; Phase B (bootstrap)
// runs on the booted system as your user. Stages live in internal/stages.
// Stage selection: --only (single stage) wins; otherwise --skip and the
// stages.disable config list subtract from the full set. User-defined hooks fire
// at lifecycle points (pre/post-{install,bootstrap}, before:/after:<stage>).
package main

import (
	"fmt"
	"os"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/run"
	"github.com/AdamJHall/archwright/internal/stages"
	"github.com/AdamJHall/archwright/internal/tui"
	"github.com/AdamJHall/archwright/internal/ui"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// version is overwritten at build time via -ldflags "-X main.version=..."
// (goreleaser sets this from the git tag).
var version = "dev"

// Persistent flag values, bound on the root command.
var (
	flagDryRun bool
	flagOnly   string
	flagSkip   []string
	flagFrom   string
	flagTo     string
	flagConfig string
	flagYes    bool
	flagPlain  bool
)

func main() {
	root := &cobra.Command{
		Use:           "archwright",
		Short:         "Declarative, Arch Linux reinstall driven by config.yaml",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.BoolVar(&flagDryRun, "dry-run", false, "print commands instead of running them")
	pf.StringVar(&flagOnly, "only", "", "run a single stage by name or number")
	pf.StringArrayVar(&flagSkip, "skip", nil, "skip a stage by name or number (repeatable)")
	pf.StringVar(&flagFrom, "from", "", "resume from a stage by name or number (inclusive)")
	pf.StringVar(&flagTo, "to", "", "stop after a stage by name or number (inclusive)")
	pf.StringVar(&flagConfig, "config", "config.yaml", "path to config.yaml")
	pf.BoolVar(&flagPlain, "plain", false, "force plain streaming output instead of the scrollable TUI")

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Phase A: partition, format, pacstrap and install GRUB (run from the live ISO as root)",
		RunE:  func(_ *cobra.Command, _ []string) error { return runPhase(stages.Install) },
	}
	installCmd.Flags().BoolVar(&flagYes, "yes", false, "skip destructive confirmation prompts (DANGEROUS; for VMs)")

	bootstrapCmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Phase B: packages, flatpaks, 1Password, theming, dotfiles (run as your user)",
		RunE:  func(_ *cobra.Command, _ []string) error { return runPhase(stages.Bootstrap) },
	}

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Parse and validate config.yaml without changing anything",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(flagConfig)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			if err := stages.ValidateHooks(cfg); err != nil {
				return err
			}
			ui.OK("config valid: %s", flagConfig)
			return nil
		},
	}

	listStagesCmd := &cobra.Command{
		Use:   "list-stages",
		Short: "List all registered stages with their order, name and phase",
		RunE: func(_ *cobra.Command, _ []string) error {
			for _, s := range stages.All() {
				phase := "Install"
				if s.Phase() == stages.Bootstrap {
					phase = "Bootstrap"
				}
				fmt.Printf("%-10s %-4d %s\n", phase, s.Order(), s.Name())
			}
			return nil
		},
	}

	root.AddCommand(installCmd, bootstrapCmd, validateCmd, listStagesCmd)

	if err := root.Execute(); err != nil {
		ui.Error(err.Error())
		os.Exit(1)
	}
}

func runPhase(p stages.Phase) error {
	cfg, err := config.Load(flagConfig)
	if err != nil {
		return err
	}
	ctx := &stages.Context{
		Cfg:        cfg,
		R:          &run.Runner{DryRun: flagDryRun, Sudo: p == stages.Bootstrap},
		AssumeYes:  flagYes,
		ConfigPath: flagConfig,
	}

	if err := stages.ValidateHooks(cfg); err != nil {
		return err
	}

	selected := stages.Select(p, flagOnly, flagSkip, cfg.Stages.Disable)
	if selected, err = stages.Within(selected, flagFrom, flagTo); err != nil {
		return err
	}
	if len(selected) == 0 {
		return fmt.Errorf("no stages matched (--only %q, --skip %v, stages.disable %v)", flagOnly, flagSkip, cfg.Stages.Disable)
	}

	// TUI mode owns the alt-screen, so it cannot coexist with the huh prompts the
	// archinstall stage runs (ConfirmErase/Password). We therefore use the TUI
	// only when no prompt will fire — i.e. on a TTY, not --plain, not --dry-run
	// (so `--dry-run | less` stays plain), and never for an interactive Phase A
	// install. This keeps all input collection in normal terminal mode.
	willPrompt := p == stages.Install && !flagYes
	if !flagPlain && !flagDryRun && !willPrompt && term.IsTerminal(os.Stdout.Fd()) {
		return runPhaseTUI(ctx, p, selected)
	}
	return runStages(ctx, p, selected, func(s stages.Stage) { ui.Header(s.Order(), s.Name()) })
}

// runStages executes the pre/before/after/post hook lifecycle around each stage.
// onStage is invoked with each stage just before it runs (plain mode prints a
// header banner; TUI mode updates the viewport header). It is the shared core of
// both the plain and TUI paths.
func runStages(ctx *stages.Context, p stages.Phase, selected []stages.Stage, onStage func(stages.Stage)) error {
	if err := stages.FireHooks(ctx, stages.PhasePre(p)); err != nil {
		return err
	}
	for _, s := range selected {
		if err := stages.FireHooks(ctx, "before:"+s.Name()); err != nil {
			return err
		}
		onStage(s)
		if err := s.Run(ctx); err != nil {
			return fmt.Errorf("stage %s: %w", s.Name(), err)
		}
		if err := stages.FireHooks(ctx, "after:"+s.Name()); err != nil {
			return err
		}
	}
	if err := stages.FireHooks(ctx, stages.PhasePost(p)); err != nil {
		return err
	}
	ui.OK("done")
	return nil
}

// runPhaseTUI runs the phase in a goroutine while the bubbletea program drives
// the scrollable viewport on the main thread. The Runner's Out and the ui sink
// both point at the program's writer so every byte (subprocess + styled ui)
// flows through the viewport — nothing writes to os.Stdout while the TUI owns
// the screen.
func runPhaseTUI(ctx *stages.Context, p stages.Phase, selected []stages.Stage) error {
	prog := tui.NewProgram()
	ctx.R.Out = prog.Writer()
	ui.SetSink(prog.Writer())
	defer ui.SetSink(nil)

	var runErr error
	go func() {
		runErr = runStages(ctx, p, selected, func(s stages.Stage) { prog.Stage(s.Order(), s.Name()) })
		prog.Done(runErr)
	}()

	if err := prog.Run(); err != nil {
		return err
	}
	// Surface a stage failure as the command's exit status; the viewport footer
	// has already shown it. runErr is written by the worker goroutine and read
	// only after prog.Run() returns (the program quits on doneMsg-driven quit),
	// so there is no concurrent access.
	return runErr
}
