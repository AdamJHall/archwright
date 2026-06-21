package stages

import (
	"sort"
	"testing"
)

// names extracts stage names in order for easy assertions.
func names(ss []Stage) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name()
	}
	return out
}

func contains(ss []Stage, name string) bool {
	for _, s := range ss {
		if s.Name() == name {
			return true
		}
	}
	return false
}

func TestSelect_OnlyWins(t *testing.T) {
	got := names(Select(Bootstrap, "kde", nil, nil))
	if len(got) != 1 || got[0] != "kde" {
		t.Fatalf("Select only=kde = %v, want [kde]", got)
	}
}

func TestSelect_OnlyOverridesDisable(t *testing.T) {
	got := names(Select(Bootstrap, "kde", nil, []string{"kde"}))
	if len(got) != 1 || got[0] != "kde" {
		t.Fatalf("Select only=kde disable=[kde] = %v, want [kde]", got)
	}
}

func TestSelect_Skip(t *testing.T) {
	got := Select(Bootstrap, "", []string{"kde"}, nil)
	all := For(Bootstrap, "")
	if len(got) != len(all)-1 {
		t.Fatalf("skip=[kde] returned %d stages, want %d", len(got), len(all)-1)
	}
	if contains(got, "kde") {
		t.Errorf("skip=[kde] still contains kde: %v", names(got))
	}
}

func TestSelect_DisableByNameAndNumber(t *testing.T) {
	// Disable by name excludes both kde and flatpak.
	got := Select(Bootstrap, "", nil, []string{"kde", "flatpak"})
	if contains(got, "kde") || contains(got, "flatpak") {
		t.Errorf("disable=[kde flatpak] still contains one of them: %v", names(got))
	}

	// Disable by order number: flatpak is order 30.
	got = Select(Bootstrap, "", nil, []string{"30"})
	if contains(got, "flatpak") {
		t.Errorf("disable=[30] still contains flatpak (order 30): %v", names(got))
	}
	all := For(Bootstrap, "")
	if len(got) != len(all)-1 {
		t.Fatalf("disable=[30] returned %d stages, want %d", len(got), len(all)-1)
	}
}

func TestAll_SortedAcrossPhases(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("All() returned no stages")
	}

	// Must include both phases.
	var hasInstall, hasBootstrap bool
	for _, s := range all {
		switch s.Phase() {
		case Install:
			hasInstall = true
		case Bootstrap:
			hasBootstrap = true
		}
	}
	if !hasInstall || !hasBootstrap {
		t.Fatalf("All() missing a phase: install=%v bootstrap=%v", hasInstall, hasBootstrap)
	}

	// Must be sorted by phase then order.
	sorted := sort.SliceIsSorted(all, func(i, j int) bool {
		if all[i].Phase() != all[j].Phase() {
			return all[i].Phase() < all[j].Phase()
		}
		return all[i].Order() < all[j].Order()
	})
	if !sorted {
		t.Errorf("All() not sorted by phase then order: %v", names(all))
	}
}
