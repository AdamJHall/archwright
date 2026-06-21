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

	Disks struct {
		ESP struct {
			Device string `yaml:"device" validate:"required,startswith=/dev/"`
			Size   string `yaml:"size"   validate:"required,size"`
		} `yaml:"esp"`
		Swap struct {
			Size string `yaml:"size" validate:"required,size"`
		} `yaml:"swap"`
		LVM struct {
			VG         string   `yaml:"vg"         validate:"required"`
			LV         string   `yaml:"lv"         validate:"required"`
			Filesystem string   `yaml:"filesystem" validate:"required,oneof=xfs ext4"`
			PVs        []string `yaml:"pvs"        validate:"min=1,dive,startswith=/dev/"`
		} `yaml:"lvm"`
	} `yaml:"disks"`

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
	AUR           []string     `yaml:"aur"`

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

	Chezmoi struct {
		Repo string `yaml:"repo" validate:"omitempty,url"`
	} `yaml:"chezmoi"`

	Setup SetupConfig `yaml:"setup"`

	Hooks []Hook `yaml:"hooks" validate:"dive"`
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
type Hook struct {
	Name   string            `yaml:"name"`
	At     string            `yaml:"at"     validate:"required,hookpoint"`
	Run    string            `yaml:"run"    validate:"required_without=Script"`
	Script string            `yaml:"script" validate:"omitempty,file"`
	Root   bool              `yaml:"root"`   // run privileged (Root) vs unprivileged (Cmd/Shell)
	Env    map[string]string `yaml:"env"`
	Dir    string            `yaml:"dir"`
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
	expanded, err := expandEnv(data)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(expanded, &c); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &c, nil
}

// expandEnv substitutes ${VAR}/$VAR references in the raw config with the
// process environment, so secrets and per-machine values can stay out of the
// (gitignored) file. An unset variable is an error rather than a silent blank.
// Write "$$" for a literal "$" (e.g. inside a shell snippet meant for runtime).
func expandEnv(data []byte) ([]byte, error) {
	var missing []string
	out := os.Expand(string(data), func(name string) string {
		if name == "$" { // os.Expand maps the "$" in "$$" through here
			return "$"
		}
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		missing = append(missing, name)
		return ""
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("undefined environment variable(s) in config: %s (use $$ for a literal $)", strings.Join(missing, ", "))
	}
	return []byte(out), nil
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
