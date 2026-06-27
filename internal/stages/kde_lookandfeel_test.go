package stages

import (
	"strings"
	"testing"
)

// The kde stage selects the Plasma global theme by writing LookAndFeelPackage into
// kdeglobals — deterministic and session-free, so the plan is asserted directly.
func TestKDE_SetsLookAndFeelPackage(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "kde", "kde:\n  look_and_feel: org.kde.breezedark.desktop\n")
	mustContain(t, plan,
		"kwriteconfig6 --file kdeglobals --group KDE --key LookAndFeelPackage org.kde.breezedark.desktop",
	)
}

// With no look_and_feel configured the stage is a clean no-op: nothing is written.
func TestKDE_NoLookAndFeelIsNoOp(t *testing.T) {
	plan := planForCfg(t, Bootstrap, "kde", "kde: {}\n")
	if j := strings.Join(plan, "\n"); strings.Contains(j, "kwriteconfig6") {
		t.Errorf("kde stage should write nothing when look_and_feel is unset, got plan:\n%s", j)
	}
}
