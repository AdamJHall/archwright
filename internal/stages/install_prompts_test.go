package stages

import (
	"testing"

	"github.com/AdamJHall/archwright/internal/run"
)

// TestCollectInstallPrompts_NoOp covers the paths that must never touch the
// terminal: --yes and an already-collected Context. (The interactive path needs
// a TTY + huh and is exercised by VM validation, not unit tests.)
func TestCollectInstallPrompts_NoOp(t *testing.T) {
	t.Run("assume-yes skips prompting", func(t *testing.T) {
		ctx := &Context{Cfg: testConfig(t), R: &run.Runner{DryRun: true}, AssumeYes: true}
		if err := ctx.CollectInstallPrompts(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ctx.PromptsCollected || ctx.InstallPassword != "" {
			t.Fatalf("assume-yes should not collect anything, got collected=%v pw=%q",
				ctx.PromptsCollected, ctx.InstallPassword)
		}
	})

	t.Run("already collected is a no-op", func(t *testing.T) {
		ctx := &Context{Cfg: testConfig(t), R: &run.Runner{DryRun: true},
			PromptsCollected: true, InstallPassword: "secret"}
		if err := ctx.CollectInstallPrompts(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ctx.InstallPassword != "secret" {
			t.Fatalf("password mutated: %q", ctx.InstallPassword)
		}
	})
}

// TestArchinstall_UsesHoistedPrompts proves the stage does not prompt when the
// password was pre-collected: with AssumeYes=false but PromptsCollected=true, a
// dry-run must succeed without reaching the (TTY-only) huh prompt.
func TestArchinstall_UsesHoistedPrompts(t *testing.T) {
	ss := For(Install, "archinstall")
	if len(ss) != 1 {
		t.Fatalf("want one archinstall stage, got %d", len(ss))
	}
	ctx := &Context{
		Cfg:              testConfig(t),
		R:                &run.Runner{DryRun: true},
		AssumeYes:        false,
		PromptsCollected: true,
		InstallPassword:  "hoisted-pw",
		ConfigPath:       "/tmp/config.yaml",
	}
	if err := ss[0].Run(ctx); err != nil {
		t.Fatalf("archinstall dry-run with hoisted prompts errored: %v", err)
	}
}
