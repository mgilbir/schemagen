package testgo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sharedCacheDir is the GOCACHE every go command a test starts points at: one
// directory per user, adopted by every test binary on the box rather than
// created per process -- the tests package, cmd/schemagen, pkg/emitter and the
// rest of a `go test ./...` all share it, and the last one out deletes it.
//
// The reason for not using ~/.cache/go-build has not changed -- a full external
// run is ~27,000 compilations of code nobody will build again, and leaving that
// in the developer's own cache is not a kindness. What changed is that a
// *per-process* copy of it multiplies by the number of runs in flight. Four
// such caches sitting at 24G, 25G, 21G and 16G at once filled a 394G volume,
// and a full volume does not announce itself as one: it reports "no space left
// on device" against individual test keys -- at the link, at the write, at the
// mkdir -- so a dead run reads exactly like a set of real validation failures,
// and one of them reported 13826 passing subtests, 0 failures and no coverage
// line at all.
//
// Sharing makes it one directory however many runs are going, and with
// -trimpath taking the paths out of the entries (see Env), that directory is
// small:
// two full runs started two minutes apart peaked at 1.9G between them, where
// the same two runs beforehand held 34.6G and 37.1G and were still growing.
//
// Go's build cache is safe to share, which is not a claim taken on faith:
// ~/.cache/go-build is already shared by every concurrent go command a user
// runs, and cmd/go's cache is written for exactly that. An entry is committed
// by writing its last byte, "because writing it will make the size match what
// other processes expect to find and might cause them to start using the file";
// trim.txt is read and rewritten under lockedfile; an ETXTBSY on an output is
// read as "it must have already been written by another go process and then
// run". Measured here as well, at 8 concurrent processes putting 25 identical
// modules each through one cache -- 200 compilations racing for the same
// entries -- with no failure and no wrong output.
var sharedCacheDir string

// sharedCacheLock is this process's claim on that directory, held for the
// lifetime of the run. See cacheLock.
var sharedCacheLock *cacheLock

// sharedCachePath names the shared cache, and sharedCacheLockPath the lock that
// says who is using it.
//
// The uid is in the name because /tmp is shared between users: a fixed name
// belongs to whoever ran first, and the second user then cannot write to it at
// all -- a failure mode a per-process MkdirTemp did not have and this must not
// introduce. The lock file sits *beside* the cache rather than inside it,
// because a lock inside a directory that gets renamed away stops being the file
// the next process opens: two runs would then hold locks on two inodes and
// neither would see the other. For the same reason the lock file is never
// deleted, by the sweep or by anything else. It is empty, and unlinking it
// while another process has it open is precisely how mutual exclusion is lost.
//
// Both take the temp directory as an argument in their -In form, so a test can
// stand up a whole box's worth of state -- a cache, a lock, a set of work
// directories -- inside a t.TempDir() and drive the sweep over it. The
// no-argument forms are the real ones every caller outside a test uses.
func sharedCachePathIn(tmp string) string {
	return filepath.Join(tmp, fmt.Sprintf("schemagen-gocache-shared-%d", os.Getuid()))
}

func sharedCachePath() string { return sharedCachePathIn(os.TempDir()) }

func sharedCacheLockPathIn(tmp string) string { return sharedCachePathIn(tmp) + ".lock" }

func sharedCacheLockPath() string { return sharedCacheLockPathIn(os.TempDir()) }

// cacheLock is a process's claim on a shared cache directory.
//
// It is an flock rather than a marker file or a pid list because the question
// is "is another run using this right now" and the kernel answers it exactly:
// the lock is held for as long as the process lives and is released when it
// dies, including under kill -9 and including a run killed by the very full
// disk this machinery exists to avoid. A marker file has to be cleaned up by
// the process that wrote it, which is the one thing a killed process cannot do,
// and that is the failure #136 already had to write a sweep for.
//
// A run holds it shared: any number of runs may use the cache at once. Deleting
// it requires the exclusive lock, so a delete can only happen when nobody else
// holds the cache -- which is what makes both "the last run out clears up" and
// "the sweep reclaims an abandoned cache" safe to do against a live box.
type cacheLock struct{ f *os.File }

// lockSharedCache takes the shared lock on the cache, creating the lock file if
// this is the first run on the box. A nil result means locking is unavailable
// (a platform without flock, or a lock file that cannot be opened); the caller
// then uses the cache anyway and leaves reclaiming it to the age-based sweep.
func lockSharedCache() *cacheLock {
	if !LockingSupported {
		return nil
	}
	f, err := os.OpenFile(sharedCacheLockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil
	}
	if !flockShared(f) {
		f.Close()
		return nil
	}
	return &cacheLock{f: f}
}

// lockCacheForRemoval takes the exclusive lock on the cache at path, if that
// cache has a lock file at all.
//
// The two "false" answers are deliberately different. A directory with no lock
// file beside it is a per-process cache from an older revision: nothing claims
// it, so removal proceeds on the age check alone, exactly as it did before. A
// directory whose lock is held is one a live run is building into, and it must
// survive however old its mtimes look.
//
// Only caches reach here. A work directory has no lock of its own and is judged
// against the run lock instead; see WorkDirPrefixes and anotherRunIsAlive.
func lockCacheForRemoval(path string) (*cacheLock, bool) {
	if !LockingSupported {
		return nil, true
	}
	f, err := os.OpenFile(path+".lock", os.O_RDWR, 0o600)
	if err != nil {
		return nil, true
	}
	if !flockExclusiveNB(f) {
		f.Close()
		return nil, false
	}
	return &cacheLock{f: f}, true
}

// tryExclusive reports whether this process is the only one still holding the
// cache, by upgrading its shared lock.
//
// A failed upgrade can leave the shared lock dropped -- flock(2) says the
// conversion is not atomic -- which is why this is only ever called on the way
// out, after the last build has run. The answer is still right: whoever is left
// holding the cache gets the exclusive lock when they in turn finish.
func (l *cacheLock) tryExclusive() bool {
	if l == nil {
		return false
	}
	return flockExclusiveNB(l.f)
}

// release drops the lock. Closing the file is what releases an flock, and it is
// also what the kernel does for a process that never gets here.
func (l *cacheLock) release() {
	if l != nil {
		l.f.Close()
	}
}

// StaleAge is how old an abandoned cache must be before this process will
// delete it. It has to exceed the longest a live run can go without touching
// its own directory -- the suite compiles continuously, so minutes would do --
// and stay far below the gap between one developer's runs. An hour is well
// clear on both sides.
const StaleAge = time.Hour

// cacheLastActive reports when a cache directory was last written to.
//
// It is not the directory's own mtime, which is the obvious reading and the
// wrong one. Go lays out GOCACHE as 256 subdirectories plus trim.txt on first
// use and writes every entry *inside* those, so the root's mtime is stamped once
// when the cache is created and never advances again however hard the cache is
// worked -- measured at six consecutive builds over fifteen seconds, all five
// after the first leaving the root untouched. Judging by it makes a run that
// outlives StaleAge indistinguishable from an abandoned one, so a
// concurrently starting run deletes a live cache out from under it. That is the
// "sweeping too much" direction this function's comment calls the worse and
// quieter of the two: nothing fails visibly, the robbed run just recompiles from
// nothing and looks inexplicably slow.
//
// The immediate children do track activity -- each build touches the bucket it
// writes into, and trim.txt is rewritten periodically -- so the newest of the
// root and its children is the answer. Reading one level is enough and is
// bounded: 257 entries for a Go cache, and no recursion into the thousands of
// files below.
func cacheLastActive(dir string, root os.FileInfo) time.Time {
	newest := root.ModTime()
	children, err := os.ReadDir(dir)
	if err != nil {
		return newest
	}
	for _, c := range children {
		info, err := c.Info()
		if err != nil {
			continue
		}
		if t := info.ModTime(); t.After(newest) {
			newest = t
		}
	}
	return newest
}

// The two families of directory tests leave in the temp directory, and the
// reason each is judged the way it is.
//
// A *cache* is one per user and lives as long as any run is using it, so it is
// claimed by an flock on a lock file beside it and the sweep asks that lock
// before deleting one. A *work directory* is one per test case: the harness
// draws it, writes a generated module into it, compiles or runs that module,
// reads the verdict and deletes it, all inside a single bounded command. A run
// of the external suite makes roughly 27,000 of them. A *process directory*
// (MkdirProcessTemp) is the same kind of thing held for the life of one test
// binary instead of one command -- a CLI built once and run by every test --
// and is judged exactly as a work directory is.
//
// That count is why they are not given locks of their own. A lock file has to
// outlive the directory it guards -- unlinking one while another process has it
// open is precisely how mutual exclusion is lost, which is why the cache's lock
// is never deleted -- so a lock per work directory would trade seven stranded
// directories for 27,000 stranded lock files, which is the litter this is meant
// to remove, multiplied.
//
// So a work directory is judged by age *and* by the claim the run that owns it
// already holds: the shared cache lock. See anotherRunIsAlive.
//
// A directory a test makes anywhere else in the temp directory is either a
// t.TempDir(), which the testing package removes, or a leak: nothing here would
// ever recognise it. TestEveryTempDirectoryATestMakesIsOneTheSweepKnows holds
// the module's tests to these names.
const ProcessDirPrefix = "schemagen-bin-"

var (
	cacheDirPrefixes = []string{"schemagen-gocache-"}

	// legacyDirPrefixes are the names two tests used before MkdirProcessTemp
	// existed, for a CLI built once per test binary and never removed:
	// root main_test.go's "schemagen-main-test" and
	// tests/crosspackage_agreement_test.go's "schemagen-bin" (no dash, a random
	// suffix straight after). Nothing makes them any more; they are swept, as
	// work directories, so that the ones earlier versions left behind -- 252 of
	// them, 863 MB, on the machine the 2026-09-26 audit ran on -- are reclaimed
	// rather than kept forever. No new directory may be made under them; see
	// TestEveryTempDirectoryATestMakesIsOneTheSweepKnows.
	legacyDirPrefixes = []string{"schemagen-main-test", "schemagen-bin"}

	// WorkDirPrefixes are the names a work directory may be drawn under; see
	// MkdirWorkTemp.
	WorkDirPrefixes = []string{
		"schemagen-cogen-",    // one generated program, built and run (cogen, and its bowtie oracle)
		"schemagen-external-", // one generated module, compiled
		"schemagen-rt-",       // one generated module, built and round-tripped
		"schemagen-val-",      // one generated module, built and asked for a verdict
	}
)

// sweptDirKind classifies a name in the temp directory, reporting the prefix it
// matched and whether that prefix names a work directory rather than a cache.
//
// The prefix is returned rather than discarded because the doomed name a
// removal renames through keeps it: a directory stranded between the rename and
// the delete should say what it was, and calling a work directory a gocache is
// the kind of unattributable debris this sweep exists to stop leaving behind.
func sweptDirKind(name string) (prefix string, work, ok bool) {
	// ProcessDirPrefix before the legacy "schemagen-bin", which it extends.
	for _, p := range append(append(append([]string{}, WorkDirPrefixes...), ProcessDirPrefix), legacyDirPrefixes...) {
		if strings.HasPrefix(name, p) {
			return p, true, true
		}
	}
	for _, p := range cacheDirPrefixes {
		if strings.HasPrefix(name, p) {
			return p, false, true
		}
	}
	return "", false, false
}

// anotherRunIsAlive reports whether any run of this package is currently using
// tmp, by asking for the shared cache lock nobody may hold but a live run.
//
// This is the claim a work directory cannot carry itself, and it is exact for
// the question that matters: a work directory only ever outlives the command
// that made it if the process died, so if no run is alive then every work
// directory in tmp is abandoned, and if one is alive the sweep leaves them all
// for the next run that starts on an idle box.
//
// It is asked once per sweep, before anything is removed, and the exclusive
// lock is dropped immediately -- a run arriving meanwhile waits microseconds
// for a lock it takes shared. A run *exiting* in that same window finds its own
// upgrade to exclusive refused and leaves the cache for the age sweep instead
// of deleting it, which is a miss of microseconds against the length of a run
// and is in the safe direction: it costs one cache an idle hour on the volume,
// where the other direction would cost a live run its cache.
//
// A missing lock file means no run has ever started here, which is "not alive".
// So does a platform without flock: nothing can claim anything there, and the
// alternative reading -- treating an unanswerable question as "a run is alive"
// -- would make work directories immortal on that platform, which is the leak
// being fixed. There the age check stands alone, exactly as it does for the
// cache. See cachefs_other.go.
func anotherRunIsAlive(tmp string) bool {
	if !LockingSupported {
		return false
	}
	f, err := os.OpenFile(sharedCacheLockPathIn(tmp), os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	return !flockExclusiveNB(f)
}

// sweepStaleCaches deletes the ephemeral cache and work directories left behind
// by earlier runs.
//
// Main removes this process's directories on the way out, which handles the
// common path. It cannot handle any other: a -timeout kill, a SIGTERM from a
// harness, an interrupt, or a crash on a full disk all skip it, and each one
// strands a cache of roughly 2G plus whatever work directories were in flight.
// That compounds -- a full disk kills runs, and each kill strands another cache
// -- and it has already exhausted a 394G volume, at which point the suite fails
// with "no space left on device" reported against individual test keys, so a
// dead run reads like a set of real validation failures.
//
// The work directories are small: the seven a session of killed runs left
// behind came to 200K, against the 25G a single stranded cache used to cost,
// so this half is not what fills a volume. They are
// swept for the other reason, which is that /tmp should not accumulate debris
// whose provenance nobody can work out a week later -- that is what made
// diagnosing the cache problem harder than it needed to be.
//
// So the sweep happens on the way in, where it works no matter how the previous
// process died. Errors are ignored throughout: reclaiming space is best-effort,
// and a directory another user owns is not this process's to worry about.
func sweepStaleCaches() { sweepStaleCachesIn(os.TempDir(), time.Now().Add(-StaleAge)) }

// sweepStaleCachesIn is sweepStaleCaches with the directory and cutoff supplied,
// so a test can drive it without touching the real temp directory or waiting an
// hour for something to age.
func sweepStaleCachesIn(tmp string, cutoff time.Time) {
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	// Asked once, before anything is removed, and read by every work directory
	// below. The listing above is what bounds the answer's usefulness in the
	// other direction: a run that starts a moment after this returns "no" draws
	// its work directories under names that are not in entries, so nothing this
	// loop can reach belongs to it.
	runAlive := anotherRunIsAlive(tmp)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		prefix, work, ok := sweptDirKind(name)
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(tmp, name)
		if cacheLastActive(path, info).After(cutoff) {
			continue
		}
		// The age check above is a heuristic about mtimes; a lock is a fact
		// about processes. Both have to agree before anything is deleted, and
		// the lock is the one that cannot be fooled.
		var lock *cacheLock
		if work {
			// A work directory's mtimes are frozen the moment the harness
			// finishes writing the module into it -- measured over a live one:
			// root and every child stamped once, and neither the compile nor
			// the run that follows advancing either -- so it has the same shape
			// as a cache root, and age alone reads a live directory as an
			// abandoned one for as long as its command takes. The commands are
			// bounded (30s for a compile or a round trip, 90s for a generated
			// program, 10m for the bowtie oracle) and the cutoff is an hour, so
			// the arithmetic happens to hold; but it holds by a margin nobody
			// is consulting when they raise a timeout, and a command that hangs
			// past its context -- a killed `go run` whose child still holds the
			// output pipe -- is bounded by nothing. So the claim decides, and
			// the arithmetic is the second opinion.
			if runAlive {
				continue
			}
		} else {
			// A run that is getting nothing but cache *hits* creates no new
			// bucket entries, so it advances no mtime this sweep can see, and
			// that is exactly the run a *shared* cache makes common. Without
			// this, the second concurrent run would delete the first one's
			// cache out from under it, and the robbed run would not fail -- it
			// would silently recompile from nothing.
			var free bool
			lock, free = lockCacheForRemoval(path)
			if !free {
				continue
			}
		}
		doomed, moved := renameForRemoval(path, prefix)
		// The claim is wanted for the rename and not for the emptying: holding
		// it through the delete would stall the startup of every run that
		// arrives meanwhile, and those runs have nothing to wait for -- the
		// directory they will create is already a different one. Nil for a work
		// directory, which holds no lock of its own; release says so.
		lock.release()
		if moved {
			os.RemoveAll(doomed)
		}
	}
}

// renameForRemoval moves a directory aside, and reports where to. prefix is the
// swept prefix the directory's name carried.
//
// Deleting a cache is done in two steps because the second one is slow. The
// rename is atomic, so a run arriving a moment later finds no directory and
// creates a fresh one rather than building into a tree being emptied underneath
// it, and only one of two sweepers can win. Emptying a cache takes long enough
// for that window to be real -- seconds for the 1.9G a shared one holds, tens of
// them for the 35G a per-process one reached -- and what comes out of the window
// is a build failure attributed to a test case, which is the shape of wrongness
// this whole file exists to stop reporting.
//
// The doomed name keeps the prefix the directory came in with, which does two
// things: a crash between the rename and the delete leaves something the next
// run reclaims rather than a permanent leak, and it leaves it under a name that
// still says what it was. A work directory renamed to schemagen-gocache-doomed
// would be reclaimed just as reliably and would be a lie to whoever found it.
func renameForRemoval(path, prefix string) (string, bool) {
	doomed := filepath.Join(filepath.Dir(path),
		fmt.Sprintf("%sdoomed-%d-%d", prefix, os.Getpid(), time.Now().UnixNano()))
	if err := os.Rename(path, doomed); err != nil {
		// Already gone, or not ours to move. Either way there is nothing to do:
		// reclaiming space is best-effort, and a directory another user owns is
		// not this process's to worry about.
		return "", false
	}
	return doomed, true
}

// sweptBeforeClaiming records that the sweep ran before this process took its
// own claim on the shared cache.
//
// The order is load-bearing now that anotherRunIsAlive reads that same claim: a
// process holding it sees itself as a live run and stops reclaiming work
// directories altogether. That regression has no symptom -- a sweep that
// reclaims nothing looks exactly like a temp directory with nothing to reclaim
// -- so the order is recorded here and asserted in
// TestSweepAsksBeforeThisRunClaimsTheCache rather than left to whoever next
// edits claimSharedCache.
var sweptBeforeClaiming bool

// claimSharedCache sweeps what earlier runs abandoned and then takes this
// process's claim on the shared cache. Run calls it before the first test.
func claimSharedCache() {
	sweptBeforeClaiming = sharedCacheLock == nil
	sweepStaleCaches()
	// The lock comes before the directory: a sweeper that has just decided this
	// cache is abandoned holds the exclusive lock while it renames it away, so
	// taking the shared lock first is what stops this process from creating a
	// directory into a rename that has not happened yet.
	sharedCacheLock = lockSharedCache()
	dir := sharedCachePath()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		panic(fmt.Sprintf("creating shared GOCACHE %s: %v", dir, err))
	}
	sharedCacheDir = dir
}

// releaseSharedCache hands the shared cache back, deleting it when nobody else
// holds it.
//
// Deleting on the way out is what keeps the footprint bounded over time, and
// the bound is the point of the exercise. Sharing alone bounds *concurrent*
// runs at one directory; it does nothing for consecutive ones, because every
// change to the generator makes all ~27,000 compilations miss and adds another
// cache's worth to the same directory. A developer iterating would fill the
// same volume from the other direction, more slowly and just as fatally, and
// the age-based sweep would not touch a cache that is used again every half
// hour.
//
// So the steady state is: one directory while runs overlap, nothing left behind
// once they have all finished. SCHEMAGEN_KEEP_GOCACHE=1 keeps it instead, which
// is worth it when the next run is going to be the same code -- a warm cache
// turns most of a 25-minute run into cache hits -- and costs another cache's
// worth per distinct generator state until an idle hour lets the sweep reclaim
// it.
func releaseSharedCache() {
	if sharedCacheDir == "" {
		return
	}
	releaseCacheDir(sharedCacheDir, sharedCacheLock, os.Getenv("SCHEMAGEN_KEEP_GOCACHE") == "1")
}

// releaseCacheDir is releaseSharedCache with the directory, the claim and the
// choice supplied, so a test can drive both halves without touching the cache
// the running process is using.
//
// The order is the whole of it: the delete happens only if the claim can be
// made exclusive, and the claim is dropped afterwards either way.
func releaseCacheDir(dir string, lock *cacheLock, keep bool) {
	doomed, moved := "", false
	if !keep && lock.tryExclusive() {
		doomed, moved = renameForRemoval(dir, "schemagen-gocache-")
	}
	lock.release()
	if moved {
		os.RemoveAll(doomed)
	}
}
