package configsrc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
)

// minimalConfig is a small but valid-enough config body shared by fixtures. It
// decodes cleanly into config.Config (Validate is the caller's job, not Load's).
const minimalConfig = `
system:
  hostname: base-box
  timezone: UTC
  locale: en_AU.UTF-8
  keymap: us
user:
  name: adam
pacstrap:
  - base
kernel:
  base:
    - linux
packages:
  - vim
`

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return p
}

func TestLoadLocalSingleFileCompat(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "config.yaml", minimalConfig)

	// configsrc.Load on a single local file with no imports must match
	// config.Load exactly (same decode, same env-expansion semantics).
	wantCfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	gotCfg, _, _, err := Load([]string{p}, Options{})
	if err != nil {
		t.Fatalf("configsrc.Load: %v", err)
	}
	if gotCfg.System.Hostname != wantCfg.System.Hostname ||
		gotCfg.User.Name != wantCfg.User.Name ||
		len(gotCfg.Packages) != len(wantCfg.Packages) {
		t.Errorf("configsrc.Load mismatch:\n got %+v\nwant %+v", gotCfg, wantCfg)
	}
}

func TestLoadEnvExpansionMatchesConfig(t *testing.T) {
	dir := t.TempDir()
	body := `
system:
  hostname: ${HN}
  timezone: UTC
  locale: en_AU.UTF-8
  keymap: us
user:
  name: adam
pacstrap:
  - base
kernel:
  base:
    - linux
`
	p := writeFile(t, dir, "config.yaml", body)
	t.Setenv("HN", "expanded-host")

	cfg, _, _, err := Load([]string{p}, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.System.Hostname != "expanded-host" {
		t.Errorf("hostname = %q, want expanded-host", cfg.System.Hostname)
	}

	// Unset variable is an error, matching config.expandEnv.
	body2 := strings.Replace(body, "${HN}", "${UNSET_VAR_XYZ}", 1)
	p2 := writeFile(t, dir, "bad.yaml", body2)
	if _, _, _, err := Load([]string{p2}, Options{}); err == nil {
		t.Errorf("expected error for unset env var")
	}
}

func TestLoadRelativeImportsSiblings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", minimalConfig)
	// Entry point imports a sibling by bare filename; importer wins.
	entry := `
imports:
  - base.yaml
system:
  hostname: desktop-box
packages:
  - steam
`
	p := writeFile(t, dir, "desktop.yaml", entry)

	cfg, flat, _, err := Load([]string{p}, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.System.Hostname != "desktop-box" {
		t.Errorf("hostname = %q, want desktop-box (importer wins)", cfg.System.Hostname)
	}
	if cfg.System.Timezone != "UTC" {
		t.Errorf("timezone = %q, want UTC (inherited from base)", cfg.System.Timezone)
	}
	// packages union+dedup: base vim + entry steam.
	if len(cfg.Packages) != 2 || cfg.Packages[0] != "vim" || cfg.Packages[1] != "steam" {
		t.Errorf("packages = %v, want [vim steam]", cfg.Packages)
	}
	if strings.Contains(string(flat), "imports:") {
		t.Errorf("flattened bytes still contain imports key:\n%s", flat)
	}
}

func TestLoadMultiLayerPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", minimalConfig)
	writeFile(t, dir, "kde.yaml", `
system:
  hostname: kde-host
packages:
  - plasma
`)
	entry := `
imports:
  - base.yaml
  - kde.yaml
packages:
  - steam
`
	p := writeFile(t, dir, "desktop.yaml", entry)

	cfg, _, _, err := Load([]string{p}, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// later import (kde) overrides earlier (base) for hostname.
	if cfg.System.Hostname != "kde-host" {
		t.Errorf("hostname = %q, want kde-host (later import wins)", cfg.System.Hostname)
	}
	// union across all layers: base vim, kde plasma, entry steam.
	want := []string{"vim", "plasma", "steam"}
	if len(cfg.Packages) != len(want) {
		t.Fatalf("packages = %v, want %v", cfg.Packages, want)
	}
	for i, w := range want {
		if cfg.Packages[i] != w {
			t.Errorf("packages[%d] = %q, want %q (full=%v)", i, cfg.Packages[i], w, cfg.Packages)
		}
	}
}

func TestLoadMultipleTopLevelRefsLastWins(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.yaml", minimalConfig)
	b := writeFile(t, dir, "b.yaml", `
system:
  hostname: from-b
packages:
  - git
`)
	cfg, _, _, err := Load([]string{a, b}, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.System.Hostname != "from-b" {
		t.Errorf("hostname = %q, want from-b (last --config wins)", cfg.System.Hostname)
	}
	if len(cfg.Packages) != 2 {
		t.Errorf("packages = %v, want union of a+b", cfg.Packages)
	}
}

func TestLoadNestedImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "root.yaml", minimalConfig)
	writeFile(t, dir, "mid.yaml", `
imports:
  - root.yaml
system:
  hostname: mid-host
`)
	p := writeFile(t, dir, "top.yaml", `
imports:
  - mid.yaml
packages:
  - top-pkg
`)
	cfg, _, _, err := Load([]string{p}, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.System.Hostname != "mid-host" {
		t.Errorf("hostname = %q, want mid-host", cfg.System.Hostname)
	}
	if len(cfg.Packages) != 2 {
		t.Errorf("packages = %v, want [vim top-pkg]", cfg.Packages)
	}
}

func TestLoadCycleDetection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "imports:\n  - b.yaml\n")
	writeFile(t, dir, "b.yaml", "imports:\n  - a.yaml\n")
	p := filepath.Join(dir, "a.yaml")

	_, _, _, err := Load([]string{p}, Options{})
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q should mention cycle", err)
	}
}

func TestLoadDepthCap(t *testing.T) {
	dir := t.TempDir()
	// Build a chain longer than the depth cap, each importing the next.
	const n = maxDepth + 5
	for i := 0; i < n; i++ {
		body := ""
		if i < n-1 {
			body = "imports:\n  - f" + itoa(i+1) + ".yaml\n"
		} else {
			body = minimalConfig
		}
		writeFile(t, dir, "f"+itoa(i)+".yaml", body)
	}
	_, _, _, err := Load([]string{filepath.Join(dir, "f0.yaml")}, Options{})
	if err == nil {
		t.Fatalf("expected depth-cap error")
	}
	if !strings.Contains(err.Error(), "depth") {
		t.Errorf("error %q should mention depth", err)
	}
}

func TestLoadReplaceTag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", minimalConfig) // packages: [vim]
	p := writeFile(t, dir, "top.yaml", `
imports:
  - base.yaml
packages: !replace
  - steam
`)
	cfg, _, _, err := Load([]string{p}, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Packages) != 1 || cfg.Packages[0] != "steam" {
		t.Errorf("packages = %v, want [steam] (replace tag drops inherited vim)", cfg.Packages)
	}
}

func TestLoadRawURLAndCache(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(minimalConfig))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	url := srv.URL + "/shared.yaml"

	cfg, _, _, err := Load([]string{url}, Options{CacheDir: cacheDir, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.System.Hostname != "base-box" {
		t.Errorf("hostname = %q, want base-box", cfg.System.Hostname)
	}
	if hits != 1 {
		t.Fatalf("server hits = %d, want 1", hits)
	}

	// Second load should be served from cache (no new hit).
	if _, _, _, err := Load([]string{url}, Options{CacheDir: cacheDir, HTTPClient: srv.Client()}); err != nil {
		t.Fatalf("Load (cached): %v", err)
	}
	if hits != 1 {
		t.Errorf("server hits = %d after cached load, want 1", hits)
	}

	// Offline with a populated cache succeeds...
	if _, _, _, err := Load([]string{url}, Options{CacheDir: cacheDir, Offline: true, HTTPClient: srv.Client()}); err != nil {
		t.Errorf("offline cached Load: %v", err)
	}
	// ...but offline with an empty cache fails.
	if _, _, _, err := Load([]string{url}, Options{CacheDir: t.TempDir(), Offline: true, HTTPClient: srv.Client()}); err == nil {
		t.Errorf("expected offline cache-miss error")
	}
}

func TestLoadGitHubShorthandViaTestServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expect raw.githubusercontent-style path: /OWNER/REPO/REF/path.
		if !strings.HasPrefix(r.URL.Path, "/AdamJHall/dotfiles/v1/") {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(minimalConfig))
	}))
	defer srv.Close()

	old := githubRawBase
	githubRawBase = srv.URL
	defer func() { githubRawBase = old }()

	ref := "github.com/AdamJHall/dotfiles/archwright.yaml@v1"
	cfg, _, _, err := Load([]string{ref}, Options{CacheDir: t.TempDir(), HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.System.Hostname != "base-box" {
		t.Errorf("hostname = %q, want base-box", cfg.System.Hostname)
	}
}

func TestLoadStrictUnpinnedGitHub(t *testing.T) {
	_, _, _, err := Load([]string{"github.com/o/r/c.yaml"}, Options{Strict: true})
	if err == nil {
		t.Fatalf("expected strict unpinned error")
	}
	if !strings.Contains(err.Error(), "strict") && !strings.Contains(err.Error(), "unpinned") {
		t.Errorf("error %q should mention strict/unpinned", err)
	}
}

// itoa is a tiny strconv.Itoa stand-in to keep the test imports lean.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
