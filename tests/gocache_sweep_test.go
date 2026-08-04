package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSweepStaleCachesReclaimsOnlyAbandonedDirectories pins both halves of the
// sweep, because each half can fail silently in its own way.
//
// Sweeping too little is the defect it exists for: TestMain's cleanup is skipped
// whenever the process is killed rather than returning, and every skip strands
// roughly 2G. Sixty-three of them once filled a 394G volume, at which point the
// suite reports "no space left on device" against individual test keys and a
// dead run reads like a set of real validation failures.
//
// Sweeping too much is worse and quieter: deleting a *live* run's cache does not
// fail anything visibly, it just makes a concurrent suite recompile from nothing
// and look mysteriously slow. So the recent directory below is a control, not a
// formality.
func TestSweepStaleCachesReclaimsOnlyAbandonedDirectories(t *testing.T) {
	tmp := t.TempDir()

	// name → how long ago it was last touched; negative means "in the future",
	// which a clock skew between machines sharing /tmp can genuinely produce.
	dirs := map[string]time.Duration{
		"schemagen-gocache-abandoned": 3 * time.Hour,
		"schemagen-cogen-abandoned":   3 * time.Hour,
		"schemagen-gocache-live":      time.Minute,
		"schemagen-cogen-live":        time.Minute,
		"schemagen-gocache-future":    -time.Hour,
	}
	for name, age := range dirs {
		p := filepath.Join(tmp, name)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	// Something that merely looks similar must survive: the sweep runs over the
	// shared temp directory, where anything at all may be sitting.
	unrelated := filepath.Join(tmp, "schemagen-something-else")
	if err := os.Mkdir(unrelated, 0o755); err != nil {
		t.Fatalf("mkdir unrelated: %v", err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(unrelated, old, old); err != nil {
		t.Fatalf("chtimes unrelated: %v", err)
	}

	sweepStaleCachesIn(tmp, time.Now().Add(-staleCacheAge))

	gone := []string{"schemagen-gocache-abandoned", "schemagen-cogen-abandoned"}
	kept := []string{"schemagen-gocache-live", "schemagen-cogen-live", "schemagen-gocache-future", "schemagen-something-else"}

	for _, name := range gone {
		if _, err := os.Stat(filepath.Join(tmp, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep; an abandoned cache is what this reclaims, and leaving it is how the disk fills", name)
		}
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(tmp, name)); err != nil {
			t.Errorf("%s was deleted; %v", name, err)
		}
	}
}
