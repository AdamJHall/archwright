package stages

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AdamJHall/archwright/internal/config"
	"github.com/AdamJHall/archwright/internal/ui"
)

// PhasePre returns the global "pre" lifecycle point for a phase:
// Install -> "pre-install", Bootstrap -> "pre-bootstrap".
func PhasePre(p Phase) string {
	if p == Install {
		return "pre-install"
	}
	return "pre-bootstrap"
}

// PhasePost returns the global "post" lifecycle point for a phase:
// Install -> "post-install", Bootstrap -> "post-bootstrap".
func PhasePost(p Phase) string {
	if p == Install {
		return "post-install"
	}
	return "post-bootstrap"
}

// FireHooks runs every configured hook whose At matches the given lifecycle
// point, in config order, routed through the Runner so it is dry-run-recorded.
// Each hook's Env and Dir apply to that hook's command only — they are restored
// afterwards so they never leak into later commands.
func FireHooks(ctx *Context, at string) error {
	for _, h := range ctx.Cfg.Hooks {
		if h.At != at {
			continue
		}
		if err := fireOne(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

// fireOne runs a single hook, applying (and restoring) its Env/Dir.
func fireOne(ctx *Context, h config.Hook) error {
	label := h.Name
	if label == "" {
		label = h.At
	}
	ui.Info("hook", "name", label, "at", h.At)

	// Save and restore the runner's Env/Dir so a hook's environment and working
	// directory never bleed into subsequent commands.
	savedEnv, savedDir := ctx.R.Env, ctx.R.Dir
	defer func() { ctx.R.Env, ctx.R.Dir = savedEnv, savedDir }()

	if len(h.Env) > 0 {
		merged := make(map[string]string, len(savedEnv)+len(h.Env))
		for k, v := range savedEnv {
			merged[k] = v
		}
		for k, v := range h.Env {
			merged[k] = v
		}
		ctx.R.Env = merged
	}
	if h.Dir != "" {
		ctx.R.Dir = expandHome(h.Dir)
	}

	switch {
	case h.Run != "":
		if h.Root {
			return ctx.R.Root("bash", "-c", h.Run)
		}
		return ctx.R.Shell(h.Run)
	case h.Script != "":
		script := expandHome(h.Script)
		if h.Root {
			return ctx.R.Root("bash", script)
		}
		return ctx.R.Cmd("bash", script)
	}
	return nil
}

// ValidateHooks checks that every before:/after: hook targets a real registered
// stage name. Returns a joined error listing every unknown target. The global
// lifecycle points and the well-formedness of `at` are validated by the config
// package's hookpoint rule; this only resolves stage references against the
// registry, which lives here.
func ValidateHooks(cfg *config.Config) error {
	known := make(map[string]bool, len(registry))
	for _, s := range registry {
		known[s.Name()] = true
	}
	var errs []error
	for i, h := range cfg.Hooks {
		var target string
		switch {
		case strings.HasPrefix(h.At, "before:"):
			target = strings.TrimPrefix(h.At, "before:")
		case strings.HasPrefix(h.At, "after:"):
			target = strings.TrimPrefix(h.At, "after:")
		default:
			continue // global point — nothing to resolve
		}
		if !known[target] {
			errs = append(errs, fmt.Errorf("hooks[%d].at references unknown stage %q", i, target))
		}
	}
	return errors.Join(errs...)
}
