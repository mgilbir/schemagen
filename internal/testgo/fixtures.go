package testgo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The pieces of the sweep a test outside this package needs to stand up a
// box's worth of state in a t.TempDir() and drive the sweep over it. The one
// such test today is tests.TestSweepSparesAWorkDirectoryALiveRunIsUsing, which
// has to live beside the generator because its fixture is a real generated
// module; everything that needs no generator is tested in this package.

// SweepIn runs the sweep over tmp as though every directory last active before
// cutoff had been abandoned by then. It is the sweep every Main runs on the way
// in, with the directory and the cutoff supplied.
func SweepIn(tmp string, cutoff time.Time) { sweepStaleCachesIn(tmp, cutoff) }

// HoldRunClaim claims tmp the way a live test binary claims it, by taking the
// shared lock on its run lock file, and returns the release. Separate open
// files are separate flock holders even inside one process, so this is another
// run as far as the kernel is concerned.
func HoldRunClaim(t testing.TB, tmp string) (release func()) {
	t.Helper()
	return holdCacheLock(t, sharedCachePathIn(tmp)).release
}

// holdCacheLock claims a cache the way a run does.
func holdCacheLock(t testing.TB, dir string) *cacheLock {
	t.Helper()
	f, err := os.OpenFile(dir+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	if !flockShared(f) {
		f.Close()
		t.Fatalf("could not take the shared lock on %s.lock", dir)
	}
	return &cacheLock{f: f}
}

// AgeTree stamps a directory's root and every immediate child as untouched for
// the given span -- the two levels cacheLastActive reads.
//
// It is what both kinds of directory the sweep looks at really look like while
// in use. A cache getting nothing but *hits* creates no new bucket and updates
// an entry's mtime at most once an hour, so nothing at this level moves for as
// long as the hits last. A work directory is stamped once, when the harness
// finishes writing the generated module into it, and neither the compile nor
// the run that follows advances the root or any child -- measured over a live
// one at 250ms intervals through a cold `go run`, every sample reporting an age
// equal to its own elapsed time.
func AgeTree(t testing.TB, dir string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), when, when); err != nil {
			t.Fatalf("chtimes %s: %v", e.Name(), err)
		}
	}
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", dir, err)
	}
}

// AssertNoDoomedLeftovers checks that the rename-then-delete left nothing
// behind. A doomed directory that survives is a directory nobody will look for.
//
// It matches the marker anywhere in the name rather than a fixed prefix,
// because the prefix is whatever the directory came in with: a swept cache
// becomes schemagen-gocache-doomed-*, a swept work directory
// schemagen-val-doomed-* and so on. A check written against one spelling would
// stop watching the others the moment they were added.
func AssertNoDoomedLeftovers(t testing.TB, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read %s: %v", tmp, err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "-doomed-") {
			t.Errorf("%s was left behind by a delete that renamed it out of the way first", e.Name())
		}
	}
}
