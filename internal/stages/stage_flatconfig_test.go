package stages

import (
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/run"
)

// stageBinary stages the binary + config into the target home for Phase B. With
// ctx.FlatConfig set it must cp a flattened temp file (the resolved+merged
// config); with FlatConfig nil it must fall back to cp'ing ctx.ConfigPath.

func TestStageBinary_FlatConfigCopiesFlattened(t *testing.T) {
	r := &run.Runner{DryRun: true} // Phase A: no sudo
	ctx := &Context{
		Cfg:        testConfig(t),
		R:          r,
		ConfigPath: "/tmp/config.yaml",
		FlatConfig: []byte("system:\n  hostname: flattened-box\n"),
	}
	if err := stageBinary(ctx); err != nil {
		t.Fatalf("stageBinary: %v", err)
	}
	joined := strings.Join(r.Plan, "\n")

	// A cp of a flattened temp file into the target config.yaml must be recorded.
	if !strings.Contains(joined, "/mnt/home/adam/config.yaml") {
		t.Errorf("plan should cp into target config.yaml:\n%s", joined)
	}
	if !strings.Contains(joined, "archwright-flat-") {
		t.Errorf("plan should cp the flattened temp file (archwright-flat-*), not ConfigPath:\n%s", joined)
	}
	// The raw ConfigPath must NOT be the cp source when a flattened config exists.
	for _, line := range r.Plan {
		if strings.HasPrefix(line, "cp /tmp/config.yaml ") {
			t.Errorf("flattened path should not cp ConfigPath, got line: %q", line)
		}
	}
}

func TestStageBinary_NilFlatConfigCopiesConfigPath(t *testing.T) {
	r := &run.Runner{DryRun: true}
	ctx := &Context{
		Cfg:        testConfig(t),
		R:          r,
		ConfigPath: "/tmp/config.yaml",
		// FlatConfig nil: back-compat path.
	}
	if err := stageBinary(ctx); err != nil {
		t.Fatalf("stageBinary: %v", err)
	}
	joined := strings.Join(r.Plan, "\n")
	if !strings.Contains(joined, "cp /tmp/config.yaml /mnt/home/adam/config.yaml") {
		t.Errorf("nil FlatConfig should cp ConfigPath verbatim:\n%s", joined)
	}
	if strings.Contains(joined, "archwright-flat-") {
		t.Errorf("nil FlatConfig should not produce a flattened temp file:\n%s", joined)
	}
}
