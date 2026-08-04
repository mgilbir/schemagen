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
		// An abandoned GOCACHE is not an empty directory -- it is a full one whose
		// contents have all gone cold. Giving the abandoned cases a bucket as old
		// as their root is what stops the fix for the live case from degenerating
		// into "never delete anything": a sweep that looked only at the newest
		// child would still have to answer three hours here.
		when := time.Now().Add(-age)
		if age > 0 {
			bucket := filepath.Join(p, "3f")
			if err := os.Mkdir(bucket, 0o755); err != nil {
				t.Fatalf("mkdir %s bucket: %v", name, err)
			}
			if err := os.Chtimes(bucket, when, when); err != nil {
				t.Fatalf("chtimes %s bucket: %v", name, err)
			}
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	// The control that matters, and the one a hand-stamped directory cannot be.
	//
	// A real GOCACHE does not look like "schemagen-gocache-live" above. Go creates
	// its 256 buckets and trim.txt on first use and writes every entry inside
	// them, so the root's mtime is stamped once at creation and never moves again
	// -- a cache worked continuously for three hours has a three-hour-old root and
	// a five-second-old bucket. Judging the root alone therefore deletes exactly
	// the long run this sweep exists to stop being killed, and the deletion is
	// silent: the robbed process recompiles from nothing rather than failing.
	//
	// So this directory is shaped like the real thing -- old root, fresh child --
	// and it must survive.
	longRun := filepath.Join(tmp, "schemagen-gocache-long-running")
	if err := os.Mkdir(longRun, 0o755); err != nil {
		t.Fatalf("mkdir long-running: %v", err)
	}
	bucket := filepath.Join(longRun, "a7")
	if err := os.Mkdir(bucket, 0o755); err != nil {
		t.Fatalf("mkdir bucket: %v", err)
	}
	rootWhen := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(longRun, rootWhen, rootWhen); err != nil {
		t.Fatalf("chtimes long-running root: %v", err)
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
	kept := []string{
		"schemagen-gocache-live", "schemagen-cogen-live", "schemagen-gocache-future",
		"schemagen-gocache-long-running", "schemagen-something-else",
	}

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
