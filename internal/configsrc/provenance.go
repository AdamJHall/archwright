package configsrc

import "strings"

// Source records one resolved config layer, for trust UX + provenance output.
type Source struct {
	Ref      string // original ref string as written (top-level --config arg or an imports: entry)
	Kind     string // "local" | "github" | "url"
	URL      string // resolved fetch URL ("" for local refs)
	Pinned   bool   // github ref carried @<tag-or-sha>
	Unpinned bool   // github ref WITHOUT @ref (resolved to HEAD) — the trust risk
}

// ProvenanceComment renders srcs as a YAML comment block (each line "# ...", trailing
// newline) suitable for prepending to flattened render output. It returns "" when there
// are NO remote (github/url) sources — pure-local renders stay header-free.
func ProvenanceComment(srcs []Source) string {
	if !hasRemote(srcs) {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Flattened by archwright from:\n")
	for _, s := range srcs {
		b.WriteString("#   - ")
		b.WriteString(s.Ref)
		b.WriteString(" (")
		b.WriteString(s.descriptor())
		b.WriteString(")")
		if s.URL != "" {
			b.WriteString(" -> ")
			b.WriteString(s.URL)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// descriptor is the parenthetical kind/trust label for a source line.
func (s Source) descriptor() string {
	switch s.Kind {
	case "github":
		switch {
		case s.Unpinned:
			return "github, UNPINNED->HEAD"
		case s.Pinned:
			return "github, pinned"
		default:
			return "github"
		}
	default:
		return s.Kind
	}
}

// hasRemote reports whether any source is a remote (github/url) ref.
func hasRemote(srcs []Source) bool {
	for _, s := range srcs {
		if s.Kind == "github" || s.Kind == "url" {
			return true
		}
	}
	return false
}

// sourceSet accumulates resolved sources in resolution order while de-duplicating
// by each ref's canonical() key (the same key used for cycle detection). It is the
// internal collector threaded through resolve; only the resulting []Source escapes.
type sourceSet struct {
	list []Source
	seen map[string]bool
}

func newSourceSet() *sourceSet {
	return &sourceSet{seen: map[string]bool{}}
}

// add records r as a Source the first time its canonical key is seen, classifying
// it from the parsed ref. Repeat refs (already merged elsewhere in the graph) are
// dropped so each layer appears once, in first-resolution order.
func (s *sourceSet) add(r ref) {
	key := r.canonical()
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	s.list = append(s.list, sourceOf(r))
}

// sourceOf classifies a parsed ref into its public Source representation.
func sourceOf(r ref) Source {
	switch r.kind {
	case refGitHub:
		return Source{
			Ref:      r.raw,
			Kind:     "github",
			URL:      r.fetchURL(),
			Pinned:   r.gref != "",
			Unpinned: r.gref == "",
		}
	case refURL:
		return Source{Ref: r.raw, Kind: "url", URL: r.raw}
	default:
		return Source{Ref: r.raw, Kind: "local"}
	}
}
