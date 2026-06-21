package stages

import (
	"strings"
	"testing"
)

// TestWithin exercises the --from/--to post-filter applied after Select.
// It resolves bounds by name OR number (like --only), is inclusive on both
// ends, and composes with the already-narrowed slice it is handed.
func TestWithin(t *testing.T) {
	// The full ordered Bootstrap set, used as the input to most cases.
	all := names(For(Bootstrap, ""))

	tests := []struct {
		name    string
		from    string
		to      string
		want    []string // nil => same as all
		wantErr string   // substring expected in the error, "" => no error
	}{
		{
			name: "no bounds returns input unchanged",
			from: "", to: "",
			want: all,
		},
		{
			name: "from only by name is inclusive lower bound",
			from: "flatpak", to: "",
			want: []string{"flatpak", "aur", "plymouth", "grub-theme", "kde", "dotfiles", "setup"},
		},
		{
			name: "from only by number resolves same as name",
			from: "30", to: "",
			want: []string{"flatpak", "aur", "plymouth", "grub-theme", "kde", "dotfiles", "setup"},
		},
		{
			name: "to only by name is inclusive upper bound",
			from: "", to: "flatpak",
			want: []string{"yay", "packages", "snapper", "flatpak"},
		},
		{
			name: "to only by number resolves same as name",
			from: "", to: "30",
			want: []string{"yay", "packages", "snapper", "flatpak"},
		},
		{
			name: "both bounds inclusive on each end",
			from: "packages", to: "plymouth",
			want: []string{"packages", "snapper", "flatpak", "aur", "plymouth"},
		},
		{
			name: "both bounds mixing name and number",
			from: "20", to: "kde",
			want: []string{"packages", "snapper", "flatpak", "aur", "plymouth", "grub-theme", "kde"},
		},
		{
			name: "single-stage window when from equals to",
			from: "aur", to: "aur",
			want: []string{"aur"},
		},
		{
			name:    "inverted bounds is an error",
			from:    "plymouth",
			to:      "packages",
			wantErr: "from",
		},
		{
			name:    "unknown from stage errors",
			from:    "nope",
			to:      "",
			wantErr: "nope",
		},
		{
			name:    "unknown to stage errors",
			from:    "",
			to:      "nope",
			wantErr: "nope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := For(Bootstrap, "")
			got, err := Within(in, tt.from, tt.to)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Within(from=%q, to=%q) = nil error, want error containing %q", tt.from, tt.to, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Within error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Within(from=%q, to=%q) unexpected error: %v", tt.from, tt.to, err)
			}
			if gotNames := names(got); !equalStrings(gotNames, tt.want) {
				t.Fatalf("Within(from=%q, to=%q) = %v, want %v", tt.from, tt.to, gotNames, tt.want)
			}
		})
	}
}

// TestWithin_ComposesWithSkip confirms the filter operates on whatever Select
// already produced: a skipped stage stays absent even when inside the window.
func TestWithin_ComposesWithSkip(t *testing.T) {
	in := Select(Bootstrap, "", []string{"aur"}, nil)
	got, err := Within(in, "packages", "plymouth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"packages", "snapper", "flatpak", "plymouth"} // aur removed by --skip
	if g := names(got); !equalStrings(g, want) {
		t.Fatalf("Within over skipped input = %v, want %v", g, want)
	}
}

// TestWithin_EmptyInput is a no-op pass-through, never a panic.
func TestWithin_EmptyInput(t *testing.T) {
	got, err := Within(nil, "packages", "plymouth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Within(nil, ...) = %v, want empty", names(got))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
