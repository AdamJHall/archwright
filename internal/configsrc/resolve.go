package configsrc

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/AdamJHall/archwright/internal/config"
	"gopkg.in/yaml.v3"
)

// maxDepth caps import recursion so a pathological (or just deeply nested) graph
// fails with a clear error instead of exhausting the stack.
const maxDepth = 32

// importsKey is the resolver-level top-level key consumed and stripped before the
// final decode. It is NOT a config.Config field.
const importsKey = "imports"

// Options controls how config references are resolved and merged.
type Options struct {
	Offline    bool         // use cache only; never hit the network
	Strict     bool         // refuse unpinned github refs (no @ref): error
	Token      string       // bearer token for private repos (Authorization: Bearer)
	CacheDir   string       // defaults to $XDG_CACHE_HOME/archwright (or ~/.cache/archwright)
	HTTPClient *http.Client // injectable for tests; nil -> http.DefaultClient
}

// cacheDir resolves the cache directory, applying the XDG default when unset.
func (o Options) cacheDir() string {
	if o.CacheDir != "" {
		return o.CacheDir
	}
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "archwright")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Last resort: a relative dir under CWD. Better than panicking; remote
		// fetches still work, they just cache locally.
		return filepath.Join(".archwright-cache")
	}
	return filepath.Join(home, ".cache", "archwright")
}

// client returns the HTTP client to use, defaulting to http.DefaultClient.
func (o Options) client() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return http.DefaultClient
}

// Load resolves one or more top-level refs (the repeatable --config: each a local
// path, github shorthand, or raw URL), recursively resolves each file's imports,
// expands ${VAR}, deep-merges everything (base-first, later/importer wins), then
// decodes and returns the resulting *config.Config together with the flattened
// YAML bytes (no `imports:` key) suitable for staging into Phase B.
func Load(refs []string, opts Options) (*config.Config, []byte, error) {
	if len(refs) == 0 {
		return nil, nil, fmt.Errorf("no config refs given")
	}

	merged := map[string]any{}
	for _, raw := range refs {
		r, err := parseRef(raw, opts.Strict)
		if err != nil {
			return nil, nil, err
		}
		// Each top-level ref is its own resolution graph; later top-level refs
		// (later --config) win, mirroring importer-wins precedence.
		visited := map[string]bool{}
		layer, err := resolve(r, opts, visited, 0)
		if err != nil {
			return nil, nil, err
		}
		merged, err = Merge(merged, layer)
		if err != nil {
			return nil, nil, err
		}
	}

	flat, err := yaml.Marshal(merged)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling merged config: %w", err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(flat, &cfg); err != nil {
		return nil, nil, fmt.Errorf("decoding merged config: %w", err)
	}
	return &cfg, flat, nil
}

// resolve fetches r, expands env, recursively resolves its imports (depth-first,
// base-first), and returns the merged map for this subtree with `imports:`
// stripped. visited holds the canonical refs on the current path for cycle
// detection.
func resolve(r ref, opts Options, visited map[string]bool, depth int) (map[string]any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("import depth exceeded %d at %s", maxDepth, r.raw)
	}
	key := r.canonical()
	if visited[key] {
		return nil, fmt.Errorf("import cycle detected at %s", r.raw)
	}
	visited[key] = true
	defer delete(visited, key)

	data, err := r.fetch(opts, opts.client())
	if err != nil {
		return nil, err
	}

	// Parse as a Node so we can see (and honor/strip) !replace tags, then expand
	// env with the exact same semantics as config.Load before converting to a
	// generic map for merging.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", r.raw, err)
	}
	if isEmptyNode(&root) {
		return map[string]any{}, nil
	}
	if err := config.ExpandEnvNode(&root); err != nil {
		return nil, fmt.Errorf("%s: %w", r.raw, err)
	}

	doc := documentMapping(&root)
	if doc == nil {
		return nil, fmt.Errorf("%s: top-level YAML must be a mapping", r.raw)
	}

	// Pull out imports (a resolver-level key) before converting the rest.
	importRefs, err := extractImports(doc, r, opts.Strict)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.raw, err)
	}

	// Convert this file's own keys (imports already removed) to a generic value,
	// honoring !replace tags via replaceMarker wrappers.
	own, err := nodeToValue(doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.raw, err)
	}
	ownMap, _ := own.(map[string]any)
	if ownMap == nil {
		ownMap = map[string]any{}
	}

	// Base-first: merge each import (in order, later wins) then this file last.
	acc := map[string]any{}
	for _, ir := range importRefs {
		layer, err := resolve(ir, opts, visited, depth+1)
		if err != nil {
			return nil, err
		}
		acc, err = Merge(acc, layer)
		if err != nil {
			return nil, err
		}
	}
	acc, err = Merge(acc, ownMap)
	if err != nil {
		return nil, err
	}
	return acc, nil
}

// extractImports reads and removes the top-level `imports:` key from doc (a
// mapping node), resolving each entry against the importing ref's location.
func extractImports(doc *yaml.Node, parent ref, strict bool) ([]ref, error) {
	idx := -1
	for i := 0; i < len(doc.Content); i += 2 {
		if doc.Content[i].Value == importsKey {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, nil
	}
	val := doc.Content[idx+1]
	// Remove the key/value pair from the mapping so it never reaches the decode.
	doc.Content = append(doc.Content[:idx], doc.Content[idx+2:]...)

	if val.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("imports must be a list of refs")
	}
	out := make([]ref, 0, len(val.Content))
	for _, item := range val.Content {
		if item.Kind != yaml.ScalarNode || item.Value == "" {
			return nil, fmt.Errorf("imports entries must be non-empty strings")
		}
		ir, err := parent.sibling(item.Value, strict)
		if err != nil {
			return nil, err
		}
		out = append(out, ir)
	}
	return out, nil
}

// documentMapping returns the top-level mapping node inside a document node, or
// nil if the document's root is not a mapping.
func documentMapping(root *yaml.Node) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// isEmptyNode reports whether a parsed document is effectively empty (no content
// — e.g. an empty or comment-only file).
func isEmptyNode(root *yaml.Node) bool {
	if root.Kind == yaml.DocumentNode {
		return len(root.Content) == 0
	}
	return root.Kind == 0
}

// nodeToValue converts a yaml.Node tree into the generic map[string]any /
// []any / scalar representation used by the merge engine, wrapping any node
// tagged `!replace` in a replaceMarker (with the tag's effect captured, the
// inner value is converted normally).
func nodeToValue(n *yaml.Node) (any, error) {
	// Honor the !replace escape hatch: wrap the converted value in a marker so
	// Merge discards whatever it inherits.
	if n.Tag == "!replace" {
		inner := *n
		inner.Tag = "" // strip so the inner conversion is normal
		v, err := nodeToValue(&inner)
		if err != nil {
			return nil, err
		}
		return replaceMarker{value: v}, nil
	}

	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return nodeToValue(n.Content[0])
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i].Value
			v, err := nodeToValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[key] = v
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := nodeToValue(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		return scalarValue(n)
	case yaml.AliasNode:
		return nodeToValue(n.Alias)
	default:
		return nil, fmt.Errorf("unsupported YAML node kind %d", n.Kind)
	}
}

// scalarValue decodes a scalar node to its native Go type (string, int, bool,
// float, nil) so merged maps round-trip through yaml.Marshal unchanged.
func scalarValue(n *yaml.Node) (any, error) {
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, fmt.Errorf("decoding scalar %q: %w", n.Value, err)
	}
	return v, nil
}
