package configsrc

import (
	"testing"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		strict  bool
		wantErr bool
		want    ref
	}{
		{
			name: "plain filename is local",
			ref:  "config.yaml",
			want: ref{kind: refLocal, raw: "config.yaml"},
		},
		{
			name: "dot-slash relative is local",
			ref:  "./desktop.yaml",
			want: ref{kind: refLocal, raw: "./desktop.yaml"},
		},
		{
			name: "absolute path is local",
			ref:  "/etc/archwright/config.yaml",
			want: ref{kind: refLocal, raw: "/etc/archwright/config.yaml"},
		},
		{
			name: "github shorthand without ref",
			ref:  "github.com/AdamJHall/dotfiles/archwright.yaml",
			want: ref{
				kind:  refGitHub,
				raw:   "github.com/AdamJHall/dotfiles/archwright.yaml",
				owner: "AdamJHall",
				repo:  "dotfiles",
				path:  "archwright.yaml",
				gref:  "",
			},
		},
		{
			name: "github shorthand with ref",
			ref:  "github.com/AdamJHall/dotfiles/sub/archwright.yaml@v1.2.3",
			want: ref{
				kind:  refGitHub,
				raw:   "github.com/AdamJHall/dotfiles/sub/archwright.yaml@v1.2.3",
				owner: "AdamJHall",
				repo:  "dotfiles",
				path:  "sub/archwright.yaml",
				gref:  "v1.2.3",
			},
		},
		{
			name: "raw https url",
			ref:  "https://example.com/teams/shared.yaml",
			want: ref{kind: refURL, raw: "https://example.com/teams/shared.yaml"},
		},
		{
			name: "http url",
			ref:  "http://example.com/shared.yaml",
			want: ref{kind: refURL, raw: "http://example.com/shared.yaml"},
		},
		{
			name:    "strict rejects unpinned github",
			ref:     "github.com/AdamJHall/dotfiles/archwright.yaml",
			strict:  true,
			wantErr: true,
		},
		{
			name:   "strict allows pinned github",
			ref:    "github.com/AdamJHall/dotfiles/archwright.yaml@abc123",
			strict: true,
			want: ref{
				kind:  refGitHub,
				raw:   "github.com/AdamJHall/dotfiles/archwright.yaml@abc123",
				owner: "AdamJHall",
				repo:  "dotfiles",
				path:  "archwright.yaml",
				gref:  "abc123",
			},
		},
		{
			name:    "github shorthand missing path is an error",
			ref:     "github.com/AdamJHall/dotfiles",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRef(tt.ref, tt.strict)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseRef(%q) expected error, got %+v", tt.ref, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRef(%q) error = %v", tt.ref, err)
			}
			if got != tt.want {
				t.Errorf("parseRef(%q) = %+v, want %+v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestGitHubRawURL(t *testing.T) {
	tests := []struct {
		name string
		ref  ref
		want string
	}{
		{
			name: "unpinned uses HEAD",
			ref:  ref{kind: refGitHub, owner: "o", repo: "r", path: "a/b.yaml", gref: ""},
			want: "https://raw.githubusercontent.com/o/r/HEAD/a/b.yaml",
		},
		{
			name: "pinned uses ref",
			ref:  ref{kind: refGitHub, owner: "o", repo: "r", path: "b.yaml", gref: "v1"},
			want: "https://raw.githubusercontent.com/o/r/v1/b.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.rawURL(githubRawBase); got != tt.want {
				t.Errorf("rawURL = %q, want %q", got, tt.want)
			}
		})
	}
}
