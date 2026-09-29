package testgo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) { Main(m) }

// TestSweepStaleCachesReclaimsOnlyAbandonedDirectories pins both halves of the
// sweep, because each half can fail silently in its own way.
//
// Sweeping too little is the defect it exists for: Main's cleanup is skipped
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
	//
	// Every prefix the sweep knows appears here, abandoned and live, because the
	// list of prefixes is the whole of what decides whether a directory is ever
	// looked at: the four work-directory prefixes were absent from it for two
	// releases while the reasoning for sweeping them was already written down.
	// What a *real* work directory looks like is the business of
	// tests.TestSweepSparesAWorkDirectoryALiveRunIsUsing; this one is about
	// names. The process-directory prefix is here for the same reason: 252
	// directories under two names no prefix covered were found on the audit
	// machine, 863 MB of them.
	dirs := map[string]time.Duration{
		"schemagen-gocache-abandoned":  3 * time.Hour,
		"schemagen-cogen-abandoned":    3 * time.Hour,
		"schemagen-external-abandoned": 3 * time.Hour,
		"schemagen-rt-abandoned":       3 * time.Hour,
		"schemagen-val-abandoned":      3 * time.Hour,
		"schemagen-bin-abandoned":      3 * time.Hour,
		"schemagen-bin2995765028":      3 * time.Hour, // legacy spelling
		"schemagen-main-test105":       3 * time.Hour, // legacy spelling
		"schemagen-gocache-live":       time.Minute,
		"schemagen-cogen-live":         time.Minute,
		"schemagen-external-live":      time.Minute,
		"schemagen-rt-live":            time.Minute,
		"schemagen-val-live":           time.Minute,
		"schemagen-bin-live":           time.Minute,
		"schemagen-gocache-future":     -time.Hour,
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

	sweepStaleCachesIn(tmp, time.Now().Add(-StaleAge))

	gone := []string{
		"schemagen-gocache-abandoned", "schemagen-cogen-abandoned",
		"schemagen-external-abandoned", "schemagen-rt-abandoned", "schemagen-val-abandoned",
		"schemagen-bin-abandoned", "schemagen-bin2995765028", "schemagen-main-test105",
	}
	kept := []string{
		"schemagen-gocache-live", "schemagen-cogen-live", "schemagen-external-live",
		"schemagen-rt-live", "schemagen-val-live", "schemagen-bin-live", "schemagen-gocache-future",
		"schemagen-gocache-long-running", "schemagen-something-else",
	}

	for _, name := range gone {
		if _, err := os.Stat(filepath.Join(tmp, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep; an abandoned directory of a swept kind is what this reclaims, and leaving it is how the disk fills and how /tmp accumulates debris nobody can attribute", name)
		}
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(tmp, name)); err != nil {
			t.Errorf("%s was deleted; %v", name, err)
		}
	}
	AssertNoDoomedLeftovers(t, tmp)
}

// buildRealCache makes a GOCACHE the only way one is really made: by compiling
// something into it.
//
// A hand-made directory is not a fixture for any of this. Go lays out 256
// bucket directories and trim.txt on first use, stamps them once, and writes
// every entry inside them; the mtimes that result are the thing the sweep
// reads, and they cannot be guessed convincingly. That mistake has already
// shipped a bug here once: a sweep test whose "live" control was a bare
// directory passed while the sweep it was guarding would have deleted every
// long-running cache on the box.
//
// It costs one cold stdlib compile, about three seconds and 35 MB.
func buildRealCache(t *testing.T, dir string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module cachefixture\n\ngo 1.23.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	main := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"cache fixture\") }\n"
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	// Command's environment with GOCACHE pointed elsewhere: exec keeps the last
	// value of a duplicated variable.
	cmd := Command(context.Background(), src, "build", "-o", filepath.Join(src, "bin"), ".")
	cmd.Env = append(cmd.Env, "GOCACHE="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building a real cache into %s: %v\n%s", dir, err, out)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) < 100 {
		t.Fatalf("%s does not look like a Go build cache (%d entries, %v); the fixture is worthless if it is not one", dir, len(entries), err)
	}
}

// TestSweepSparesACacheALiveRunIsHolding is the half of the sweep that mtimes
// alone cannot get right, and it only became reachable once runs shared a cache.
//
// A run that is getting cache hits writes no new entries, so it creates no
// bucket, so the newest mtime the sweep can see -- root or immediate child --
// stops advancing for as long as the hits last. That is exactly the run a
// shared cache produces: the second and every later run on the same generator.
// Judged on mtimes it is indistinguishable from a cache abandoned by a killed
// process an hour ago, and deleting it does not fail anything visibly; the
// robbed run simply recompiles ~27,000 programs from nothing.
//
// So the claim is a lock, and the fixture is a real cache aged to look
// abandoned. The control is the same directory a moment later with nobody
// holding it: if that one does not disappear, this test proves nothing, because
// the age check would have spared it anyway.
func TestSweepSparesACacheALiveRunIsHolding(t *testing.T) {
	if !LockingSupported {
		t.Skip("cache locking is unavailable on this platform; the sweep judges by age alone there")
	}
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "schemagen-gocache-shared-test")
	buildRealCache(t, dir)
	AgeTree(t, dir, 3*time.Hour)

	lock := holdCacheLock(t, dir)
	sweepStaleCachesIn(tmp, time.Now().Add(-StaleAge))
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the sweep deleted a cache a live run was holding (%v); that run now recompiles from nothing and nothing says so", err)
	}
	AssertNoDoomedLeftovers(t, tmp)

	lock.release()
	sweepStaleCachesIn(tmp, time.Now().Add(-StaleAge))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("an abandoned cache of exactly the shape spared above survived the sweep (%v); the lock was not what spared it, and an abandoned cache stays on the volume", err)
	}
	AssertNoDoomedLeftovers(t, tmp)
}

// TestSweepAsksBeforeThisRunClaimsTheCache pins the order inside init, which
// became load-bearing when the work-directory half started reading the run
// lock.
//
// A process that has already taken its own claim sees itself as a live run, so
// anotherRunIsAlive answers true for the whole of that process's sweep and not
// one work directory is ever reclaimed. Nothing fails: a sweep that reclaims
// nothing is indistinguishable from a temp directory with nothing to reclaim,
// which is how this leak went unnoticed for two releases in the first place.
func TestSweepAsksBeforeThisRunClaimsTheCache(t *testing.T) {
	if !LockingSupported {
		t.Skip("nothing can claim a run here, so there is no order to get wrong")
	}
	if !sweptBeforeClaiming {
		t.Errorf("this process held its claim on %s before it swept, so its sweep saw a live run and reclaimed no work directory at all", sharedCacheLockPath())
	}
}

// TestAWorkDirectoryIsSweptUnderItsOwnName holds the doomed name honest.
//
// The rename is what makes a delete atomic to a run arriving mid-sweep, and a
// crash between the two steps leaves the renamed directory behind -- so the
// name it is renamed to has to be a name the sweep still reclaims *and* a name
// that says what it was. Reusing the cache's spelling for a work directory
// would satisfy the first and quietly break the second, which is the exact
// complaint #158 makes: /tmp accumulating debris nobody can attribute.
func TestAWorkDirectoryIsSweptUnderItsOwnName(t *testing.T) {
	for _, prefix := range append(append([]string{}, WorkDirPrefixes...), cacheDirPrefixes...) {
		tmp := t.TempDir()
		dir := filepath.Join(tmp, prefix+"named")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		doomed, moved := renameForRemoval(dir, prefix)
		if !moved {
			t.Fatalf("%s could not be renamed out of the way", dir)
		}
		base := filepath.Base(doomed)
		if !strings.HasPrefix(base, prefix) {
			t.Errorf("a %s directory was renamed to %s, which no longer says what it was", prefix, base)
		}
		gotPrefix, _, ok := sweptDirKind(base)
		switch {
		case !ok:
			t.Errorf("%s is not a name the sweep looks at, so a crash between the rename and the delete leaks it permanently", base)
		case gotPrefix != prefix:
			t.Errorf("%s is reclaimed as a %s directory rather than the %s one it was; the leak is closed and the attribution is not", base, gotPrefix, prefix)
		}
		// And it must actually be reclaimed, not merely recognised.
		AgeTree(t, doomed, 3*time.Hour)
		sweepStaleCachesIn(tmp, time.Now().Add(-StaleAge))
		if _, err := os.Stat(doomed); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep (%v); a directory stranded between the rename and the delete is a permanent leak", base, err)
		}
	}
}

// TestSharedCacheIsClearedByTheLastRunOut pins the other end of the lock:
// sharing bounds what concurrent runs cost, and clearing up on the way out is
// what bounds what consecutive ones cost.
//
// Both halves can fail silently. A run that deletes the cache while another is
// still building into it robs that run exactly as the sweep would. A run that
// never deletes it leaves a cache per distinct generator state in /tmp, which
// is the same volume filling up more slowly -- and an hourly staleness sweep
// never touches a directory that gets used again every half hour.
func TestSharedCacheIsClearedByTheLastRunOut(t *testing.T) {
	if !LockingSupported {
		t.Skip("without flock nothing can claim the cache, so it is left for the age-based sweep")
	}
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "schemagen-gocache-shared-test")
	buildRealCache(t, dir)

	first := holdCacheLock(t, dir)
	second := holdCacheLock(t, dir)

	releaseCacheDir(dir, first, false)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the first run out deleted the cache the second is still building into (%v)", err)
	}

	releaseCacheDir(dir, second, false)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the last run out left the cache behind (%v); nothing else reclaims it while runs keep arriving", err)
	}
	AssertNoDoomedLeftovers(t, tmp)

	// SCHEMAGEN_KEEP_GOCACHE is the deliberate other answer: keep the cache for
	// the next run, warm, and pay for it.
	kept := filepath.Join(tmp, "schemagen-gocache-shared-kept")
	buildRealCache(t, kept)
	releaseCacheDir(kept, holdCacheLock(t, kept), true)
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the cache was deleted although the run asked to keep it (%v)", err)
	}
}

// TestHarnessCompilationsAreSharableBetweenRuns is the property that makes one
// shared directory worth having, and it is not the directory that provides it.
//
// Every group of every run compiles in a temp directory drawn fresh from
// MkdirTemp, and a build records the absolute directory of its source in what
// it produces -- so without -trimpath two runs compiling byte-identical source
// have different action IDs and share not one entry. A shared cache would then
// hold a cache per run in a single directory, which is the same volume filling
// up with one fewer place to look. Measured on the round-trip half of draft3,
// twice over: 745M then 1443M without -trimpath, 249M then 249M with it, and
// the second run 19s against 54s. On the full corpus, two concurrent runs
// peaked at 1.9G together, where two per-process caches reached 34.6G and
// 37.1G.
//
// So this compiles the same program twice from two directories a run would
// never draw twice, and holds the second to adding nothing. The tolerance is
// for the action-index entry each build writes whether it hits or misses; a
// miss costs megabytes, and the ten lines below were 2 MB of them.
func TestHarnessCompilationsAreSharableBetweenRuns(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	build := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module roundtrip_test\n\ngo 1.23.0\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		src := "package main\n\nimport \"fmt\"\n\ntype Root struct {\n\tA string `json:\"a\"`\n\tB int    `json:\"b\"`\n}\n\nfunc (r Root) Validate() error {\n\tif r.A == \"\" {\n\t\treturn fmt.Errorf(\"a empty\")\n\t}\n\treturn nil\n}\n\nfunc main() { fmt.Println(Root{A: \"x\", B: 1}.Validate()) }\n"
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
			t.Fatalf("write main.go: %v", err)
		}
		// Command's own environment -- which is the claim under test: it is
		// what every test's builds get, and it has to be what makes them
		// sharable -- with only GOCACHE pointed at this test's private cache.
		cmd := Command(context.Background(), dir, "run", ".")
		cmd.Env = append(cmd.Env, "GOCACHE="+cache)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compiling in %s: %v\n%s", dir, err, out)
		}
	}

	tmp := t.TempDir()
	build(filepath.Join(tmp, "schemagen-rt-1113344"))
	before := treeBytes(t, cache)
	// The first build must have gone into this cache at all, or "the second
	// added nothing" is true of a cache nothing was ever written to.
	if before < 1<<20 {
		t.Fatalf("the first build put only %d bytes into %s; the GOCACHE override did not reach it, and the comparison below would pass vacuously", before, cache)
	}
	build(filepath.Join(tmp, "schemagen-rt-9927001"))
	after := treeBytes(t, cache)

	const tolerance = 256 << 10
	if after-before > tolerance {
		t.Errorf("the same program compiled in a second directory added %d bytes to the cache; two runs would then need a cache each in one directory, and the shared cache buys nothing", after-before)
	}
}

// treeBytes is the size of everything under dir.
func treeBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return total
}

// TestSharedCacheIsTheOneEveryRunUses is the arithmetic the issue turns on: N
// runs must cost one cache, not N. The path is per user and per nothing else,
// so two processes on a box compute the same one.
func TestSharedCacheIsTheOneEveryRunUses(t *testing.T) {
	if sharedCacheDir != sharedCachePath() {
		t.Errorf("the running process is using %s but a second run would compute %s; each would then have a cache of its own", sharedCacheDir, sharedCachePath())
	}
	if want := fmt.Sprintf("schemagen-gocache-shared-%d", os.Getuid()); filepath.Base(sharedCachePath()) != want {
		t.Errorf("the shared cache is %s, not %s; the uid is what keeps two users on one box from colliding on a directory neither can write", filepath.Base(sharedCachePath()), want)
	}
	// The sweep has to be able to reclaim it once nobody holds it, which it
	// does by name. A shared cache the sweep does not recognise is a permanent
	// leak.
	if !strings.HasPrefix(filepath.Base(sharedCachePath()), "schemagen-gocache-") {
		t.Errorf("%s is not a name sweepStaleCachesIn looks at", sharedCachePath())
	}
	// And the lock must not be, because the sweep would then try to judge a
	// zero-byte file by its mtime.
	if _, err := os.Stat(sharedCacheLockPath()); err != nil {
		t.Fatalf("this process holds no lock file at %s: %v", sharedCacheLockPath(), err)
	}
	info, err := os.Stat(sharedCacheLockPath())
	if err == nil && info.IsDir() {
		t.Errorf("%s is a directory; the sweep would delete the lock other runs are holding", sharedCacheLockPath())
	}
}
