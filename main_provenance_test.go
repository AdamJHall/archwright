package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AdamJHall/archwright/internal/configsrc"
)

// TestRenderConfig_RemoteProvenanceHeader exercises the remote path: a raw URL
// ref (Kind=="url") is a remote source, so render output must lead with the
// provenance comment block and still carry the merged YAML body beneath it.
func TestRenderConfig_RemoteProvenanceHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cfg.yaml" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte(baseConfig))
	}))
	defer srv.Close()

	ref := srv.URL + "/cfg.yaml"

	var out bytes.Buffer
	opts := configsrc.Options{CacheDir: t.TempDir(), HTTPClient: srv.Client()}
	if err := renderConfig([]string{ref}, &out, opts); err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	body := out.String()
	if !strings.HasPrefix(body, "# Flattened by archwright from:") {
		t.Errorf("remote render missing provenance header; output:\n%s", body)
	}
	// The original ref must be named in the header.
	if !strings.Contains(body, ref) {
		t.Errorf("provenance header does not mention the source ref %q:\n%s", ref, body)
	}
	// The merged YAML body must still be present below the header.
	if !strings.Contains(body, "hostname: arch-box") {
		t.Errorf("merged config body missing from remote render:\n%s", body)
	}
}

// TestRenderConfig_LocalNoProvenanceHeader guards the "" rule: a pure-local
// render must emit no leading provenance comment.
func TestRenderConfig_LocalNoProvenanceHeader(t *testing.T) {
	dir := t.TempDir()
	local := writeFile(t, dir, "config.yaml", baseConfig)

	var out bytes.Buffer
	if err := renderConfig([]string{local}, &out, configsrc.Options{}); err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	if strings.HasPrefix(out.String(), "#") {
		t.Errorf("local-only render should have no provenance header, got:\n%s", out.String())
	}
}
