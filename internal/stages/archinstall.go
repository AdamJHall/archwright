package stages

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	rootDev, err := rootDevice(ctx.Cfg)
	if err != nil {
		return err
	}
	esp := partDev(ctx.Cfg.Disks.ESP.Device, 1)

	ctx.R.Try("mount", rootDev, "/mnt")
	ctx.R.Try("mount", esp, "/mnt/boot")

	if err := setupSwapfile(ctx); err != nil {
		return err
	}

	if len(ctx.Cfg.System.Locales) > 0 {
		if err := configureLocales(ctx, ctx.Cfg.System.Locales); err != nil {
			return err
		}
	}

	if len(ctx.Cfg.Repos) > 0 {
		if err := configureRepos(ctx, ctx.Cfg.Repos); err != nil {
			return err
		}
	}
	if len(ctx.Cfg.Kernel.Packages) > 0 {
		if err := installKernels(ctx, ctx.Cfg.Kernel); err != nil {
			return err
		}
	}
	if err := stageBinary(ctx); err != nil {
		return err
	}

	ctx.R.Try("umount", "/mnt/boot")
	ctx.R.Try("umount", "/mnt")
	return nil
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
		return partDev(cfg.Disks.ESP.Device, rootIdx), nil
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
