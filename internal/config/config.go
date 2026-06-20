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
		Locale   string `yaml:"locale"   validate:"required"`
		Keymap   string `yaml:"keymap"   validate:"required"`
	} `yaml:"system"`

	User struct {
		Name   string   `yaml:"name"   validate:"required"`
		Shell  string   `yaml:"shell"  validate:"omitempty,startswith=/"`
		Groups []string `yaml:"groups"`
	} `yaml:"user"`

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
	Kernel   KernelConfig `yaml:"kernel"`
	Flatpaks []string     `yaml:"flatpaks"`
	AUR      []string     `yaml:"aur"`

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

// Load reads and parses the YAML file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &c, nil
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
	default:
		return fmt.Sprintf("failed rule %q", fe.Tag())
	}
}
