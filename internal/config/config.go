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
	// PacstrapExtra are packages added to archinstall's pacstrap (Phase A), so
	// they are present on first boot — for things that must precede or be part of
	// the initial system, e.g. microcode (intel-ucode/amd-ucode) which GRUB needs
	// at config-generation time. Most software belongs in Packages (Phase B).
	PacstrapExtra []string     `yaml:"pacstrap_extra"`
	Kernel        KernelConfig `yaml:"kernel"`
	Flatpaks      []string     `yaml:"flatpaks"`
	// FlatpakRemotes are extra flatpak remotes registered (in addition to the
	// always-added built-in flathub remote) before installing apps.
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

	Setup SetupConfig `yaml:"setup"`

	Hooks []Hook `yaml:"hooks" validate:"dive"`

	// Bootloader selects which bootloader Phase A installs and Phase B configures.
	// Empty defaults to "grub" (today's behavior). systemd-boot is reverse-engineered
	// and VM-validation-pending (see CLAUDE.md archinstall-drift rule).
	Bootloader BootloaderConfig `yaml:"bootloader"`
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
type LVMLayout struct {
	VG         string   `yaml:"vg"         validate:"required"`
	LV         string   `yaml:"lv"         validate:"required"`
	Filesystem string   `yaml:"filesystem" validate:"required,oneof=xfs ext4"`
	PVs        []string `yaml:"pvs"        validate:"min=1,dive,startswith=/dev/"`
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

// Encryption enables LUKS. Type: "luks" (encrypt the single root partition,
// for plain/btrfs) or "lvm_on_luks" (encrypt the PV partitions under LVM).
type Encryption struct {
	Type string `yaml:"type" validate:"required,oneof=luks lvm_on_luks luks_on_lvm"`
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

// Hook is a user-defined command run at a named lifecycle point. Exactly one of
// Run (an inline shell snippet) or Script (a path to a script file) is set.
// Script and Dir have a leading `~` expanded to the user's home at run time.
// Script existence is NOT checked at validate time: a hook script may be
// produced by an earlier hook or stage in the same run.
type Hook struct {
	Name   string            `yaml:"name"`
	At     string            `yaml:"at"     validate:"required,hookpoint"`
	Run    string            `yaml:"run"    validate:"required_without=Script"`
	Script string            `yaml:"script" validate:"omitempty"`
	Root   bool              `yaml:"root"` // run privileged (Root) vs unprivileged (Cmd/Shell)
	Env    map[string]string `yaml:"env"`
	Dir    string            `yaml:"dir"`
}

// FlatpakRemote is an extra flatpak remote registered before installing apps.
// The built-in "flathub" remote is always added; list others here.
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
	Packages     []string `yaml:"packages"`      // e.g. [linux-cachyos, linux-cachyos-headers]
	Default      string   `yaml:"default"`       // GRUB top-level (default) kernel; must be one of Packages
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
	k := c.Kernel
	if k.ReplaceStock && len(k.Packages) == 0 {
		errs = append(errs, fmt.Errorf("kernel.replace_stock requires at least one kernel.packages entry (otherwise the system would have no kernel)"))
	}
	if k.Default != "" && !slices.Contains(k.Packages, k.Default) {
		errs = append(errs, fmt.Errorf("kernel.default %q must be one of kernel.packages", k.Default))
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
	case "lvm_on_luks", "luks_on_lvm":
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
