package stages

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/AdamJHall/archwright/internal/archinstall"
	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/ui"
)

// archinstallStage is Phase A: render config.yaml into an archinstall config +
// credentials file and let archinstall do the partitioning, LVM, pacstrap and
// bootloader install. Replaces the old hand-rolled partition/lvm/mount/pacstrap/
// system/initramfs/grub stages. After archinstall finishes it stages the binary
// + config into the new user's home so Phase B is available post-reboot.
type archinstallStage struct{}

func init() { register(archinstallStage{}) }

func (archinstallStage) Order() int   { return 10 }
func (archinstallStage) Name() string { return "archinstall" }
func (archinstallStage) Phase() Phase { return Install }

const (
	aiConfigPath = "/tmp/archinstall-config.json"
	aiCredsPath  = "/tmp/archinstall-creds.json"
)

func (archinstallStage) Run(ctx *Context) error {
	wiped := wipedDevices(ctx.Cfg)

	// Destructive confirmation (skipped by --yes).
	if !ctx.AssumeYes {
		ui.Warn("archinstall will ERASE these devices", "devices", strings.Join(wiped, " "))
		if err := ui.ConfirmErase("ERASE", "This wipes the disks above and installs Arch."); err != nil {
			return err
		}
	}

	// Credentials: prompt for a real install, throwaway for --yes (VMs only).
	password := "installme"
	if !ctx.AssumeYes {
		pw, err := ui.Password(fmt.Sprintf("Set a password for user %q and root", ctx.Cfg.User.Name))
		if err != nil {
			return err
		}
		password = pw
	}

	geom, err := probeGeometry(wiped, ctx.R.DryRun)
	if err != nil {
		return err
	}

	cfg, creds, err := archinstall.Build(ctx.Cfg, geom, password)
	if err != nil {
		return fmt.Errorf("rendering archinstall config: %w", err)
	}

	// In dry-run, show the rendered config (secrets withheld) and stop short of
	// writing files / invoking archinstall.
	if ctx.R.DryRun {
		data, _ := json.MarshalIndent(cfg, "", "  ")
		ui.Info("would write archinstall config", "path", aiConfigPath)
		fmt.Fprintln(os.Stderr, string(data))
		ui.Info("would write credentials", "path", aiCredsPath, "users", len(creds.Users))
	} else {
		if err := writeJSON(aiConfigPath, cfg, 0o600); err != nil {
			return err
		}
		if err := writeJSON(aiCredsPath, creds, 0o600); err != nil {
			return err
		}
	}

	// Pick fast mirrors before archinstall pacstraps (archinstall uses the live
	// ISO's mirrorlist and copies it into the target).
	if err := runReflector(ctx); err != nil {
		return err
	}

	if err := ctx.R.Root("archinstall",
		"--config", aiConfigPath, "--creds", aiCredsPath, "--silent"); err != nil {
		return err
	}

	if err := postInstall(ctx); err != nil {
		return err
	}

	ui.OK("archinstall complete; Phase B staged in /home/%s", ctx.Cfg.User.Name)
	return nil
}

// postInstall runs after archinstall, inside the target: configure custom repos
// and install custom kernels (so first boot already has them), then stage the
// binary + config for Phase B. archinstall unmounts the target on finish, so we
// remount the root LV and the ESP (kernels/GRUB live on /boot) for the chroot
// work, then unmount.
func postInstall(ctx *Context) error {
	// Encrypted layouts (Issue #2): rootDevice() returns the *plaintext* device
	// (the bare partition or /dev/VG/LV), but after archinstall provisions LUKS the
	// real root filesystem lives behind a /dev/mapper/* device that requires a LUKS
	// remount (cryptsetup open with the passphrase) we have NOT implemented yet —
	// encryption is VM-validation-pending. Mounting the still-encrypted device and
	// running chroot work against it would either fail or, worse, silently no-op.
	//
	// Conservative behaviour until the LUKS remount is implemented and VM-validated:
	// skip the remount and every chroot-dependent post-install step (swapfile,
	// locales, repos, kernels). We also cannot stage the Phase B binary/config,
	// because staging copies into /mnt/home/<user> which requires the (correctly
	// mapped+mounted) root — so we warn that Phase B staging must be done manually.
	// This keeps the encrypted path coherent and loud rather than silently wrong.
	if ctx.Cfg.Disks.Encryption != nil {
		ui.Warn("encrypted install: custom repos, kernels, locale extras, and swapfile are NOT applied yet (LUKS remount not implemented)")
		ui.Warn("encrypted install: Phase B binary/config staging is SKIPPED — after first boot, copy the archwright binary + config.yaml into your home and run bootstrap manually")
		return nil
	}

	rootDev, err := rootDevice(ctx.Cfg)
	if err != nil {
		return err
	}
	esp := archinstall.PartDev(ctx.Cfg.Disks.ESP.Device, 1)

	// Remount the target for the chroot work. archinstall unmounts on finish, so we
	// rebuild the mount tree exactly as the installed system sees it.
	//
	// For btrfs the system lives INSIDE the root subvolume (e.g. @), not the
	// top-level subvolume: archinstall installs into @ and mounts it at /. Mounting
	// the bare partition would expose only the (empty) top-level subvol — /boot and
	// /home/<user> would be missing and staging would fail — so we must mount the
	// root subvolume with subvol=<name>, and mount any non-root subvolumes
	// (e.g. @home at /home) so staged files land in the right subvolume.
	mounts, err := targetMounts(ctx.Cfg, rootDev, esp)
	if err != nil {
		return err
	}
	// Checked mounts (Issue #2): a failed remount must abort, not silently run the
	// chroot steps against an empty /mnt. (The umounts below stay best-effort.)
	for _, m := range mounts {
		var args []string
		if len(m.opts) > 0 {
			args = append(args, "-o", strings.Join(m.opts, ","))
		}
		args = append(args, m.dev, m.target)
		if err := ctx.R.Root("mount", args...); err != nil {
			return err
		}
	}

	if err := setupSwapfile(ctx); err != nil {
		return err
	}

	if len(ctx.Cfg.System.Locales) > 0 {
		if err := configureLocales(ctx, ctx.Cfg.System.Locales); err != nil {
			return err
		}
	}

	if ctx.Cfg.User.Shell != "" {
		if err := configureUserShell(ctx, ctx.Cfg.User.Name, ctx.Cfg.User.Shell); err != nil {
			return err
		}
	}

	if ctx.Cfg.Pacman.Multilib {
		if err := enableMultilib(ctx); err != nil {
			return err
		}
	}

	if len(ctx.Cfg.Repos) > 0 {
		if err := configureRepos(ctx, ctx.Cfg.Repos); err != nil {
			return err
		}
	}
	// Run installKernels when there are extra kernel packages to install OR a
	// default kernel to pin (Issue #3): kernel.default may name a base kernel with
	// no extra packages, and that default must still be written to the bootloader.
	if len(ctx.Cfg.Kernel.Packages) > 0 || ctx.Cfg.Kernel.Default != "" {
		if err := installKernels(ctx, ctx.Cfg.Kernel); err != nil {
			return err
		}
	}
	if err := stageBinary(ctx); err != nil {
		return err
	}

	// Unmount in reverse so nested subvolume/ESP mounts come off before /mnt.
	for i := len(mounts) - 1; i >= 0; i-- {
		ctx.R.Try("umount", mounts[i].target)
	}
	return nil
}

// mount is one entry in the target mount tree rebuilt for post-install chroot
// work: a device mounted at an absolute /mnt path with optional mount options.
type mount struct {
	dev    string
	target string
	opts   []string
}

// targetMounts builds the ordered mount tree for the installed system, rebuilt
// so post-install staging writes into the same filesystem each path resolves to
// at boot. The root device is at /mnt and the ESP at /mnt/boot; crucially, any
// layout with a SEPARATE /home (or other non-root mount) must also remount it,
// otherwise staging into /mnt/home/<user> lands on the root fs and is shadowed
// once the real /home mounts over it at boot — which strands the Phase B binary
// and config (and, in the e2e harness, the autorun trigger).
//
//   - btrfs: the system lives inside the root subvolume, so root is mounted with
//     subvol=<root> and every non-root subvolume at its mountpoint under /mnt.
//   - lvm multi-volume: each non-root volume (e.g. a /home LV) is mounted at its
//     mountpoint under /mnt via /dev/<vg>/<name>.
//
// Non-root mounts are ordered shallowest-first so a parent is mounted before any
// nested child (e.g. /home before /home/foo).
func targetMounts(cfg *config.Config, rootDev, esp string) ([]mount, error) {
	switch cfg.Disks.EffectiveLayout() {
	case "btrfs":
		if cfg.Disks.Btrfs == nil {
			break
		}
		var rootSub string
		var others []config.Subvol
		for _, sv := range cfg.Disks.Btrfs.Subvolumes {
			if sv.Mountpoint == "/" {
				rootSub = sv.Name
			} else {
				others = append(others, sv)
			}
		}
		if rootSub == "" {
			return nil, fmt.Errorf("btrfs layout has no subvolume mounted at /")
		}
		mounts := []mount{
			{dev: rootDev, target: "/mnt", opts: []string{"subvol=" + rootSub}},
			{dev: esp, target: "/mnt/boot"},
		}
		sortByDepth(others, func(s config.Subvol) string { return s.Mountpoint })
		for _, sv := range others {
			mounts = append(mounts, mount{
				dev:    rootDev,
				target: "/mnt" + sv.Mountpoint,
				opts:   []string{"subvol=" + sv.Name},
			})
		}
		return mounts, nil

	case "lvm":
		mounts := []mount{
			{dev: rootDev, target: "/mnt"},
			{dev: esp, target: "/mnt/boot"},
		}
		if cfg.Disks.LVM != nil {
			var others []config.LVMVolume
			for _, v := range cfg.Disks.LVM.Volumes {
				if v.Mountpoint != "/" {
					others = append(others, v)
				}
			}
			sortByDepth(others, func(v config.LVMVolume) string { return v.Mountpoint })
			for _, v := range others {
				mounts = append(mounts, mount{
					dev:    fmt.Sprintf("/dev/%s/%s", cfg.Disks.LVM.VG, v.Name),
					target: "/mnt" + v.Mountpoint,
				})
			}
		}
		return mounts, nil
	}

	return []mount{
		{dev: rootDev, target: "/mnt"},
		{dev: esp, target: "/mnt/boot"},
	}, nil
}

// sortByDepth orders entries shallowest-mountpoint first (by path separator
// count) so a parent mount is always applied before a nested child.
func sortByDepth[T any](s []T, mp func(T) string) {
	sort.SliceStable(s, func(i, j int) bool {
		return strings.Count(mp(s[i]), "/") < strings.Count(mp(s[j]), "/")
	})
}

// setupSwapfile creates /swapfile on the freshly installed root and enables it
// via fstab. archinstall 4.x can't format a raw swap partition in an LVM layout
// (its LVM path formats only the boot partition), so swap lives as a file sized
// from cfg.Disks.Swap.Size. Written with dd (real zeros) rather than fallocate
// so it works on xfs too, where a preallocated file has unwritten extents that
// swapon rejects. The target root is mounted at /mnt, so the in-system path is
// /swapfile. No-op if no swap size is configured.
func setupSwapfile(ctx *Context) error {
	// Only the swapfile swap type creates /swapfile here; zram is handled by
	// archinstall, a swap partition is created by the layout builder, and none
	// skips swap entirely.
	if ctx.Cfg.Disks.Swap.EffectiveType() != "swapfile" {
		return nil
	}
	size := ctx.Cfg.Disks.Swap.Size
	if size == "" {
		return nil
	}
	n, err := archinstall.ParseSize(size)
	if err != nil {
		return fmt.Errorf("swap size: %w", err)
	}
	mib := n >> 20
	if mib == 0 {
		return fmt.Errorf("swap size %q is smaller than 1 MiB", size)
	}
	if err := ctx.R.Root("dd", "if=/dev/zero", "of=/mnt/swapfile",
		"bs=1M", fmt.Sprintf("count=%d", mib), "status=none"); err != nil {
		return err
	}
	if err := ctx.R.Root("chmod", "600", "/mnt/swapfile"); err != nil {
		return err
	}
	if err := ctx.R.Root("mkswap", "/mnt/swapfile"); err != nil {
		return err
	}
	return ctx.R.Shell("echo '/swapfile none swap defaults 0 0' >> /mnt/etc/fstab")
}

// runReflector refreshes the live ISO's mirrorlist with reflector per the mirrors
// config, so pacstrap (and, since archinstall copies it, the installed system) use
// fast, recent mirrors. No-op unless mirrors.reflector is set.
func runReflector(ctx *Context) error {
	m := ctx.Cfg.Mirrors
	if !m.Enabled {
		return nil
	}
	var args []string
	if len(m.Countries) > 0 {
		args = append(args, "--country", strings.Join(m.Countries, ","))
	}
	if m.Latest > 0 {
		args = append(args, "--latest", strconv.Itoa(m.Latest))
	}
	if m.Fastest > 0 {
		args = append(args, "--fastest", strconv.Itoa(m.Fastest))
	}
	if m.Sort != "" {
		args = append(args, "--sort", m.Sort)
	}
	if len(m.Protocols) > 0 {
		args = append(args, "--protocol", strings.Join(m.Protocols, ","))
	}
	args = append(args, "--save", "/etc/pacman.d/mirrorlist")
	ui.Info("selecting mirrors with reflector")
	return ctx.R.Root("reflector", args...)
}

// rootDevice returns the path the freshly-installed root filesystem lives on, so
// postInstall can remount it for chroot work after archinstall unmounts the
// target. It depends on the layout: the LVM root LV, or the root partition on
// disk 1 for plain/btrfs (partition 1 is the ESP; with a swap partition the root
// is partition 3, otherwise partition 2).
func rootDevice(cfg *config.Config) (string, error) {
	switch cfg.Disks.EffectiveLayout() {
	case "lvm":
		if cfg.Disks.LVM == nil {
			return "", fmt.Errorf("disks.lvm is required for the lvm layout")
		}
		// Single-LV mode names the root LV directly; multi-volume mode leaves LV
		// empty, so the root LV is the volume mounted at "/" (validation guarantees
		// exactly one exists).
		lv := cfg.Disks.LVM.LV
		if lv == "" {
			for _, v := range cfg.Disks.LVM.Volumes {
				if v.Mountpoint == "/" {
					lv = v.Name
					break
				}
			}
		}
		if lv == "" {
			return "", fmt.Errorf("no root LVM volume (mounted at /) found")
		}
		return fmt.Sprintf("/dev/%s/%s", cfg.Disks.LVM.VG, lv), nil
	case "plain", "btrfs":
		rootIdx := 2
		if cfg.Disks.Swap.EffectiveType() == "partition" {
			rootIdx = 3
		}
		return archinstall.PartDev(cfg.Disks.ESP.Device, rootIdx), nil
	default:
		return "", fmt.Errorf("unknown disks.layout %q", cfg.Disks.Layout)
	}
}

// wipedDevices is the set of whole devices archinstall will wipe: disk 1 plus, for
// the LVM layout, every extra whole-disk PV (PV paths that aren't a partition of
// disk 1). Plain/btrfs use only disk 1.
func wipedDevices(cfg *config.Config) []string {
	disk1 := cfg.Disks.ESP.Device
	devs := []string{disk1}
	if cfg.Disks.LVM != nil {
		for _, pv := range cfg.Disks.LVM.PVs {
			if !strings.HasPrefix(pv, disk1) {
				devs = append(devs, pv)
			}
		}
	}
	return devs
}

// probeGeometry reads each device's total size in bytes via `blockdev`. This is
// a read-only query, so it runs directly (not through the Runner). In dry-run on
// a machine without these devices it falls back to representative sizes so the
// rendered plan is still meaningful.
func probeGeometry(devs []string, dryRun bool) (archinstall.Geometry, error) {
	geom := archinstall.Geometry{}
	for _, dev := range devs {
		out, err := exec.Command("blockdev", "--getsize64", dev).Output()
		if err != nil {
			if dryRun {
				// Absent device under dry-run: fall back to a representative size so
				// the rendered plan is still meaningful, but warn loudly (Issue #11)
				// — silence here masks a typo'd device path, making the placeholder
				// look like a successful probe.
				ui.Warn("device not present; using 512 GiB placeholder size (dry-run)", "device", dev)
				geom[dev] = 512 * (1 << 30) // 512 GiB placeholder
				continue
			}
			return nil, fmt.Errorf("probing size of %s (is it present?): %w", dev, err)
		}
		n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing size of %s: %w", dev, err)
		}
		geom[dev] = n
	}
	return geom, nil
}

// stageBinary copies the running binary + config into the freshly installed
// user's home so Phase B is available after reboot. Assumes the target is already
// mounted at /mnt (postInstall handles mount/unmount).
func stageBinary(ctx *Context) error {
	user := ctx.Cfg.User.Name
	home := "/mnt/home/" + user

	self, err := os.Executable()
	if err != nil {
		ui.Warn("could not resolve own path; skipping binary staging", "err", err)
		return nil
	}

	if err := ctx.R.Root("mkdir", "-p", home); err != nil {
		return err
	}
	if err := ctx.R.Root("cp", self, home+"/archwright"); err != nil {
		return err
	}
	if err := stageConfig(ctx, home); err != nil {
		return err
	}
	return ctx.R.Chroot("/mnt", "chown", "-R", fmt.Sprintf("%s:%s", user, user), "/home/"+user)
}

// stageConfig writes the config Phase B will read into the target home. When
// ctx.FlatConfig is set (the resolved+merged config from configsrc), it is
// written to a temp file and copied in via Root("cp", ...) so the operation is
// recorded and dry-run-safe like the binary copy. With no flattened config it
// falls back to copying ctx.ConfigPath verbatim (single-file, back-compat).
func stageConfig(ctx *Context, home string) error {
	dst := home + "/config.yaml"
	if ctx.FlatConfig != nil {
		tmp, err := os.CreateTemp("", "archwright-flat-*.yaml")
		if err != nil {
			return fmt.Errorf("staging flattened config: %w", err)
		}
		tmp.Close()
		// In dry-run we still write the temp file so the recorded cp has a real
		// source; the cp itself is recorded (not executed) by the Runner.
		if err := os.WriteFile(tmp.Name(), ctx.FlatConfig, 0o600); err != nil {
			return fmt.Errorf("staging flattened config: %w", err)
		}
		return ctx.R.Root("cp", tmp.Name(), dst)
	}
	if ctx.ConfigPath != "" {
		return ctx.R.Root("cp", ctx.ConfigPath, dst)
	}
	return nil
}

func writeJSON(path string, v any, mode os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	ui.Step("wrote %s", path)
	return nil
}
