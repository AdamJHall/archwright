package archinstall

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/AdamJHall/archwright/internal/config"
)

// update regenerates the golden files instead of asserting against them:
//
//	go test ./internal/archinstall/ -run TestRenderGolden -update
var update = flag.Bool("update", false, "rewrite testdata/golden/*.json from current output")

// renderCases pairs each config fixture with the device geometry it should be
// rendered against (archinstall sizes are concrete bytes, so the "rest of disk"
// math depends on disk size). One case per representative disk layout — add a
// fixture + geometry here and run with -update to snapshot a new configuration.
var renderCases = []struct {
	name string
	geom Geometry
}{
	{
		name: "single-disk-lvm",
		geom: Geometry{"/dev/nvme0n1": 256 << 30}, // 256 GiB
	},
	{
		name: "multi-disk-lvm",
		geom: Geometry{
			"/dev/nvme0n1": 512 << 30, // 512 GiB
			"/dev/sda":     1 << 40,   // 1 TiB
			"/dev/sdb":     1 << 40,   // 1 TiB
		},
	},
	{
		name: "ext4-sata",
		geom: Geometry{"/dev/sda": 64 << 30}, // 64 GiB
	},
	{
		name: "btrfs-subvols",
		geom: Geometry{"/dev/nvme0n1": 256 << 30}, // 256 GiB
	},
	{
		name: "lvm-zram",
		geom: Geometry{"/dev/nvme0n1": 256 << 30}, // 256 GiB
	},
}

// TestRenderGolden renders every fixture config against fixed geometry and
// compares the archinstall JSON byte-for-byte to a checked-in golden file. obj_id
// values are made deterministic (objid-0001, ...) so the snapshot stays stable
// while still exercising the lvm_pvs cross-references. A failure means the
// rendered archinstall schema changed — eyeball the diff, and if intended,
// rerun with -update.
func TestRenderGolden(t *testing.T) {
	for _, tc := range renderCases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join("testdata", "configs", tc.name+".yaml")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("load %s: %v", cfgPath, err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("fixture %s is invalid: %v", tc.name, err)
			}

			defer setDeterministicObjIDs()()

			c, _, err := Build(cfg, tc.geom, "TESTPASS")
			if err != nil {
				t.Fatalf("Build(%s): %v", tc.name, err)
			}
			got, err := json.MarshalIndent(c, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')

			goldenPath := filepath.Join("testdata", "golden", tc.name+".config.json")
			if *update {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run with -update to create it): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("rendered archinstall config for %s differs from golden.\n"+
					"If this change is intended, rerun: go test ./internal/archinstall/ -run TestRenderGolden -update\n"+
					"--- got ---\n%s\n--- want ---\n%s", tc.name, got, want)
			}
		})
	}
}

// setDeterministicObjIDs swaps newObjID for a sequential counter and returns a
// restore func; cross-references (lvm_pvs -> partition obj_id) stay intact, just
// predictable.
func setDeterministicObjIDs() func() {
	orig := newObjID
	n := 0
	newObjID = func() string {
		n++
		return fmtObjID(n)
	}
	return func() { newObjID = orig }
}

func fmtObjID(n int) string {
	const digits = "0123456789"
	b := []byte("objid-0000")
	for i := len(b) - 1; i >= len("objid-"); i-- {
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b)
}
