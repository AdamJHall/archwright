package config

import (
	"strings"
	"testing"
)

// baseYAML is a minimal-but-valid config used to exercise the kernel.base rules
// in isolation. It sets kernel.base so the baseline is valid; individual cases
// rewrite the kernel block to drive a specific failure or success.
const baseYAML = `
system:
  hostname: arch-box
  timezone: Europe/London
  locale: en_GB.UTF-8
  keymap: uk
user:
  name: adam
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
    pvs: [/dev/nvme0n1p3]
kernel:
  base: [linux]
`

func TestValidate_KernelBase(t *testing.T) {
	cases := []struct {
		name    string
		kernel  string // replaces the "kernel:\n  base: [linux]\n" block
		wantErr string // empty = expect valid
	}{
		{
			name:    "base linux is valid",
			kernel:  "kernel:\n  base: [linux]\n",
			wantErr: "",
		},
		{
			name:    "missing base is rejected",
			kernel:  "kernel:\n  packages: [linux-cachyos]\n  default: linux-cachyos\n",
			wantErr: "kernel.base",
		},
		{
			name:    "empty base list is rejected",
			kernel:  "kernel:\n  base: []\n",
			wantErr: "kernel.base must have at least 1 item(s)",
		},
		{
			name:    "default in base passes",
			kernel:  "kernel:\n  base: [linux, linux-lts]\n  default: linux-lts\n",
			wantErr: "",
		},
		{
			name:    "default in packages passes",
			kernel:  "kernel:\n  base: [linux]\n  packages: [linux-cachyos, linux-cachyos-headers]\n  default: linux-cachyos\n",
			wantErr: "",
		},
		{
			name:    "default in neither base nor packages fails",
			kernel:  "kernel:\n  base: [linux]\n  packages: [linux-cachyos]\n  default: linux-zen\n",
			wantErr: `kernel.default "linux-zen" must be one of kernel.base or kernel.packages`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y := strings.Replace(baseYAML, "kernel:\n  base: [linux]\n", tc.kernel, 1)
			err := load(t, y).Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("missing %q in error:\n%s", tc.wantErr, err)
			}
		})
	}
}
