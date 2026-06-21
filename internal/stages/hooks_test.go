package stages

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/run"
	"gopkg.in/yaml.v3"
)

// hooksConfig builds a validated *config.Config from a self-contained YAML
// snippet that carries hooks at several lifecycle points. scriptPath need not
// exist (Hook.Script existence is no longer validated); these tests pass a real
// file anyway so the recorded plan is realistic.
func hooksConfig(t *testing.T, scriptPath string) *config.Config {
	t.Helper()
	y := fmt.Sprintf(`
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
kernel:
  base: [linux]
disks:
  esp:
    device: /dev/nvme0n1
    size: 4GiB
  swap:
    size: 64GiB
  lvm:
    vg: vg0
    lv: root
    filesystem: xfs
    pvs: [/dev/nvme0n1p2]
hooks:
  - name: announce
    at: pre-bootstrap
    run: echo hello
  - name: before-packages
    at: before:packages
    run: echo before-packages
  - name: after-kde
    at: after:kde
    run: echo after-kde
  - name: privileged
    at: pre-bootstrap
    root: true
    run: echo privileged
  - name: with-env
    at: pre-bootstrap
    dir: /tmp/workdir
    env:
      FOO: bar
    run: echo env-and-dir
  - name: scripted
    at: pre-bootstrap
    script: %s
`, scriptPath)
	var c config.Config
	if err := yaml.Unmarshal([]byte(y), &c); err != nil {
		t.Fatalf("unmarshal hooks config: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("hooks config should be valid: %v", err)
	}
	if err := ValidateHooks(&c); err != nil {
		t.Fatalf("hooks config should pass ValidateHooks: %v", err)
	}
	return &c
}

func TestFireHooks_GlobalPoint(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho scripted\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := hooksConfig(t, script)
	r := &run.Runner{DryRun: true, Sudo: true}
	ctx := &Context{Cfg: cfg, R: r, AssumeYes: true}

	if err := FireHooks(ctx, "pre-bootstrap"); err != nil {
		t.Fatalf("FireHooks returned error: %v", err)
	}
	mustContain(t, r.Plan,
		"sh: echo hello",               // unprivileged inline run -> Shell
		"sudo bash -c echo privileged", // root inline run -> Root bash -c (sudo in Bootstrap)
		"sh: echo env-and-dir",         // env/dir hook still runs via Shell
		"bash "+script,                 // script hook -> Cmd bash <path>
	)
	// before:/after: hooks must NOT fire at a global point.
	if joined := strings.Join(r.Plan, "\n"); strings.Contains(joined, "before-packages") || strings.Contains(joined, "after-kde") {
		t.Errorf("global point fired a per-stage hook.\nplan:\n%s", joined)
	}
}

func TestFireHooks_PerStagePoints(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("echo scripted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := hooksConfig(t, script)

	r := &run.Runner{DryRun: true, Sudo: true}
	ctx := &Context{Cfg: cfg, R: r, AssumeYes: true}
	if err := FireHooks(ctx, "before:packages"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, r.Plan, "sh: echo before-packages")
	if len(r.Plan) != 1 {
		t.Errorf("before:packages should fire exactly one hook, plan: %v", r.Plan)
	}

	r2 := &run.Runner{DryRun: true, Sudo: true}
	ctx2 := &Context{Cfg: cfg, R: r2, AssumeYes: true}
	if err := FireHooks(ctx2, "after:kde"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, r2.Plan, "sh: echo after-kde")
	if len(r2.Plan) != 1 {
		t.Errorf("after:kde should fire exactly one hook, plan: %v", r2.Plan)
	}
}

func TestFireHooks_NonMatchingPointFiresNothing(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("echo scripted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := hooksConfig(t, script)
	r := &run.Runner{DryRun: true, Sudo: true}
	ctx := &Context{Cfg: cfg, R: r, AssumeYes: true}
	if err := FireHooks(ctx, "after:nonexistent-point"); err != nil {
		t.Fatal(err)
	}
	if len(r.Plan) != 0 {
		t.Errorf("non-matching point should fire nothing, got plan: %v", r.Plan)
	}
}

// TestFireHooks_EnvDirRestored asserts a hook's Env/Dir apply only to that hook
// and are restored so a following plain command is unaffected.
func TestFireHooks_EnvDirRestored(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("echo scripted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := hooksConfig(t, script)
	r := &run.Runner{DryRun: true, Sudo: true}
	ctx := &Context{Cfg: cfg, R: r, AssumeYes: true}

	if r.Env != nil || r.Dir != "" {
		t.Fatalf("precondition: runner Env/Dir should start empty, got %v / %q", r.Env, r.Dir)
	}
	if err := FireHooks(ctx, "pre-bootstrap"); err != nil {
		t.Fatal(err)
	}
	// After firing, the with-env hook's Env/Dir must have been restored.
	if r.Env != nil {
		t.Errorf("runner Env leaked after hooks: %v", r.Env)
	}
	if r.Dir != "" {
		t.Errorf("runner Dir leaked after hooks: %q", r.Dir)
	}
}

func TestValidateHooks(t *testing.T) {
	mk := func(at string) *config.Config {
		var c config.Config
		c.Hooks = []config.Hook{{Name: "h", At: at, Run: "echo x"}}
		return &c
	}
	if err := ValidateHooks(mk("before:packages")); err != nil {
		t.Errorf("before:packages should be a known stage: %v", err)
	}
	if err := ValidateHooks(mk("after:kde")); err != nil {
		t.Errorf("after:kde should be a known stage: %v", err)
	}
	if err := ValidateHooks(mk("pre-install")); err != nil {
		t.Errorf("global point should pass ValidateHooks: %v", err)
	}
	err := ValidateHooks(mk("before:nope"))
	if err == nil {
		t.Fatal("before:nope should fail ValidateHooks")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the unknown stage, got: %v", err)
	}
}
