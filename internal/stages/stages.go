// Package stages defines the Stage interface and an ordered registry. Phase A
// (Install) renders + runs archinstall; Phase B (Bootstrap) does the post-install
// customization (packages, flatpaks, theming, dotfiles).
package stages

import (
	"sort"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/run"
)

type Phase int

const (
	Install   Phase = iota // Phase A — live ISO, root
	Bootstrap              // Phase B — booted system, user
)

// Context is passed to every stage: the parsed config, the command runner,
// whether destructive confirmations should be skipped (--yes), and the path to
// the config file (so Phase A can stage it for Phase B).
type Context struct {
	Cfg        *config.Config
	R          *run.Runner
	AssumeYes  bool
	ConfigPath string
}

// Stage is one ordered unit of work. Order is the numeric prefix (10, 20, ...)
// kept stable so --only by number still works.
type Stage interface {
	Order() int
	Name() string
	Phase() Phase
	Run(ctx *Context) error
}

var registry []Stage

// register is called from each stage's init(); keeps wiring local to the stage.
func register(s Stage) { registry = append(registry, s) }

// For returns the stages of a phase, sorted by Order. If only != "", it filters
// to stages whose name or order matches (the --only flag).
func For(p Phase, only string) []Stage {
	var out []Stage
	for _, s := range registry {
		if s.Phase() != p {
			continue
		}
		if only != "" && only != s.Name() && only != itoa(s.Order()) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order() < out[j].Order() })
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
