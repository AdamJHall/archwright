package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/configsrc"
	"gopkg.in/yaml.v3"
)

func TestConfigRefs(t *testing.T) {
	tests := []struct {
		name string
		flag []string
		want []string
	}{
		{"no flag falls back to default", nil, []string{defaultConfigRef}},
		{"empty slice falls back to default", []string{}, []string{defaultConfigRef}},
		{"single ref used verbatim", []string{"a.yaml"}, []string{"a.yaml"}},
		// The StringArrayVar-default pitfall: a user-supplied value must NOT be
		// appended to "config.yaml". With an empty bound default, repeats stand alone.
		{"repeated refs stand alone", []string{"a.yaml", "b.yaml"}, []string{"a.yaml", "b.yaml"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configRefs(tt.flag)
			if len(got) != len(tt.want) {
				t.Fatalf("configRefs(%v) = %v, want %v", tt.flag, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("configRefs(%v) = %v, want %v", tt.flag, got, tt.want)
				}
			}
		})
	}
}

// baseConfig is a complete, valid config used as the import base in render tests.
const baseConfig = `
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
  shell: /usr/bin/zsh
  groups: [wheel]
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
    pvs: [/dev/nvme0n1p3, /dev/sda]
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
packages: [git]
kernel:
  base: [linux]
  packages: [linux-cachyos]
  default: linux-cachyos
  replace_stock: true
`

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderConfig_FlattensMerge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", baseConfig)
	// Importer pulls in the base, overrides hostname and adds a package.
	importer := writeFile(t, dir, "machine.yaml", `
imports:
  - base.yaml
system:
  hostname: desktop-box
packages: [steam]
`)

	var out bytes.Buffer
	if err := renderConfig([]string{importer}, &out, configsrc.Options{}); err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	// Output must be flattened: no imports: key, merged values present.
	var flat map[string]any
	if err := yaml.Unmarshal(out.Bytes(), &flat); err != nil {
		t.Fatalf("output is not valid YAML: %v", err)
	}
	if _, ok := flat["imports"]; ok {
		t.Errorf("flattened output still contains imports: key:\n%s", out.String())
	}

	sys, _ := flat["system"].(map[string]any)
	if sys == nil || sys["hostname"] != "desktop-box" {
		t.Errorf("importer override not applied; hostname = %v", sys["hostname"])
	}
	// String-slice fields union: base's git + importer's steam.
	body := out.String()
	if !strings.Contains(body, "git") || !strings.Contains(body, "steam") {
		t.Errorf("packages not unioned (want git+steam):\n%s", body)
	}
}

func TestRenderConfig_InvalidErrorsBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	// A config missing required fields (no user, disks, etc.) must fail validation.
	bad := writeFile(t, dir, "bad.yaml", "system:\n  hostname: bad host\n")

	var out bytes.Buffer
	err := renderConfig([]string{bad}, &out, configsrc.Options{})
	if err == nil {
		t.Fatal("expected invalid merged config to error")
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be written for an invalid config, got:\n%s", out.String())
	}
}
