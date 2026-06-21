package stages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/run"
	"gopkg.in/yaml.v3"
)

// TestFireHooks_ExpandsTildeInScriptPlan asserts a hook whose script starts with
// `~` has that `~` expanded to $HOME in the recorded dry-run plan.
func TestFireHooks_ExpandsTildeInScriptPlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var c config.Config
	c.Hooks = []config.Hook{{
		Name:   "tilde",
		At:     "pre-bootstrap",
		Script: "~/foo.sh",
		Dir:    "~/work",
	}}
	r := &run.Runner{DryRun: true}
	ctx := &Context{Cfg: &c, R: r}

	if err := FireHooks(ctx, "pre-bootstrap"); err != nil {
		t.Fatalf("FireHooks: %v", err)
	}
	joined := strings.Join(r.Plan, "\n")
	wantScript := "bash " + filepath.Join(home, "foo.sh")
	if !strings.Contains(joined, wantScript) {
		t.Errorf("plan missing expanded script %q\nplan:\n%s", wantScript, joined)
	}
	if strings.Contains(joined, "~") {
		t.Errorf("plan still contains a literal ~:\n%s", joined)
	}
}

// TestFireHooks_ExpandsTildeInDir runs a hook for real (not dry-run) whose dir
// starts with `~`; the script prints its working directory, proving the `~` was
// expanded to $HOME for the runner's Dir before the command executed.
func TestFireHooks_ExpandsTildeInDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(home, "cwd.txt")
	script := filepath.Join(home, "foo.sh")
	// Record the working directory so the test can assert Dir was expanded.
	if err := os.WriteFile(script, []byte("#!/bin/sh\npwd > '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var c config.Config
	c.Hooks = []config.Hook{{
		Name:   "tilde-dir",
		At:     "pre-bootstrap",
		Script: "~/foo.sh",
		Dir:    "~/work",
	}}
	r := &run.Runner{} // real execution; the script is harmless (writes one file)
	ctx := &Context{Cfg: &c, R: r}

	if err := FireHooks(ctx, "pre-bootstrap"); err != nil {
		t.Fatalf("FireHooks: %v", err)
	}
	// Runner Dir must be restored afterwards.
	if r.Dir != "" {
		t.Errorf("runner Dir leaked after hooks: %q", r.Dir)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading recorded cwd: %v", err)
	}
	// pwd may resolve symlinks (e.g. /tmp -> /private/tmp); compare resolved paths.
	gotDir, _ := filepath.EvalSymlinks(strings.TrimSpace(string(got)))
	wantDir, _ := filepath.EvalSymlinks(work)
	if gotDir != wantDir {
		t.Errorf("hook ran in %q, want expanded dir %q", gotDir, wantDir)
	}
}

// TestValidate_HookScriptNeedNotExist asserts that a hook whose script path does
// not exist on disk now passes validation: a hook script may be produced by an
// earlier hook or stage in the same run, so validate-time existence is wrong.
func TestValidate_HookScriptNeedNotExist(t *testing.T) {
	const y = `
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
  - name: made-by-earlier-stage
    at: post-bootstrap
    script: /this/does/not/exist/yet.sh
`
	var c config.Config
	if err := yaml.Unmarshal([]byte(y), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("hook with a non-existent script path should validate, got: %v", err)
	}
	// Sanity: the path really does not exist.
	if _, err := os.Stat(c.Hooks[0].Script); !os.IsNotExist(err) {
		t.Skipf("test path unexpectedly exists; stat err=%v", err)
	}
}
