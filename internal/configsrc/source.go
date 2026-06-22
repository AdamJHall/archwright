package configsrc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// githubRawBase is the host configuration translates github shorthands to. It is
// a package var (not a const) so tests can point it at an httptest server.
var githubRawBase = "https://raw.githubusercontent.com"

// refKind enumerates the three reference forms.
type refKind int

const (
	refLocal  refKind = iota // filesystem path
	refGitHub                // github.com/OWNER/REPO/path[@ref] shorthand
	refURL                   // raw http(s) URL
)

// ref is a parsed config reference. owner/repo/path/gref are only populated for
// refGitHub. raw is the original string (used for cycle keys and diagnostics).
type ref struct {
	kind  refKind
	raw   string
	owner string
	repo  string
	path  string
	gref  string
}

// parseRef classifies ref by shape. In strict mode an unpinned github shorthand
// (no @ref) is rejected. Detection order: raw URL (scheme), github shorthand
// (github.com/ prefix), else local path.
func parseRef(s string, strict bool) (ref, error) {
	switch {
	case strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "http://"):
		return ref{kind: refURL, raw: s}, nil
	case strings.HasPrefix(s, "github.com/"):
		return parseGitHub(s, strict)
	default:
		return ref{kind: refLocal, raw: s}, nil
	}
}

// parseGitHub splits github.com/OWNER/REPO/PATH[@REF] into its parts.
func parseGitHub(s string, strict bool) (ref, error) {
	rest := strings.TrimPrefix(s, "github.com/")
	body, gitref, _ := strings.Cut(rest, "@")

	parts := strings.SplitN(body, "/", 3)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return ref{}, fmt.Errorf("malformed github reference %q: want github.com/OWNER/REPO/path/to.yaml[@ref]", s)
	}
	if strict && gitref == "" {
		return ref{}, fmt.Errorf("refusing unpinned github reference %q in strict mode: add @<tag-or-sha>", s)
	}
	return ref{
		kind:  refGitHub,
		raw:   s,
		owner: parts[0],
		repo:  parts[1],
		path:  parts[2],
		gref:  gitref,
	}, nil
}

// rawURL renders a github shorthand to its raw.githubusercontent URL, using HEAD
// when the ref is unpinned. base is the host (overridable for tests).
func (r ref) rawURL(base string) string {
	gitref := r.gref
	if gitref == "" {
		gitref = "HEAD"
	}
	return fmt.Sprintf("%s/%s/%s/%s/%s", strings.TrimRight(base, "/"), r.owner, r.repo, gitref, r.path)
}

// fetchURL is the URL a ref is fetched from (empty for local refs).
func (r ref) fetchURL() string {
	switch r.kind {
	case refGitHub:
		return r.rawURL(githubRawBase)
	case refURL:
		return r.raw
	default:
		return ""
	}
}

// cacheKey is a stable, filesystem-safe key for a remote ref keyed by URL+ref.
func (r ref) cacheKey() string {
	sum := sha256.Sum256([]byte(r.fetchURL()))
	return hex.EncodeToString(sum[:])
}

// sibling resolves a bare relative import path against this ref's location. A
// github or URL ref makes its siblings github/URL-rooted (same dir, new
// filename); a local ref resolves against the importing file's directory. An
// import that is itself an absolute/github/URL ref is returned parsed as-is.
func (r ref) sibling(imp string, strict bool) (ref, error) {
	// An import that names its own form (absolute path, github, url) is not
	// relative to the importer.
	if strings.HasPrefix(imp, "https://") || strings.HasPrefix(imp, "http://") ||
		strings.HasPrefix(imp, "github.com/") || strings.HasPrefix(imp, "/") {
		return parseRef(imp, strict)
	}

	switch r.kind {
	case refGitHub:
		child := r
		child.path = path.Join(path.Dir(r.path), imp)
		child.raw = fmt.Sprintf("github.com/%s/%s/%s", r.owner, r.repo, child.path)
		if r.gref != "" {
			child.raw += "@" + r.gref
		}
		return child, nil
	case refURL:
		base := r.raw[:strings.LastIndex(r.raw, "/")+1]
		return ref{kind: refURL, raw: base + imp}, nil
	default:
		dir := filepath.Dir(r.raw)
		return ref{kind: refLocal, raw: filepath.Join(dir, imp)}, nil
	}
}

// canonical is the cycle-detection key: the resolved fetch URL for remote refs,
// or the cleaned absolute-ish path for local refs.
func (r ref) canonical() string {
	if u := r.fetchURL(); u != "" {
		return u
	}
	return filepath.Clean(r.raw)
}

// fetch returns the bytes for r. Local files are read from disk and never
// cached. Remote refs are read from cache when present; otherwise fetched over
// HTTP (with an optional bearer token) and written to the cache. In Offline mode
// a remote ref must already be cached or fetch errors.
func (r ref) fetch(opts Options, client *http.Client) ([]byte, error) {
	if r.kind == refLocal {
		data, err := os.ReadFile(r.raw)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", r.raw, err)
		}
		return data, nil
	}

	cachePath := filepath.Join(opts.cacheDir(), r.cacheKey())
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}
	if opts.Offline {
		return nil, fmt.Errorf("offline: %s not in cache (%s)", r.fetchURL(), cachePath)
	}

	data, err := httpGet(r.fetchURL(), opts.Token, client)
	if err != nil {
		return nil, err
	}
	if err := writeCache(cachePath, data); err != nil {
		return nil, err
	}
	return data, nil
}

// httpGet performs the GET, attaching a bearer token when set, and errors on a
// non-2xx status.
func httpGet(url, token string, client *http.Client) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", url, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return body, nil
}

// writeCache atomically writes data to path, creating the parent dir.
func writeCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("finalizing cache: %w", err)
	}
	return nil
}
