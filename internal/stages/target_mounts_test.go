package stages

import (
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
)

// TestTargetMounts_Btrfs asserts the post-install mount tree for btrfs uses the
// root subvolume (subvol=@), not the bare partition, and mounts non-root
// subvolumes at their mountpoints (parents before children). Mounting the bare
// partition would expose only the empty top-level subvolume, so /boot and
// /home/<user> staging would fail — this is the regression guarded here.
func TestTargetMounts_Btrfs(t *testing.T) {
	cfg := &config.Config{}
	cfg.Disks.Layout = "btrfs"
	cfg.Disks.Btrfs = &config.BtrfsLayout{
		Subvolumes: []config.Subvol{
			{Name: "@home", Mountpoint: "/home"},
			{Name: "@", Mountpoint: "/"},
		},
	}

	got, err := targetMounts(cfg, "/dev/vda2", "/dev/vda1")
	if err != nil {
		t.Fatalf("targetMounts: %v", err)
	}

	want := []mount{
		{dev: "/dev/vda2", target: "/mnt", opts: []string{"subvol=@"}},
		{dev: "/dev/vda1", target: "/mnt/boot"},
		{dev: "/dev/vda2", target: "/mnt/home", opts: []string{"subvol=@home"}},
	}
	assertMounts(t, want, got)
}

// TestTargetMounts_BtrfsNoRoot errors when no subvolume maps to "/".
func TestTargetMounts_BtrfsNoRoot(t *testing.T) {
	cfg := &config.Config{}
	cfg.Disks.Layout = "btrfs"
	cfg.Disks.Btrfs = &config.BtrfsLayout{
		Subvolumes: []config.Subvol{{Name: "@home", Mountpoint: "/home"}},
	}
	if _, err := targetMounts(cfg, "/dev/vda2", "/dev/vda1"); err == nil {
		t.Fatal("want error when no subvolume is mounted at /, got nil")
	}
}

// TestTargetMounts_Plain leaves the non-btrfs path unchanged: bare root at /mnt,
// ESP at /mnt/boot, no subvol options.
func TestTargetMounts_Plain(t *testing.T) {
	cfg := &config.Config{}
	cfg.Disks.Layout = "plain"

	got, err := targetMounts(cfg, "/dev/vda2", "/dev/vda1")
	if err != nil {
		t.Fatalf("targetMounts: %v", err)
	}
	want := []mount{
		{dev: "/dev/vda2", target: "/mnt"},
		{dev: "/dev/vda1", target: "/mnt/boot"},
	}
	assertMounts(t, want, got)
}

func assertMounts(t *testing.T, want, got []mount) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("mount count = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].dev != want[i].dev || got[i].target != want[i].target {
			t.Errorf("mount[%d] = {%s %s}, want {%s %s}", i, got[i].dev, got[i].target, want[i].dev, want[i].target)
		}
		if len(got[i].opts) != len(want[i].opts) {
			t.Errorf("mount[%d] opts = %v, want %v", i, got[i].opts, want[i].opts)
			continue
		}
		for j := range want[i].opts {
			if got[i].opts[j] != want[i].opts[j] {
				t.Errorf("mount[%d] opts[%d] = %q, want %q", i, j, got[i].opts[j], want[i].opts[j])
			}
		}
	}
}
