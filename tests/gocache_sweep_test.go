package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/schema"
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
	//
	// Every prefix the sweep knows appears here, abandoned and live, because the
	// list of prefixes is the whole of what decides whether a directory is ever
	// looked at: the four work-directory prefixes were absent from it for two
	// releases while the reasoning for sweeping them was already written down.
	// What a *real* work directory looks like is the business of
	// TestSweepSparesAWorkDirectoryALiveRunIsUsing below; this one is about
	// names.
	dirs := map[string]time.Duration{
		"schemagen-gocache-abandoned":  3 * time.Hour,
		"schemagen-cogen-abandoned":    3 * time.Hour,
		"schemagen-external-abandoned": 3 * time.Hour,
		"schemagen-rt-abandoned":       3 * time.Hour,
		"schemagen-val-abandoned":      3 * time.Hour,
		"schemagen-gocache-live":       time.Minute,
		"schemagen-cogen-live":         time.Minute,
		"schemagen-external-live":      time.Minute,
		"schemagen-rt-live":            time.Minute,
		"schemagen-val-live":           time.Minute,
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

	sweepStaleCachesIn(tmp, time.Now().Add(-staleCacheAge))

	gone := []string{
		"schemagen-gocache-abandoned", "schemagen-cogen-abandoned",
		"schemagen-external-abandoned", "schemagen-rt-abandoned", "schemagen-val-abandoned",
	}
	kept := []string{
		"schemagen-gocache-live", "schemagen-cogen-live", "schemagen-external-live",
		"schemagen-rt-live", "schemagen-val-live", "schemagen-gocache-future",
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
	assertNoDoomedLeftovers(t, tmp)
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
	cmd := exec.Command("go", "build", "-o", filepath.Join(src, "bin"), ".")
	cmd.Dir = src
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GOCACHE=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "GOCACHE="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building a real cache into %s: %v\n%s", dir, err, out)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) < 100 {
		t.Fatalf("%s does not look like a Go build cache (%d entries, %v); the fixture is worthless if it is not one", dir, len(entries), err)
	}
}

// ageTree stamps a directory's root and every immediate child as untouched for
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
func ageTree(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read cache %s: %v", dir, err)
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

// holdCacheLock claims a cache the way a run does. Separate open files are
// separate flock holders even inside one process, so a second call is a second
// concurrent run as far as the kernel is concerned.
func holdCacheLock(t *testing.T, dir string) *cacheLock {
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

// assertNoDoomedLeftovers checks that the rename-then-delete left nothing
// behind. A doomed directory that survives is a directory nobody will look for.
//
// It matches the marker anywhere in the name rather than a fixed prefix,
// because the prefix is now whatever the directory came in with: a swept cache
// becomes schemagen-gocache-doomed-*, a swept work directory
// schemagen-val-doomed-* and so on. A check written against one spelling would
// stop watching the other four the moment they were added.
func assertNoDoomedLeftovers(t *testing.T, tmp string) {
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
	if !cacheLockingSupported {
		t.Skip("cache locking is unavailable on this platform; the sweep judges by age alone there")
	}
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "schemagen-gocache-shared-test")
	buildRealCache(t, dir)
	ageTree(t, dir, 3*time.Hour)

	lock := holdCacheLock(t, dir)
	sweepStaleCachesIn(tmp, time.Now().Add(-staleCacheAge))
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the sweep deleted a cache a live run was holding (%v); that run now recompiles from nothing and nothing says so", err)
	}
	assertNoDoomedLeftovers(t, tmp)

	lock.release()
	sweepStaleCachesIn(tmp, time.Now().Add(-staleCacheAge))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("an abandoned cache of exactly the shape spared above survived the sweep (%v); the lock was not what spared it, and an abandoned cache stays on the volume", err)
	}
	assertNoDoomedLeftovers(t, tmp)
}

// workDirFixtureSchema is the schema the work-directory fixtures are generated
// from.
//
// It is written for what it makes the generator emit, not for what it says: a
// root object with a Validate() method, string, numeric, array and object
// constraints, a pattern, and an asserted `format: email`. The format block is
// what puts the ECMA-262 engine and x/net/idna in the helper file, so the
// module carries the same external requirements as the ones the corpus
// produces and is compiled the same way rather than being a cheap stand-in.
const workDirFixtureSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "a": {"type": "string", "minLength": 2, "pattern": "^a.*", "format": "email"},
    "b": {"type": "integer", "minimum": 1, "maximum": 100},
    "c": {"type": "array", "items": {"type": "string"}, "minItems": 1},
    "d": {"type": "object", "additionalProperties": {"type": "number"}}
  },
  "required": ["a", "b"]
}`

// buildRealWorkDir writes a work directory the way tryValidation writes one:
// the real generator and emitter over a real schema, the real helper-file
// companion, the real go.mod and go.sum, and the real generated main().
//
// A hand-made directory is not a fixture for this, for the same reason a
// hand-made GOCACHE was not a fixture for the cache sweep -- and that mistake
// has already shipped here once, in a test whose "live" control was a bare
// directory while the sweep it guarded would have deleted every long-running
// cache on the box. What the sweep has to spare is a directory with a
// compilable module in it and a `go` process working inside it, so that is what
// this builds.
//
// fixture.json is made a named pipe rather than a file. The generated program
// reads it with os.ReadFile, which blocks in the open until a writer arrives,
// so the caller decides exactly when the program stops being mid-run -- the
// directory is genuinely in use, at an instant chosen rather than hoped for.
// The returned path is that pipe.
func buildRealWorkDir(t *testing.T, dir string) string {
	t.Helper()
	code, err := tryGenerateWithValidation(json.RawMessage(workDirFixtureSchema), nil, schema.Draft202012, false, true, false)
	if err != nil {
		t.Fatalf("generating the fixture module: %v", err)
	}
	if code == "" {
		t.Fatalf("the fixture schema produced no Validate(); a work directory without one is not what the harness compiles")
	}
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		t.Fatalf("no root type in the generated fixture")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	main := strings.Replace(code, "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(main), 0o644); err != nil {
		t.Fatalf("write types.go: %v", err)
	}
	if err := writeSharedHelpersErr(dir, main); err != nil {
		t.Fatalf("write helpers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(generateValidateMain(rootType)), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := writeTestGoMod(dir, "validate_test"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	// The fixture is worthless if it is not the real shape, so it says so here
	// rather than passing quietly with three files in it.
	for _, name := range []string{"types.go", "schemagen_helpers.go", "main.go", "go.mod", "go.sum"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("the fixture work directory has no %s (%v); it is not a module the harness would have written", name, err)
		}
	}
	helpers, err := os.ReadFile(filepath.Join(dir, "schemagen_helpers.go"))
	if err != nil {
		t.Fatalf("read helpers: %v", err)
	}
	for _, dep := range []string{"goecma262", "golang.org/x/net/idna"} {
		if !strings.Contains(string(helpers), dep) {
			t.Fatalf("the fixture module does not import %s, so it is cheaper to build than the ones the corpus produces and is not the thing being spared", dep)
		}
	}

	fixture := filepath.Join(dir, "fixture.json")
	if !makeBlockingFile(fixture) {
		t.Fatalf("could not create the blocking fixture at %s", fixture)
	}
	return fixture
}

// holdRunLock claims tmp the way a live run claims it, by taking the shared
// lock on the run lock file. Separate open files are separate flock holders
// even inside one process, so this is another run as far as the kernel is
// concerned.
func holdRunLock(t *testing.T, tmp string) *cacheLock {
	t.Helper()
	return holdCacheLock(t, sharedCachePathIn(tmp))
}

// TestSweepSparesAWorkDirectoryALiveRunIsUsing is the half of #158 that adding
// four prefixes to a list does not get right on its own.
//
// A work directory's mtimes are stamped once, when the harness finishes writing
// the generated module into it, and nothing afterwards advances them: the
// compile and the run that follow write into GOCACHE and into go run's own
// scratch space, never into the directory they are working in. Measured over a
// live one at 250ms intervals through a cold `go run` -- every sample reporting
// a root age and a cacheLastActive age equal to its own elapsed time, and every
// child still stamped at the moment it was written. So a work directory has
// exactly the property that made the first cache sweep wrong: judged on mtimes,
// a directory in use is indistinguishable from one a killed run left behind.
//
// The commands are bounded -- 30s for a compile or a round trip, 90s for a
// generated program, 10m for the bowtie oracle -- and the cutoff is an hour, so
// today the arithmetic holds. It holds by a margin nobody is consulting when
// they raise a timeout, and a `go run` killed at its context whose child still
// holds the output pipe is bounded by nothing at all. So the claim decides, and
// what is asserted here is the claim deciding: the fixture is aged past the
// cutoff so that age says "delete", and only the lock says otherwise.
//
// The control is the same directory a moment later with the run gone. If that
// one does not disappear, this test proves nothing, because it would mean the
// age check had spared it all along.
func TestSweepSparesAWorkDirectoryALiveRunIsUsing(t *testing.T) {
	if !cacheLockingSupported {
		t.Skip("without flock nothing can claim a run, so the sweep judges work directories by age alone here")
	}
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "schemagen-val-liveworkdir")
	fixture := buildRealWorkDir(t, dir)

	// A real run, in the real directory, with the real arguments and the real
	// cache. It gets as far as reading its fixture and stops there.
	cmd := exec.Command("go", goRunArgs...)
	cmd.Dir = dir
	cmd.Env = sharedCacheEnv()
	type runResult struct {
		out []byte
		err error
	}
	finished := make(chan runResult, 1)
	go func() {
		out, err := cmd.CombinedOutput()
		finished <- runResult{out, err}
	}()

	// Opening the pipe for writing returns only once the program has opened it
	// for reading, so this is the build having succeeded and the program being
	// alive inside dir -- known, not assumed.
	opened := make(chan *os.File, 1)
	go func() {
		w, err := os.OpenFile(fixture, os.O_WRONLY, 0)
		if err != nil {
			opened <- nil
			return
		}
		opened <- w
	}()
	var w *os.File
	select {
	case w = <-opened:
		if w == nil {
			t.Fatalf("could not open the fixture pipe for writing")
		}
	case r := <-finished:
		t.Fatalf("the run ended before the generated program read its fixture: %v\n%s", r.err, r.out)
	case <-time.After(5 * time.Minute):
		t.Fatalf("the generated program never reached its fixture")
	}

	// Aged past the cutoff, which is what a work directory whose command has
	// outlived the sweep's patience looks like -- and what every live one would
	// look like if a timeout were ever raised past an hour.
	ageTree(t, dir, 3*time.Hour)

	lock := holdRunLock(t, tmp)
	sweepStaleCachesIn(tmp, time.Now().Add(-staleCacheAge))
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the sweep deleted a work directory a live run was compiling in (%v); that group now fails to build and reports itself as a schema failure", err)
	}
	for _, name := range []string{"types.go", "schemagen_helpers.go", "main.go", "go.mod", "go.sum"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("the sweep emptied a work directory a live run was using: %s is gone (%v)", name, err)
		}
	}
	assertNoDoomedLeftovers(t, tmp)

	// Let it finish. The verdict is the proof that the directory really was
	// usable throughout, rather than merely still present.
	if _, err := w.Write([]byte(`{"a":"a@example.com","b":5,"c":["x"],"d":{"e":1.5}}`)); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	w.Close()
	select {
	case r := <-finished:
		if r.err != nil {
			t.Fatalf("the run that survived the sweep then failed: %v\n%s", r.err, r.out)
		}
		if got := strings.TrimSpace(string(r.out)); got != "VALID" {
			t.Fatalf("the generated program said %q, not VALID; the directory it ran in was not intact", got)
		}
	case <-time.After(5 * time.Minute):
		t.Fatalf("the generated program never finished after its fixture arrived")
	}

	// The control. Same directory, same ages, nobody running: it must go, or
	// the lock was not what spared it above and this proves nothing.
	lock.release()
	ageTree(t, dir, 3*time.Hour)
	sweepStaleCachesIn(tmp, time.Now().Add(-staleCacheAge))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a work directory of exactly the shape spared above, with no run left to own it, survived the sweep (%v); either the lock was not what spared it or /tmp keeps the debris #158 is about", err)
	}
	assertNoDoomedLeftovers(t, tmp)
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
	if !cacheLockingSupported {
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
	for _, prefix := range append(append([]string{}, workDirPrefixes...), cacheDirPrefixes...) {
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
		ageTree(t, doomed, 3*time.Hour)
		sweepStaleCachesIn(tmp, time.Now().Add(-staleCacheAge))
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
	if !cacheLockingSupported {
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
	assertNoDoomedLeftovers(t, tmp)

	// SCHEMAGEN_KEEP_GOCACHE is the deliberate other answer: keep the cache for
	// the next run, warm, and pay for it.
	kept := filepath.Join(tmp, "schemagen-gocache-shared-kept")
	buildRealCache(t, kept)
	releaseCacheDir(kept, holdCacheLock(t, kept), true)
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the cache was deleted although the run asked to keep it (%v)", err)
	}
}

// TestCacheHeadroomRefusesAVolumeTooSmallForTheRun watches the precondition
// fire, on a volume that really does have less free space than it is asked for.
//
// The requirement is raised past what the volume holds rather than the volume
// being filled: the predicate is the same one a full disk trips, and filling a
// 394G volume to watch a check fire is the harm this check exists to prevent.
// The message is asserted on because the message is the whole value of the
// check -- the failure it replaces was 144 "no space left on device" errors
// attributed to individual schemas.
func TestCacheHeadroomRefusesAVolumeTooSmallForTheRun(t *testing.T) {
	dir := t.TempDir()
	free, err := freeBytes(dir)
	if err != nil {
		t.Skipf("free space is not measurable here: %v", err)
	}

	msg, measured := cacheShortfall(dir, free+(1<<30))
	if !measured {
		t.Fatalf("the volume holding %s reported free space and then could not be measured", dir)
	}
	if msg == "" {
		t.Fatalf("a volume with %.1f GiB free passed a check for %.1f GiB", float64(free)/(1<<30), float64(free+(1<<30))/(1<<30))
	}
	for _, want := range []string{dir, "no space left on device", "free", "requires"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, and a message that does not name the requirement is why this failure was read as schema failures:\n%s", want, msg)
		}
	}

	// The control. Without it a check that refuses everything would pass the
	// test above, and refusing every run is not an improvement on dying halfway
	// through one.
	if msg, measured := cacheShortfall(dir, 1); !measured || msg != "" {
		t.Errorf("a run needing one byte was refused on a volume with %.1f GiB free: %s", float64(free)/(1<<30), msg)
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
		var env []string
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "GOCACHE=") {
				env = append(env, e)
			}
		}
		cmd := exec.Command("go", goRunArgs...)
		cmd.Dir = dir
		cmd.Env = append(env, "GOCACHE="+cache)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compiling in %s: %v\n%s", dir, err, out)
		}
	}

	tmp := t.TempDir()
	build(filepath.Join(tmp, "schemagen-rt-1113344"))
	before := treeBytes(t, cache)
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
