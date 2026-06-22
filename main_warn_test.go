package main

import (
	"testing"

	"github.com/AdamJHall/archwright/internal/configsrc"
)

func TestRemoteSources(t *testing.T) {
	tests := []struct {
		name string
		in   []configsrc.Source
		want []string // wanted Refs, in order
	}{
		{
			name: "nil",
			in:   nil,
			want: nil,
		},
		{
			name: "all local",
			in: []configsrc.Source{
				{Ref: "config.yaml", Kind: "local"},
				{Ref: "overlay.yaml", Kind: "local"},
			},
			want: nil,
		},
		{
			name: "github and url are remote",
			in: []configsrc.Source{
				{Ref: "user/repo@v1", Kind: "github"},
				{Ref: "https://x/y.yaml", Kind: "url"},
			},
			want: []string{"user/repo@v1", "https://x/y.yaml"},
		},
		{
			name: "mixed keeps only remote in order",
			in: []configsrc.Source{
				{Ref: "config.yaml", Kind: "local"},
				{Ref: "user/repo", Kind: "github"},
				{Ref: "local2.yaml", Kind: "local"},
				{Ref: "https://x/y.yaml", Kind: "url"},
			},
			want: []string{"user/repo", "https://x/y.yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remoteSources(tt.in)
			assertRefs(t, got, tt.want)
		})
	}
}

func TestUnpinnedSources(t *testing.T) {
	tests := []struct {
		name string
		in   []configsrc.Source
		want []string // wanted Refs, in order
	}{
		{
			name: "nil",
			in:   nil,
			want: nil,
		},
		{
			name: "all local none unpinned",
			in: []configsrc.Source{
				{Ref: "config.yaml", Kind: "local"},
			},
			want: nil,
		},
		{
			name: "pinned github not selected",
			in: []configsrc.Source{
				{Ref: "user/repo@v1", Kind: "github", Pinned: true},
			},
			want: nil,
		},
		{
			name: "unpinned github selected",
			in: []configsrc.Source{
				{Ref: "user/repo", Kind: "github", Unpinned: true},
			},
			want: []string{"user/repo"},
		},
		{
			name: "mix keeps only unpinned in order",
			in: []configsrc.Source{
				{Ref: "config.yaml", Kind: "local"},
				{Ref: "a/b@v1", Kind: "github", Pinned: true},
				{Ref: "c/d", Kind: "github", Unpinned: true},
				{Ref: "https://x/y.yaml", Kind: "url"},
				{Ref: "e/f", Kind: "github", Unpinned: true},
			},
			want: []string{"c/d", "e/f"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unpinnedSources(tt.in)
			assertRefs(t, got, tt.want)
		})
	}
}

func assertRefs(t *testing.T, got []configsrc.Source, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d sources, want %d: %+v", len(got), len(want), got)
	}
	for i, s := range got {
		if s.Ref != want[i] {
			t.Errorf("source[%d].Ref = %q, want %q", i, s.Ref, want[i])
		}
	}
}
