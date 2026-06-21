// Package stages defines the Stage interface and an ordered registry. Phase A
// (Install) renders + runs archinstall; Phase B (Bootstrap) does the post-install
// customization (packages, flatpaks, theming, dotfiles).
package stages

import (
	"fmt"
	"sort"
	"strconv"

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
		if only != "" && only != s.Name() && only != strconv.Itoa(s.Order()) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order() < out[j].Order() })
	return out
}

// Select returns the stages of phase p to run. When only != "", it wins and
// returns just that stage (matched by name or number), ignoring skip/disable.
// Otherwise it returns all phase stages minus any whose name or order-number
// appears in skip or disable.
func Select(p Phase, only string, skip, disable []string) []Stage {
	if only != "" {
		return For(p, only)
	}
	excluded := make(map[string]bool)
	for _, e := range skip {
		excluded[e] = true
	}
	for _, e := range disable {
		excluded[e] = true
	}
	var out []Stage
	for _, s := range For(p, "") {
		if excluded[s.Name()] || excluded[strconv.Itoa(s.Order())] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Within narrows an already-selected, order-sorted slice of stages to the
// inclusive window [from, to] — the --from / --to resume flags. Each bound is
// resolved by stage name OR order-number, exactly like --only, but matched
// against the stages actually present in selected (so it composes cleanly with
// --skip / disable, which run first via Select). An empty bound means open on
// that end. It returns an error if a bound names a stage not in selected, or if
// the bounds are inverted (from after to).
func Within(selected []Stage, from, to string) ([]Stage, error) {
	if len(selected) == 0 {
		return selected, nil
	}
	lo := 0
	if from != "" {
		i := indexOf(selected, from)
		if i < 0 {
			return nil, fmt.Errorf("--from: no stage %q in the selected set", from)
		}
		lo = i
	}
	hi := len(selected) - 1
	if to != "" {
		i := indexOf(selected, to)
		if i < 0 {
			return nil, fmt.Errorf("--to: no stage %q in the selected set", to)
		}
		hi = i
	}
	if lo > hi {
		return nil, fmt.Errorf("--from %q comes after --to %q", from, to)
	}
	return selected[lo : hi+1], nil
}

// indexOf returns the position of the stage matching ref (by name or order
// number) within ss, or -1 if none matches.
func indexOf(ss []Stage, ref string) int {
	for i, s := range ss {
		if ref == s.Name() || ref == strconv.Itoa(s.Order()) {
			return i
		}
	}
	return -1
}

// All returns every registered stage across all phases, sorted by phase then order.
func All() []Stage {
	out := append([]Stage(nil), registry...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Phase() != out[j].Phase() {
			return out[i].Phase() < out[j].Phase()
		}
		return out[i].Order() < out[j].Order()
	})
	return out
}
