package configsrc

import (
	"reflect"
	"testing"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name string
		base map[string]any
		over map[string]any
		want map[string]any
	}{
		{
			name: "scalar override",
			base: map[string]any{"hostname": "old"},
			over: map[string]any{"hostname": "new"},
			want: map[string]any{"hostname": "new"},
		},
		{
			name: "new key appended",
			base: map[string]any{"a": 1},
			over: map[string]any{"b": 2},
			want: map[string]any{"a": 1, "b": 2},
		},
		{
			name: "maps merge recursively",
			base: map[string]any{"system": map[string]any{"hostname": "old", "timezone": "UTC"}},
			over: map[string]any{"system": map[string]any{"hostname": "new"}},
			want: map[string]any{"system": map[string]any{"hostname": "new", "timezone": "UTC"}},
		},
		{
			name: "string slice union and dedup base order first",
			base: map[string]any{"packages": []any{"vim", "git"}},
			over: map[string]any{"packages": []any{"git", "steam"}},
			want: map[string]any{"packages": []any{"vim", "git", "steam"}},
		},
		{
			name: "structured slice key-merged by name overrides element",
			base: map[string]any{"repos": []any{
				map[string]any{"name": "cachyos", "server": "old"},
				map[string]any{"name": "other", "server": "x"},
			}},
			over: map[string]any{"repos": []any{
				map[string]any{"name": "cachyos", "server": "new"},
			}},
			want: map[string]any{"repos": []any{
				map[string]any{"name": "cachyos", "server": "new"},
				map[string]any{"name": "other", "server": "x"},
			}},
		},
		{
			name: "structured slice key-merged appends new name",
			base: map[string]any{"hooks": []any{
				map[string]any{"name": "a", "run": "echo a"},
			}},
			over: map[string]any{"hooks": []any{
				map[string]any{"name": "b", "run": "echo b"},
			}},
			want: map[string]any{"hooks": []any{
				map[string]any{"name": "a", "run": "echo a"},
				map[string]any{"name": "b", "run": "echo b"},
			}},
		},
		{
			name: "structured slice element fields merge recursively",
			base: map[string]any{"hooks": []any{
				map[string]any{"name": "a", "run": "echo a", "root": true},
			}},
			over: map[string]any{"hooks": []any{
				map[string]any{"name": "a", "run": "echo new"},
			}},
			want: map[string]any{"hooks": []any{
				map[string]any{"name": "a", "run": "echo new", "root": true},
			}},
		},
		{
			name: "no-name structured slice replaces wholesale",
			base: map[string]any{"subvolumes": []any{
				map[string]any{"mountpoint": "/"},
				map[string]any{"mountpoint": "/home"},
			}},
			over: map[string]any{"subvolumes": []any{
				map[string]any{"mountpoint": "/"},
			}},
			want: map[string]any{"subvolumes": []any{
				map[string]any{"mountpoint": "/"},
			}},
		},
		{
			name: "over scalar replaces base map",
			base: map[string]any{"x": map[string]any{"a": 1}},
			over: map[string]any{"x": "scalar"},
			want: map[string]any{"x": "scalar"},
		},
		{
			name: "over map replaces base scalar",
			base: map[string]any{"x": "scalar"},
			over: map[string]any{"x": map[string]any{"a": 1}},
			want: map[string]any{"x": map[string]any{"a": 1}},
		},
		{
			name: "nested combination",
			base: map[string]any{
				"system":   map[string]any{"hostname": "base", "locales": []any{"en_AU"}},
				"packages": []any{"vim"},
			},
			over: map[string]any{
				"system":   map[string]any{"locales": []any{"en_AU", "en_US"}},
				"packages": []any{"git"},
			},
			want: map[string]any{
				"system":   map[string]any{"hostname": "base", "locales": []any{"en_AU", "en_US"}},
				"packages": []any{"vim", "git"},
			},
		},
		{
			name: "empty base",
			base: map[string]any{},
			over: map[string]any{"a": 1},
			want: map[string]any{"a": 1},
		},
		{
			name: "empty over keeps base",
			base: map[string]any{"a": 1},
			over: map[string]any{},
			want: map[string]any{"a": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Merge(tt.base, tt.over)
			if err != nil {
				t.Fatalf("Merge() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Merge() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMergeReplaceTag(t *testing.T) {
	// !replace on a value means: ignore the inherited base value, use over's wholesale.
	base := map[string]any{"packages": []any{"vim", "git"}}
	over := map[string]any{"packages": replaceMarker{value: []any{"steam"}}}

	got, err := Merge(base, over)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	want := map[string]any{"packages": []any{"steam"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Merge() = %#v, want %#v", got, want)
	}
}

func TestMergeReplaceTagOnMap(t *testing.T) {
	base := map[string]any{"system": map[string]any{"hostname": "old", "timezone": "UTC"}}
	over := map[string]any{"system": replaceMarker{value: map[string]any{"hostname": "new"}}}

	got, err := Merge(base, over)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	// !replace discards inherited timezone.
	want := map[string]any{"system": map[string]any{"hostname": "new"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Merge() = %#v, want %#v", got, want)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	base := map[string]any{"packages": []any{"vim"}}
	over := map[string]any{"packages": []any{"git"}}
	_, err := Merge(base, over)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if !reflect.DeepEqual(base, map[string]any{"packages": []any{"vim"}}) {
		t.Errorf("Merge mutated base: %#v", base)
	}
	if !reflect.DeepEqual(over, map[string]any{"packages": []any{"git"}}) {
		t.Errorf("Merge mutated over: %#v", over)
	}
}
