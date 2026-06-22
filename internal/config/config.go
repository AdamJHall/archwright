// Package config loads and validates config.yaml — the single source of truth.
// This is the same file the bash implementation consumes; only the parser changes.
//
// Validation rules live as `validate` struct tags (go-playground/validator), so
// the struct doubles as the schema. Validate() reports every problem at once.
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// Config mirrors config.yaml. `yaml` tags map keys to fields; `validate` tags
// declare the rules enforced by Validate().
type Config struct {
	System struct {
		Hostname string `yaml:"hostname" validate:"required,hostname_rfc1123"`
		Timezone string `yaml:"timezone" validate:"required"`
		Locale   string `yaml:"locale"   validate:"required"` // default LANG; enabled by the installer
		// Locales are additional locales to enable in /etc/locale.gen (the default
		// `locale` above is enabled automatically). Generated in Phase A's chroot so
		// the system has e.g. both en_AU.UTF-8 (default) and en_US.UTF-8 on first boot.
		Locales []string `yaml:"locales"`
		Keymap  string   `yaml:"keymap" validate:"required"`
		NTP     *bool    `yaml:"ntp"` // enable NTP time sync; nil/unset defaults to true (today's behavior)
	} `yaml:"system"`

	User struct {
		Name   string   `yaml:"name"   validate:"required"`
		Shell  string   `yaml:"shell"  validate:"omitempty,startswith=/"`
		Groups []string `yaml:"groups"`
	} `yaml:"user"`

	Stages struct {
		Disable []string `yaml:"disable"` // stage names to skip without emptying their config
	} `yaml:"stages"`

	Disks DisksConfig `yaml:"disks"`

	Mirrors  MirrorConfig `yaml:"mirrors"`
	Repos    []Repo       `yaml:"repos" validate:"dive"`
	Packages []string     `yaml:"packages"`
	// Pacstrap is the COMPLETE Phase-A pacstrap set, rendered verbatim into the
	// archinstall config — nothing is prepended in code. It must list everything
	// the installed system needs at first boot: the base set Phase B relies on
	// (base-devel/git to build the AUR helper, the login shell, sudo,
	// networkmanager for first-boot networking), boot tooling (efibootmgr), and
	// CPU microcode (intel-ucode/amd-ucode). preflight warns about recommended-
	// but-absent entries; it never silently re-adds them. Most software belongs
	// in Packages (Phase B).
	Pacstrap []string     `yaml:"pacstrap" validate:"required,min=1,dive,required"`
	Kernel   KernelConfig `yaml:"kernel"`
	// Flatpaks lists apps to install, each as a "remote:appid" reference whose
	// remote must be declared in FlatpakRemotes (enforced in semanticErrors).
	Flatpaks []string `yaml:"flatpaks"`
	// FlatpakRemotes is the COMPLETE set of flatpak remotes registered before
	// installing apps — nothing is implicit. If you install from Flathub, list it
	// here.
	FlatpakRemotes []FlatpakRemote `yaml:"flatpak_remotes" validate:"dive"`
	AUR            []string        `yaml:"aur"`
	// AurHelper selects the AUR helper to install and use in Phase B. Empty
	// defaults to "yay" (today's behavior); "paru" is argument-compatible.
	AurHelper string `yaml:"aur_helper" validate:"omitempty,oneof=yay paru"`

	Plymouth struct {
		Theme string `yaml:"theme"`
	} `yaml:"plymouth"`

	GRUB struct {
		CmdlineExtra string `yaml:"cmdline_extra"`
		Theme        struct {
			Source string `yaml:"source" validate:"omitempty,oneof=vinceliuice url none"`
			Name   string `yaml:"name"`
			URL    string `yaml:"url" validate:"omitempty,url"`
		} `yaml:"theme"`
	} `yaml:"grub"`

	KDE struct {
		LookAndFeel string `yaml:"look_and_feel"`
		ColorScheme string `yaml:"color_scheme"`
		CursorTheme string `yaml:"cursor_theme"`
		Wallpaper   string `yaml:"wallpaper"`
	} `yaml:"kde"`

	// Desktop selects which desktop-environment stage runs in Phase B. Empty (the
	// default) preserves today's behavior: the KDE stage runs. Any other value
	// makes the KDE stage a clean no-op (other DEs are not yet implemented).
	Desktop struct {
		Environment string `yaml:"environment" validate:"omitempty,oneof=kde gnome hyprland sway none"`
	} `yaml:"desktop"`

	Chezmoi struct {
		Repo string `yaml:"repo" validate:"omitempty,url"`
	} `yaml:"chezmoi"`

	// Dotfiles selects how dotfiles are applied in Phase B. Manager defaults to
	// "chezmoi"; Repo defaults to chezmoi.repo when unset (backward compatible).
	Dotfiles struct {
		Manager string `yaml:"manager" validate:"omitempty,oneof=chezmoi yadm bare-git none"`
		Repo    string `yaml:"repo"    validate:"omitempty,url"`
	} `yaml:"dotfiles"`

	Setup SetupConfig `yaml:"setup"`

	// Services lists systemd units to enable in Phase B so they start on the next
	// boot (e.g. a display-manager unit like plasmalogin.service). Enabling is
	// idempotent, so the stage is safe to re-run. Unset = no services touched.
	Services ServicesConfig `yaml:"services"`

	Hooks []Hook `yaml:"hooks" validate:"dive"`

	// Bootloader selects which bootloader Phase A installs and Phase B configures.
	// Empty defaults to "grub" (today's behavior). systemd-boot is reverse-engineered
	// and VM-validation-pending (see CLAUDE.md archinstall-drift rule).
	Bootloader BootloaderConfig `yaml:"bootloader"`

	// PacstrapExtraDeprecated captures the removed `pacstrap_extra` key so
	// semanticErrors can emit a clear rename-to-`pacstrap` migration error for one
	// release, rather than silently ignoring a config that used the old key. It is
	// not part of the schema and has no validate rules.
	PacstrapExtraDeprecated []string `yaml:"pacstrap_extra"`
}

// DisksConfig describes the disk layout archinstall should create. Layout is a
// discriminator selecting one of the layout strategies; the matching sub-block
// (LVM/Btrfs/Plain) must be present. Layout defaults to "lvm" when empty so
// pre-existing configs (which never set it) keep their original behaviour.
//
// Cross-field rules (the right sub-block present for the chosen layout, swap-type
// constraints) live in semanticErrors(), not in struct tags, because they span
// fields.
type DisksConfig struct {
	// Layout selects the partitioning strategy: "lvm" (ESP + LVM-on-partitions
	// root), "btrfs" (ESP + single btrfs root with subvolumes), or "plain" (ESP +
	// single ext4/xfs root). Empty defaults to "lvm".
	Layout string `yaml:"layout" validate:"omitempty,oneof=lvm btrfs plain"`

	ESP  ESPConfig  `yaml:"esp"`
	Swap SwapConfig `yaml:"swap"`

	LVM   *LVMLayout   `yaml:"lvm"`   // required when layout is lvm (or empty)
	Btrfs *BtrfsLayout `yaml:"btrfs"` // required when layout is btrfs
	Plain *PlainLayout `yaml:"plain"` // required when layout is plain

	Encryption *Encryption `yaml:"encryption"` // optional LUKS; nil = no encryption
}

// EffectiveLayout returns the configured layout with the empty-default applied.
func (d DisksConfig) EffectiveLayout() string {
	if d.Layout == "" {
		return "lvm"
	}
	return d.Layout
}

// ESPConfig is the EFI system partition created on disk 1.
type ESPConfig struct {
	Device string `yaml:"device" validate:"required,startswith=/dev/"`
	Size   string `yaml:"size"   validate:"required,size"`
}

// SwapConfig selects how swap is provided. Type defaults to "swapfile" (the
// original behaviour) when empty:
//   - swapfile: a post-install /swapfile sized from Size (the only LVM-compatible
//     option; archinstall 4.x can't format a raw swap partition in an LVM layout).
//   - zram: compressed RAM swap (archinstall's own `swap` flag); no on-disk swap.
//   - partition: a real linux-swap partition (only valid for plain/btrfs layouts).
//   - none: no swap at all.
type SwapConfig struct {
	Type string `yaml:"type" validate:"omitempty,oneof=swapfile zram partition none"`
	Size string `yaml:"size" validate:"omitempty,size"`
}

// EffectiveType returns the configured swap type with the empty-default applied.
func (s SwapConfig) EffectiveType() string {
	if s.Type == "" {
		return "swapfile"
	}
	return s.Type
}

// LVMLayout is the classic ESP + LVM-on-partitions root (the historical default).
//
// Two mutually-exclusive shapes (enforced in lvmVolumeErrors):
//   - single root LV: set LV + Filesystem, leave Volumes empty (the historical
//     shape; behaviour and rendered output are byte-identical).
//   - multiple volumes: set Volumes (e.g. a fixed root + a /home that takes the
//     remainder), leave LV/Filesystem empty.
type LVMLayout struct {
	VG         string      `yaml:"vg"         validate:"required"`
	LV         string      `yaml:"lv"         validate:"omitempty"`                // single-LV mode: the root LV name
	Filesystem string      `yaml:"filesystem" validate:"omitempty,oneof=xfs ext4"` // single-LV mode: root filesystem
	PVs        []string    `yaml:"pvs"        validate:"min=1,dive,startswith=/dev/"`
	Volumes    []LVMVolume `yaml:"volumes"    validate:"dive"` // optional; empty = single root LV from LV/Filesystem
}

// LVMVolume is one logical volume in the VG. Exactly one volume in the list may
// omit Size (it receives the remainder of the VG); the rest are fixed-size.
type LVMVolume struct {
	Name       string `yaml:"name"       validate:"required"`
	Mountpoint string `yaml:"mountpoint" validate:"required,startswith=/"`
	Filesystem string `yaml:"filesystem" validate:"required,oneof=xfs ext4"`
	Size       string `yaml:"size"       validate:"omitempty,size"` // empty = rest of VG
}

// PlainLayout is a single root partition with no LVM: ESP + root on one disk.
type PlainLayout struct {
	Device     string `yaml:"device"     validate:"required,startswith=/dev/"`
	Filesystem string `yaml:"filesystem" validate:"required,oneof=xfs ext4"`
}

// BtrfsLayout is a single btrfs root partition carrying subvolumes (the common
// snapshot-friendly desktop layout). Compress (e.g. "zstd" or "zstd:3") becomes a
// compress= mount option; Snapshots selects an optional snapshot tool.
type BtrfsLayout struct {
	Device     string   `yaml:"device"     validate:"required,startswith=/dev/"`
	Compress   string   `yaml:"compress"`
	Snapshots  string   `yaml:"snapshots"  validate:"omitempty,oneof=snapper none"`
	Subvolumes []Subvol `yaml:"subvolumes" validate:"dive"`
}

// Subvol is one btrfs subvolume and where it is mounted (e.g. {@, /} or
// {@home, /home}).
type Subvol struct {
	Name       string `yaml:"name"       validate:"required"`
	Mountpoint string `yaml:"mountpoint" validate:"required"`
}

// Encryption enables LUKS. Type is one of:
//   - "luks": encrypt the single root partition (for the plain/btrfs layouts).
//   - "lvm_on_luks": encrypt the PV partitions under LVM (for the lvm layout).
//
// The "luks_on_lvm" topology (encrypt individual LVs on top of an unencrypted
// VG) is intentionally NOT accepted: it is unimplemented in the archinstall
// renderer, so allowing it would silently produce the wrong (lvm_on_luks)
// layout. Rejecting it here surfaces a clear validation error instead.
type Encryption struct {
	Type string `yaml:"type" validate:"required,oneof=luks lvm_on_luks"`
}

// SetupConfig drives the Phase B 85-setup stage, which runs after chezmoi has
// applied the dotfiles. It covers the things a dotfiles repo references but can't
// vendor itself — oh-my-zsh and its custom plugins, tmux's TPM, theme repos.
//
// Steps run strictly in the order written, so a clone that lands inside another
// clone's tree (or a command that must precede a clone) is sequenced just by
// where it appears in the list.
type SetupConfig struct {
	Steps []Step `yaml:"steps" validate:"dive"`
}

// Step is one setup action: exactly one of Clone or Command is set (enforced in
// semanticErrors). Command is a shell snippet run via the runner; Clone is a git
// clone. They share one list so they can be interleaved in any order.
type Step struct {
	Command string `yaml:"command"`
	Clone   *Clone `yaml:"clone"`
}

// Clone is a git repo cloned into Dest (a path, `~` expanded to the user's home).
// It is idempotent: skipped when Dest already exists, or `git pull`ed instead
// when Update is set. Ref optionally checks out a branch or tag.
type Clone struct {
	URL    string `yaml:"url"    validate:"required,url"`
	Dest   string `yaml:"dest"   validate:"required"`
	Ref    string `yaml:"ref"`
	Update bool   `yaml:"update"`
}

// ServicesConfig drives the Phase B 90-services stage, which runs last (after
// dotfiles + setup) and `systemctl enable`s the listed units so they start on
// the next boot. Enable holds system units (enabled as root); User holds
// per-user units (enabled with `systemctl --user`, unprivileged). Both default
// to empty, so an unset block touches no services. The trailing ".service" is
// optional — systemctl accepts a bare unit name.
type ServicesConfig struct {
	Enable []string `yaml:"enable"` // system units: systemctl enable <unit>...
	User   []string `yaml:"user"`   // user units: systemctl --user enable <unit>...
}

// Hook is a user-defined command run at a named lifecycle point. Exactly one of
// Run (an inline shell snippet) or Script (a path to a script file) is set.
// Script and Dir have a leading `~` expanded to the user's home at run time.
// Script existence is NOT checked at validate time: a hook script may be
// produced by an earlier hook or stage in the same run.
//
// Name is required: it identifies the hook for layered-config merge-by-name (a
// nameless element would silently flip the whole hooks slice from merge to
// wholesale replace — see internal/configsrc/merge.go) and for diagnostics.
type Hook struct {
	Name   string            `yaml:"name"   validate:"required"`
	At     string            `yaml:"at"     validate:"required,hookpoint"`
	Run    string            `yaml:"run"    validate:"required_without=Script"`
	Script string            `yaml:"script" validate:"omitempty"`
	Root   bool              `yaml:"root"` // run privileged (Root) vs unprivileged (Cmd/Shell)
	Env    map[string]string `yaml:"env"`
	Dir    string            `yaml:"dir"`
}

// FlatpakRemote is a flatpak remote registered before installing apps. The
// flatpak_remotes list is complete — no remote (not even flathub) is added
// implicitly, so every remote an app installs from must appear here.
type FlatpakRemote struct {
	Name string `yaml:"name" validate:"required"`
	URL  string `yaml:"url"  validate:"required,url"`
}

// Repo is a custom pacman repository. It is configured in Phase A's
// post-archinstall chroot (so a custom kernel can be installed before first boot)
// and persists into the installed system. The fields are independent primitives —
// use whichever a given repo needs:
//   - Key (+ optional Keyserver): import & locally sign the repo's signing key.
//   - Setup: a shell snippet to run as root, for repos that ship a maintained
//     installer (e.g. CachyOS, which detects x86-64-v3/v4 CPU features). Avoids
//     pinning versioned keyring/mirrorlist package URLs. Must not use `sudo` — it
//     already runs as root.
//   - Server or Include: the line written into the repo's pacman.conf section.
type Repo struct {
	Name      string `yaml:"name"      validate:"required"`
	Key       string `yaml:"key"`
	Keyserver string `yaml:"keyserver"`
	Setup     string `yaml:"setup"`
	Server    string `yaml:"server"`
	Include   string `yaml:"include"`
}

// KernelConfig selects extra kernels, installed from the repos above in Phase A's
// post-archinstall chroot so the first boot already runs them. archinstall always
// pacstraps the stock `linux` kernel for a bootable baseline; ReplaceStock removes
// it afterwards (before first boot) so nothing lingers.
type KernelConfig struct {
	// Base are the kernel(s) archinstall pacstraps for a bootable baseline. They
	// must be available in the live ISO's official repos (the custom `repos:` are
	// configured later, in the chroot), so a custom kernel like linux-cachyos
	// belongs in Packages, not Base.
	Base         []string `yaml:"base" validate:"required,min=1,dive,required"`
	Packages     []string `yaml:"packages"`      // extra kernels installed in the chroot (post-repo-setup)
	Default      string   `yaml:"default"`       // GRUB top-level (default) kernel; must be in Base ∪ Packages
	ReplaceStock bool     `yaml:"replace_stock"` // remove the stock `linux` kernel after install
}

// BootloaderConfig selects the bootloader Phase A installs (via archinstall) and
// Phase B configures (kernel cmdline + boot config regeneration). Empty Kind
// defaults to "grub" (today's behavior); "systemd-boot" is reverse-engineered and
// VM-validation-pending (see CLAUDE.md archinstall-drift rule).
type BootloaderConfig struct {
	Kind string `yaml:"kind" validate:"omitempty,oneof=grub systemd-boot"`
}

// EffectiveKind returns the configured bootloader with the empty-default applied.
func (b BootloaderConfig) EffectiveKind() string {
	if b.Kind == "" {
		return "grub"
	}
	return b.Kind
}

// MirrorConfig drives a reflector run in the live ISO before archinstall, so
// pacstrap (and the installed system) use fast, recent mirrors. Reflector runs
// only when Enabled.
type MirrorConfig struct {
	Enabled   bool     `yaml:"reflector"` // run reflector at all
	Countries []string `yaml:"countries"` // --country (e.g. [GB, DE]); empty = worldwide
	Latest    int      `yaml:"latest"`    // --latest N most-recently-synced
	Fastest   int      `yaml:"fastest"`   // --fastest N by download rate
	Sort      string   `yaml:"sort" validate:"omitempty,oneof=rate age score delay country"`
	Protocols []string `yaml:"protocols" validate:"dive,oneof=https http rsync ftp"` // --protocol
}

// Load reads, env-substitutes, and parses the YAML file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := expandEnv(&root); err != nil {
		return nil, err
	}
	var c Config
	if err := root.Decode(&c); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &c, nil
}

// ExpandEnvNode applies the exact same value-only env substitution used by Load
// to a parsed YAML node tree. It exists so internal/configsrc can expand each
// merge layer with identical semantics ($$->$, unset-var errors, keys and
// comments untouched) without duplicating the logic. It is a thin wrapper over
// the unexported expandEnv; keep them in lockstep.
func ExpandEnvNode(n *yaml.Node) error { return expandEnv(n) }

// expandEnv substitutes ${VAR}/$VAR references in the config's scalar VALUES
// with the process environment, so secrets and per-machine values can stay out
// of the (gitignored) file. It walks the parsed YAML node tree and expands only
// value scalars — mapping keys and comments are left untouched, so a literal "$"
// in a comment or key is harmless. An unset variable is an error rather than a
// silent blank. Write "$$" for a literal "$" (e.g. inside a shell snippet meant
// for runtime). Returns a joined error naming every missing variable.
func expandEnv(n *yaml.Node) error {
	var missing []string
	walkValues(n, func(v *yaml.Node) {
		v.Value = os.Expand(v.Value, func(name string) string {
			if name == "$" { // os.Expand maps the "$" in "$$" through here
				return "$"
			}
			if val, ok := os.LookupEnv(name); ok {
				return val
			}
			missing = append(missing, name)
			return ""
		})
	})
	if len(missing) > 0 {
		return fmt.Errorf("undefined environment variable(s) in config: %s (use $$ for a literal $)", strings.Join(missing, ", "))
	}
	return nil
}

// walkValues invokes fn on every scalar VALUE node reachable from n, skipping
// mapping key nodes (so keys are never substituted). Document, sequence and
// alias nodes are traversed transparently; anchors carry their (already-walked)
// value through aliases, so an alias is left untouched to avoid double-expansion.
func walkValues(n *yaml.Node, fn func(*yaml.Node)) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			walkValues(c, fn)
		}
	case yaml.MappingNode:
		// Content is [key0, val0, key1, val1, ...]; recurse into values only.
		for i := 1; i < len(n.Content); i += 2 {
			walkValues(n.Content[i], fn)
		}
	case yaml.ScalarNode:
		fn(n)
	}
}

// Validate runs the struct-tag schema and returns every failure joined together,
// each phrased against the config's YAML path (e.g. "disks.lvm.filesystem").
func (c *Config) Validate() error {
	var errs []error
	if err := validate.Struct(c); err != nil {
		var ve validator.ValidationErrors
		if !errors.As(err, &ve) {
			return err // an internal validator error, not a rule failure
		}
		for _, fe := range ve {
			errs = append(errs, fmt.Errorf("%s %s", fieldPath(fe), describe(fe)))
		}
	}
	errs = append(errs, c.semanticErrors()...)
	return errors.Join(errs...) // nil when errs is empty/all-nil
}

// semanticErrors covers cross-field rules that struct tags can't express.
func (c *Config) semanticErrors() []error {
	var errs []error
	if len(c.PacstrapExtraDeprecated) > 0 {
		errs = append(errs, fmt.Errorf("pacstrap_extra has been removed: rename it to `pacstrap` and add the base set (base-devel, git, zsh, sudo, networkmanager, efibootmgr, intel-ucode) — see config.example.yaml"))
	}
	k := c.Kernel
	if k.ReplaceStock && len(k.Packages) == 0 {
		errs = append(errs, fmt.Errorf("kernel.replace_stock requires at least one kernel.packages entry (otherwise the system would have no kernel)"))
	}
	if k.Default != "" && !slices.Contains(k.Base, k.Default) && !slices.Contains(k.Packages, k.Default) {
		errs = append(errs, fmt.Errorf("kernel.default %q must be one of kernel.base or kernel.packages", k.Default))
	}
	for i, s := range c.Setup.Steps {
		switch {
		case s.Command != "" && s.Clone != nil:
			errs = append(errs, fmt.Errorf("setup.steps[%d] must set exactly one of command or clone, not both", i))
		case s.Command == "" && s.Clone == nil:
			errs = append(errs, fmt.Errorf("setup.steps[%d] must set either command or clone", i))
		}
	}
	errs = append(errs, c.diskErrors()...)
	errs = append(errs, c.encryptionErrors()...)
	errs = append(errs, c.lvmVolumeErrors()...)
	errs = append(errs, c.flatpakErrors()...)
	return errs
}

// flatpakErrors enforces the per-app remote rules struct tags can't express:
// every flatpaks entry is a "remote:appid" reference (exactly one ":", both
// halves non-empty) whose remote is declared in flatpak_remotes. This catches
// typos and undeclared remotes at validate time, before anything runs.
func (c *Config) flatpakErrors() []error {
	var errs []error
	declared := make(map[string]bool, len(c.FlatpakRemotes))
	for _, rem := range c.FlatpakRemotes {
		declared[rem.Name] = true
	}
	for i, app := range c.Flatpaks {
		remote, appid, ok := strings.Cut(app, ":")
		if !ok || remote == "" || appid == "" {
			errs = append(errs, fmt.Errorf("flatpaks[%d] %q must be \"remote:appid\" (e.g. flathub:com.example.App)", i, app))
			continue
		}
		if !declared[remote] {
			errs = append(errs, fmt.Errorf("flatpaks[%d] remote %q is not declared in flatpak_remotes (add it there or fix the name)", i, remote))
		}
	}
	return errs
}

// encryptionErrors covers the cross-field LUKS rules: the encryption type must
// match the chosen layout, and archinstall's LVM-on-LUKS path rejects more than
// two PV partitions (archinstall/lib/disk/device_handler.py). Kept separate from
// diskErrors so the two concerns stay disjoint.
//
// VM-validation-pending: the >2-PV limit and exact archinstall rejection
// behaviour have not yet been confirmed against a real archinstall run.
func (c *Config) encryptionErrors() []error {
	var errs []error
	d := c.Disks
	if d.Encryption == nil {
		return errs
	}
	layout := d.EffectiveLayout()
	switch d.Encryption.Type {
	case "lvm_on_luks":
		if layout != "lvm" {
			errs = append(errs, fmt.Errorf("disks.encryption.type %s requires the lvm layout", d.Encryption.Type))
		}
		if layout == "lvm" && d.LVM != nil && len(d.LVM.PVs) > 2 {
			errs = append(errs, fmt.Errorf("disks.encryption.type %s supports at most 2 PVs (archinstall limit); got %d", d.Encryption.Type, len(d.LVM.PVs)))
		}
	case "luks":
		if layout != "plain" && layout != "btrfs" {
			errs = append(errs, fmt.Errorf("disks.encryption.type luks requires the plain or btrfs layout"))
		}
	}
	return errs
}

// lvmVolumeErrors enforces the cross-field rules struct tags can't express for
// the lvm layout: an LVMLayout is either single-LV mode (LV+Filesystem set,
// Volumes empty) or multi-volume mode (Volumes set, LV/Filesystem empty), never
// both and never neither; and in multi-volume mode exactly one volume omits Size
// (the remainder) and exactly one is mounted at "/".
func (c *Config) lvmVolumeErrors() []error {
	d := c.Disks
	// Only relevant for the lvm layout with an lvm block present.
	if d.EffectiveLayout() != "lvm" || d.LVM == nil {
		return nil
	}
	l := d.LVM

	var errs []error
	single := l.LV != "" || l.Filesystem != ""
	multi := len(l.Volumes) > 0
	switch {
	case single && multi:
		errs = append(errs, fmt.Errorf("disks.lvm must set either lv+filesystem (single root LV) or volumes, not both"))
		return errs
	case !single && !multi:
		errs = append(errs, fmt.Errorf("disks.lvm must set either lv+filesystem (single root LV) or volumes"))
		return errs
	}

	if single {
		// In single-LV mode both lv and filesystem are required (the relaxed
		// struct tags allow one without the other; enforce the pair here).
		if l.LV == "" {
			errs = append(errs, fmt.Errorf("disks.lvm.lv is required when volumes is not set"))
		}
		if l.Filesystem == "" {
			errs = append(errs, fmt.Errorf("disks.lvm.filesystem is required when volumes is not set"))
		}
		return errs
	}

	// Multi-volume mode: exactly one rest (size-less) volume and exactly one "/".
	rest, roots := 0, 0
	for _, v := range l.Volumes {
		if v.Size == "" {
			rest++
		}
		if v.Mountpoint == "/" {
			roots++
		}
	}
	if rest != 1 {
		errs = append(errs, fmt.Errorf("disks.lvm.volumes must have exactly one volume without a size (it takes the rest of the VG); found %d", rest))
	}
	if roots != 1 {
		errs = append(errs, fmt.Errorf("disks.lvm.volumes must have exactly one volume mounted at / (the root); found %d", roots))
	}
	return errs
}

// diskErrors covers the cross-field disk rules struct tags can't express: the
// sub-block matching the chosen layout must be present (and unrelated ones absent
// is tolerated but the matching one is required), and a swap partition is only
// valid for partition-based layouts (not LVM).
func (c *Config) diskErrors() []error {
	var errs []error
	d := c.Disks
	switch d.EffectiveLayout() {
	case "lvm":
		if d.LVM == nil {
			errs = append(errs, fmt.Errorf("disks.lvm is required when disks.layout is lvm"))
		}
	case "btrfs":
		if d.Btrfs == nil {
			errs = append(errs, fmt.Errorf("disks.btrfs is required when disks.layout is btrfs"))
		}
	case "plain":
		if d.Plain == nil {
			errs = append(errs, fmt.Errorf("disks.plain is required when disks.layout is plain"))
		}
	}

	switch d.Swap.EffectiveType() {
	case "swapfile", "partition":
		if d.Swap.Size == "" {
			errs = append(errs, fmt.Errorf("disks.swap.size is required when disks.swap.type is %s", d.Swap.EffectiveType()))
		}
	}
	if d.Swap.EffectiveType() == "partition" && d.EffectiveLayout() == "lvm" {
		errs = append(errs, fmt.Errorf("disks.swap.type partition is not supported with the lvm layout (use swapfile or zram)"))
	}
	return errs
}

// --- validator setup --------------------------------------------------------

var validate = newValidator()

// sizeRe matches sizes like "4GiB", "64G", "512MiB", "1024".
var sizeRe = regexp.MustCompile(`^\d+(\.\d+)?([KMGT]i?)?B?$`)

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())

	// Report errors using the YAML key name, not the Go field name.
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("yaml"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})

	// Custom rule: human-readable disk size string.
	_ = v.RegisterValidation("size", func(fl validator.FieldLevel) bool {
		return sizeRe.MatchString(fl.Field().String())
	})

	// Custom rule: a hook lifecycle point — one of the four global points, or a
	// per-stage "before:<stage>"/"after:<stage>" with a non-empty stage token.
	// The existence of the named stage is validated elsewhere (the stages package
	// owns the registry), keeping this package dependency-free.
	_ = v.RegisterValidation("hookpoint", func(fl validator.FieldLevel) bool {
		at := fl.Field().String()
		switch at {
		case "pre-install", "post-install", "pre-bootstrap", "post-bootstrap":
			return true
		}
		if t, ok := strings.CutPrefix(at, "before:"); ok {
			return t != ""
		}
		if t, ok := strings.CutPrefix(at, "after:"); ok {
			return t != ""
		}
		return false
	})

	return v
}

// fieldPath turns validator's "Config.disks.lvm.filesystem" namespace into the
// "disks.lvm.filesystem" the user actually wrote in YAML.
func fieldPath(fe validator.FieldError) string {
	return strings.TrimPrefix(fe.Namespace(), "Config.")
}

// describe renders a friendly message for a failed rule.
func describe(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "oneof":
		return fmt.Sprintf("must be one of: %s", fe.Param())
	case "startswith":
		return fmt.Sprintf("must start with %q", fe.Param())
	case "min":
		return fmt.Sprintf("must have at least %s item(s)", fe.Param())
	case "url":
		return "must be a valid URL"
	case "hostname_rfc1123":
		return "must be a valid hostname"
	case "size":
		return "must be a size like 64GiB"
	case "hookpoint":
		return `must be a lifecycle point: pre-install, post-install, pre-bootstrap, post-bootstrap, or before:<stage>/after:<stage>`
	case "required_without":
		return "is required when script is not set"
	default:
		return fmt.Sprintf("failed rule %q", fe.Tag())
	}
}
