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
	"time"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/run"
	"github.com/AdamJHall/archwright/internal/stages"
	"github.com/AdamJHall/archwright/internal/ui"
	"github.com/spf13/cobra"
)

// version is overwritten at build time via -ldflags "-X main.version=..."
// (goreleaser sets this from the git tag).
var version = "dev"

// Persistent flag values, bound on the root command.
var (
	flagDryRun  bool
	flagOnly    string
	flagSkip    []string
	flagFrom    string
	flagTo      string
	flagConfig  string
	flagYes     bool
	flagNoColor bool
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
	pf.BoolVar(&flagNoColor, "no-color", false, "disable coloured output (NO_COLOR is also honoured)")

	// Apply colour preference once flags are parsed, before any output.
	root.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		if flagNoColor || os.Getenv("NO_COLOR") != "" {
			ui.DisableColor()
		}
	}

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

	return runStages(ctx, p, selected)
}

// phaseName is the lowercase label used in run bookends ("install"/"bootstrap").
func phaseName(p stages.Phase) string {
	if p == stages.Bootstrap {
		return "bootstrap"
	}
	return "install"
}

// runStages executes the pre/before/after/post hook lifecycle around each stage,
// streaming each stage's output straight through the terminal. It frames the run
// with a banner + summary and prints an [i/n] progress header and elapsed time
// per stage.
func runStages(ctx *stages.Context, p stages.Phase, selected []stages.Stage) error {
	total := len(selected)
	start := time.Now()
	ui.RunBanner(version, phaseName(p), total, ctx.R.DryRun)

	if err := stages.FireHooks(ctx, stages.PhasePre(p)); err != nil {
		return err
	}
	for i, s := range selected {
		if err := stages.FireHooks(ctx, "before:"+s.Name()); err != nil {
			return err
		}
		ui.StageStart(i+1, total, s.Order(), s.Name())
		stageStart := time.Now()
		if err := s.Run(ctx); err != nil {
			ui.RunFailed(i+1, total, s.Name(), time.Since(start))
			return fmt.Errorf("stage %s: %w", s.Name(), err)
		}
		ui.StageTime(time.Since(stageStart))
		if err := stages.FireHooks(ctx, "after:"+s.Name()); err != nil {
			return err
		}
	}
	if err := stages.FireHooks(ctx, stages.PhasePost(p)); err != nil {
		return err
	}
	ui.RunComplete(phaseName(p), total, time.Since(start))
	return nil
}
