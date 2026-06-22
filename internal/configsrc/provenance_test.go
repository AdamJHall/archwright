package configsrc

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvenanceComment(t *testing.T) {
	tests := []struct {
		name string
		srcs []Source
		want string // "" means expect the empty string exactly
		// when want is non-empty we assert each line is present instead of an
		// exact match, so the format can be tuned without churning the test.
		wantLines  []string
		wantAbsent []string
	}{
		{
			name: "all local returns empty",
			srcs: []Source{
				{Ref: "config.yaml", Kind: "local"},
				{Ref: "base.yaml", Kind: "local"},
			},
			want: "",
		},
		{
			name: "no sources returns empty",
			srcs: nil,
			want: "",
		},
		{
			name: "mix of local github pinned unpinned url",
			srcs: []Source{
				{Ref: "config.yaml", Kind: "local"},
				{
					Ref:    "github.com/O/R/base.yaml@v1",
					Kind:   "github",
					URL:    "https://raw.githubusercontent.com/O/R/v1/base.yaml",
					Pinned: true,
				},
				{
					Ref:      "github.com/O/R/kde.yaml",
					Kind:     "github",
					URL:      "https://raw.githubusercontent.com/O/R/HEAD/kde.yaml",
					Unpinned: true,
				},
				{
					Ref:  "https://example.com/x.yaml",
					Kind: "url",
					URL:  "https://example.com/x.yaml",
				},
			},
			wantLines: []string{
				"# Flattened by archwright from:",
				"config.yaml",
				"github.com/O/R/base.yaml@v1",
				"https://raw.githubusercontent.com/O/R/v1/base.yaml",
				"github.com/O/R/kde.yaml",
				"UNPINNED",
				"https://raw.githubusercontent.com/O/R/HEAD/kde.yaml",
				"https://example.com/x.yaml",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProvenanceComment(tt.srcs)

			if tt.want == "" && len(tt.wantLines) == 0 {
				if got != "" {
					t.Errorf("ProvenanceComment() = %q, want empty", got)
				}
				return
			}

			if !strings.HasSuffix(got, "\n") {
				t.Errorf("ProvenanceComment() must end with newline, got %q", got)
			}
			for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
				if line != "" && !strings.HasPrefix(line, "#") {
					t.Errorf("every line must be a comment, got %q", line)
				}
			}
			for _, want := range tt.wantLines {
				if !strings.Contains(got, want) {
					t.Errorf("ProvenanceComment() missing %q in:\n%s", want, got)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("ProvenanceComment() should not contain %q in:\n%s", absent, got)
				}
			}
		})
	}
}

func TestLoadSources(t *testing.T) {
	// A fake github host serving base.yaml (no imports) and kde.yaml (no imports).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/O/R/v1/base.yaml":
			_, _ = w.Write([]byte(minimalConfig))
		case "/O/R/HEAD/kde.yaml":
			_, _ = w.Write([]byte("packages:\n  - plasma\n"))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	old := githubRawBase
	githubRawBase = srv.URL
	defer func() { githubRawBase = old }()

	dir := t.TempDir()
	// Local entry imports a pinned github base and an unpinned github kde, and a
	// local sibling that itself re-imports the pinned base (to exercise dedup).
	writeFile(t, dir, "extra.yaml", `
imports:
  - github.com/O/R/base.yaml@v1
packages:
  - extra-pkg
`)
	entry := writeFile(t, dir, "config.yaml", `
imports:
  - github.com/O/R/base.yaml@v1
  - github.com/O/R/kde.yaml
  - extra.yaml
system:
  hostname: top
`)

	_, _, srcs, err := Load([]string{entry}, Options{CacheDir: t.TempDir(), HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Expected resolution order, base-first depth-first, deduped by canonical key:
	//   1. github base (pinned)        <- first import of entry
	//   2. github kde   (unpinned)     <- second import of entry
	//   3. extra.yaml (local)          <- third import; its own import of the
	//                                     github base is a dup and dropped
	//   4. config.yaml (local)         <- the entry itself, resolved last
	wantRefs := []string{
		"github.com/O/R/base.yaml@v1",
		"github.com/O/R/kde.yaml",
		filepath.Join(dir, "extra.yaml"),
		entry,
	}
	if len(srcs) != len(wantRefs) {
		t.Fatalf("got %d sources, want %d:\n%+v", len(srcs), len(wantRefs), srcs)
	}
	for i, want := range wantRefs {
		if srcs[i].Ref != want {
			t.Errorf("srcs[%d].Ref = %q, want %q", i, srcs[i].Ref, want)
		}
	}

	// Classification + trust flags.
	base := srcs[0]
	if base.Kind != "github" || !base.Pinned || base.Unpinned {
		t.Errorf("base source = %+v, want github pinned", base)
	}
	if base.URL != srv.URL+"/O/R/v1/base.yaml" {
		t.Errorf("base URL = %q, want resolved github raw url", base.URL)
	}
	kde := srcs[1]
	if kde.Kind != "github" || kde.Pinned || !kde.Unpinned {
		t.Errorf("kde source = %+v, want github UNPINNED", kde)
	}
	if kde.URL != srv.URL+"/O/R/HEAD/kde.yaml" {
		t.Errorf("kde URL = %q, want HEAD-resolved url", kde.URL)
	}
	if extra := srcs[2]; extra.Kind != "local" || extra.URL != "" {
		t.Errorf("extra source = %+v, want local with empty URL", extra)
	}
	if top := srcs[3]; top.Kind != "local" || top.URL != "" {
		t.Errorf("top source = %+v, want local with empty URL", top)
	}
}
