package stages

import (
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/run"
	"gopkg.in/yaml.v3"
)

// These tests cover the Phase-A postInstall fixes:
//   - encrypted layouts skip the chroot/remount/staging steps (Issue #2),
//   - the post-install remounts are checked, not best-effort (Issue #2),
//   - installKernels runs when only kernel.default is set (Issue #3).
// They use self-contained YAML so they don't touch the shared fixtures.

// runArchinstall runs the archinstall stage in dry-run against a caller-supplied
// config and returns the recorded plan. Unlike planForCfg it asserts the stage
// succeeds and is reused across the cases below.
func runArchinstall(t *testing.T, yamlBody string) []string {
	t.Helper()
	var c config.Config
	if err := yaml.Unmarshal([]byte(yamlBody), &c); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	ss := For(Install, "archinstall")
	if len(ss) != 1 {
		t.Fatalf("expected exactly one archinstall stage, got %d", len(ss))
	}
	r := &run.Runner{DryRun: true}
	ctx := &Context{Cfg: &c, R: r, AssumeYes: true, ConfigPath: "/tmp/config.yaml"}
	if err := ss[0].Run(ctx); err != nil {
		t.Fatalf("archinstall stage errored in dry-run: %v", err)
	}
	return r.Plan
}

const encLVMYAML = `
system: {hostname: arch-luks, timezone: Europe/London, locale: en_GB.UTF-8, keymap: uk}
user: {name: adam}
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel: {base: [linux], packages: [linux-cachyos], default: linux-cachyos}
repos:
  - name: cachyos
    server: https://example.invalid/$repo
disks:
  layout: lvm
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: swapfile, size: 4GiB}
  lvm: {vg: vg0, lv: root, filesystem: ext4, pvs: [/dev/nvme0n1p2]}
  encryption: {type: lvm_on_luks}
`

// Encrypted installs must NOT mount the (still-encrypted) device or run any
// chroot/remount/staging steps: the plan ends at archinstall, and none of the
// post-install side effects appear (Issue #2). archinstall itself still runs.
func TestPostInstall_EncryptedSkipsChrootAndStaging(t *testing.T) {
	plan := runArchinstall(t, encLVMYAML)
	mustContain(t, plan, "archinstall --config")
	// No remount of the plaintext device, no swapfile, no repo setup, no kernel
	// install, no Phase B staging — all skipped for the encrypted layout.
	mustNotContain(t, plan,
		"mount /dev/vg0/root /mnt",
		"mount /dev/nvme0n1p1 /mnt/boot",
		"of=/mnt/swapfile",
		"arch-chroot /mnt pacman -Sy",
		"linux-cachyos",
		"/mnt/home/adam",
	)
}

const kernelDefaultOnlyYAML = `
system: {hostname: arch-kd, timezone: Europe/London, locale: en_GB.UTF-8, keymap: uk}
user: {name: adam}
pacstrap: [base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode]
kernel: {base: [linux, linux-lts], default: linux-lts}
disks:
  layout: plain
  esp: {device: /dev/nvme0n1, size: 1GiB}
  swap: {type: none}
  plain: {device: /dev/nvme0n1, filesystem: ext4}
`

// kernel.default pointing at a base kernel with no extra packages must still pin
// the default in the bootloader (Issue #3): installKernels runs, the pacman -S
// install is skipped (no packages), and GRUB's default is written + regenerated.
func TestPostInstall_KernelDefaultOnlyPinsBootloader(t *testing.T) {
	plan := runArchinstall(t, kernelDefaultOnlyYAML)
	mustContain(t, plan,
		`GRUB_TOP_LEVEL="/boot/vmlinuz-linux-lts"`,
		"arch-chroot /mnt grub-mkconfig -o /boot/grub/grub.cfg",
	)
	// No packages to install -> no `pacman -S` kernel install.
	joined := strings.Join(plan, "\n")
	if strings.Contains(joined, "pacman -S --needed --noconfirm linux-lts") {
		t.Errorf("did not expect a kernel package install for a base-only default.\nplan:\n%s", joined)
	}
}

// installKernels with no packages but a default set still pins the bootloader and
// skips the empty `pacman -S` (Issue #3 guard), tested at the function level so it
// is independent of the full archinstall stage plan above.
func TestInstallKernels_NoPackagesSkipsPacman(t *testing.T) {
	r := &run.Runner{DryRun: true}
	ctx := &Context{
		Cfg: &config.Config{Kernel: config.KernelConfig{Base: []string{"linux"}, Default: "linux"}},
		R:   r,
	}
	if err := installKernels(ctx, ctx.Cfg.Kernel); err != nil {
		t.Fatalf("installKernels: %v", err)
	}
	joined := strings.Join(r.Plan, "\n")
	if strings.Contains(joined, "pacman -S") {
		t.Errorf("expected no pacman -S with empty packages.\nplan:\n%s", joined)
	}
	if !strings.Contains(joined, `GRUB_TOP_LEVEL="/boot/vmlinuz-linux"`) {
		t.Errorf("expected GRUB default to be pinned.\nplan:\n%s", joined)
	}
}
