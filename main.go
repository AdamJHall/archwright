// Command archwright is the single-binary, declarative Arch Linux installer.
//
//	archwright install     [--dry-run] [--only <stage>] [--skip <stage>] [--config <ref>...] [--yes]
//	archwright bootstrap   [--dry-run] [--only <stage>] [--skip <stage>] [--config <ref>...]
//	archwright validate    [--config <ref>...]
//	archwright render      [--config <ref>...] [-o out.yaml]
//	archwright list-stages
//
// Phase A (install) runs from the Arch live ISO as root; Phase B (bootstrap)
// runs on the booted system as your user. Stages live in internal/stages.
// Stage selection: --only (single stage) wins; otherwise --skip and the
// stages.disable config list subtract from the full set. User-defined hooks fire
// at lifecycle points (pre/post-{install,bootstrap}, before:/after:<stage>).
//
// --config is repeatable: each ref is a local path, github shorthand or raw URL,
// resolved + deep-merged (later wins) via internal/configsrc, which also resolves
// in-file imports:. render resolves + merges + validates and writes the flattened
// YAML without running any stages or touching disks.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/AdamJHall/archwright/internal/configsrc"
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
	flagConfig  []string
	flagOffline bool
	flagStrict  bool
	flagYes     bool
	flagNoColor bool
	flagOutput  string // local to render
)

// defaultConfigRef is the ref used when --config is not supplied.
const defaultConfigRef = "config.yaml"

// configRefs returns the config refs to load. cobra's StringArrayVar appends
// user values to any non-empty default, so we bind --config with an empty
// default and apply defaultConfigRef here when the user passed none. This keeps
// "no flag" -> [config.yaml] while "--config a --config b" -> [a b] (no leaked
// default). Extracted so the default/repeat logic is unit-testable.
func configRefs(flag []string) []string {
	if len(flag) == 0 {
		return []string{defaultConfigRef}
	}
	return flag
}

// loadOpts builds configsrc.Options from the persistent flags + GITHUB_TOKEN env
// (the token is deliberately env-only, never a flag). CacheDir/HTTPClient are
// left zero so configsrc applies its own defaults.
func loadOpts() configsrc.Options {
	return configsrc.Options{
		Offline: flagOffline,
		Strict:  flagStrict,
		Token:   os.Getenv("GITHUB_TOKEN"),
	}
}

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
	// Bound with an empty default on purpose: see configRefs. A non-empty
	// StringArrayVar default would be *appended to* by user values.
	pf.StringArrayVar(&flagConfig, "config", nil, "config ref: local path, github shorthand or URL (repeatable; default config.yaml)")
	pf.BoolVar(&flagOffline, "offline", false, "resolve config from cache only; never hit the network")
	pf.BoolVar(&flagStrict, "strict", false, "refuse unpinned github config refs (require @ref)")
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
			refs := configRefs(flagConfig)
			cfg, _, _, err := configsrc.Load(refs, loadOpts())
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			if err := stages.ValidateHooks(cfg); err != nil {
				return err
			}
			ui.OK("config valid: %v", refs)
			return nil
		},
	}

	renderCmd := &cobra.Command{
		Use:   "render",
		Short: "Resolve + merge --config refs, validate, and write the flattened YAML (no stages, no disks)",
		RunE: func(_ *cobra.Command, _ []string) error {
			out := os.Stdout
			if flagOutput != "" && flagOutput != "-" {
				f, err := os.Create(flagOutput)
				if err != nil {
					return err
				}
				defer f.Close()
				return renderConfig(configRefs(flagConfig), f, loadOpts())
			}
			return renderConfig(configRefs(flagConfig), out, loadOpts())
		},
	}
	renderCmd.Flags().StringVarP(&flagOutput, "output", "o", "", "write flattened config here (default stdout; - is stdout)")

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

	root.AddCommand(installCmd, bootstrapCmd, validateCmd, renderCmd, listStagesCmd)

	if err := root.Execute(); err != nil {
		ui.Error(err.Error())
		os.Exit(1)
	}
}

// renderConfig resolves + merges refs, validates the result, then writes the
// flattened YAML to out. It runs no stages and touches no disks. Validation
// happens before any bytes are written so an invalid merged config errors out
// rather than emitting a bad file. Factored out so it is testable without cobra.
func renderConfig(refs []string, out io.Writer, opts configsrc.Options) error {
	cfg, flat, srcs, err := configsrc.Load(refs, opts)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := stages.ValidateHooks(cfg); err != nil {
		return err
	}
	if header := configsrc.ProvenanceComment(srcs); header != "" {
		if _, err := io.WriteString(out, header); err != nil {
			return err
		}
	}
	_, err = out.Write(flat)
	return err
}

func runPhase(p stages.Phase) error {
	refs := configRefs(flagConfig)
	cfg, flat, _, err := configsrc.Load(refs, loadOpts())
	if err != nil {
		return err
	}
	ctx := &stages.Context{
		Cfg:        cfg,
		R:          &run.Runner{DryRun: flagDryRun, Sudo: p == stages.Bootstrap},
		AssumeYes:  flagYes,
		ConfigPath: refs[0],
		FlatConfig: flat,
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
