package fuzzdeadline

import (
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
)

// fuzzSeedBudget is the CPU-time ceiling one seed may take through the fuzz
// body.
//
// It exists because Go's fuzzing engine has a per-input deadline of its own that
// nothing in this repository can change: internal/fuzz gives the worker process
// ten seconds per call to the fuzz function and panics it as deadlocked past
// that. The coordinator sees a worker that died, reports "fuzzing process hung or
// terminated unexpectedly: exit status 2" against whichever seed it was holding,
// and stops -- while still gathering baseline coverage, so no fuzzing happens at
// all. The worker's stderr is discarded, so there is no stack and no crasher
// file, and `go test ./...` does not reproduce it because the ordinary seed
// replay runs in-process with no deadline. That is issue #233, and it left the
// fuzz gate inert for as long as it took someone to look.
//
// Two seconds is Go's ten mapped onto this binary. The worker runs a
// coverage-instrumented build, measured at roughly four to five times slower than
// an ordinary one on the same seeds, so a seed at two seconds here is at the
// deadline there. The budget is therefore the point of failure rather than a
// margin below it -- which is the only threshold that does not go stale, since a
// slower machine moves both sides of it together.
//
// It is spent as CPU time, not wall-clock time, and that is deliberate rather
// than incidental. What the ten-second deadline actually limits is how long the
// worker process may occupy a CPU on one input before internal/fuzz gives up on
// it; a worker runs one input at a time in a process of its own; nothing else in
// that process competes with it for a core. Wall-clock time and CPU time are
// therefore the same figure for the thing being modelled. They stopped being the
// same figure for the thing measuring it once #370 split tests/ into packages
// `go test ./...` runs as separate, concurrent binaries: this test's own process
// now shares the machine with the others, and the wall clock counts every
// microsecond it spent runnable but waiting for a core alongside them, which the
// worker this budget stands in for never has to. CPU time (getrusage,
// RUSAGE_SELF -- see fuzz_cpu_unix_test.go) does not count that wait, so it
// keeps measuring the pipeline's own cost regardless of how many other test
// binaries happen to be running beside it. GC the seed's own allocation
// triggers still counts, correctly: that is work the worker would also pay for.
//
// The corpus runs about three times under it as this is written: the slowest
// seed is the 2000-deep `not`, at roughly 0.6s of CPU time (against several
// seconds of wall-clock time under `go test ./...`'s contention, which is the
// gap this file closes). That same seed took five seconds of wall-clock time
// before #233, and the 1000-deep anyOf that first killed the gate took two.
const fuzzSeedBudget = 2 * time.Second

// TestFuzzSeedCorpusFitsTheWorkerDeadline runs every fuzz seed through the fuzz
// body and fails on any that takes longer than fuzzSeedBudget of CPU time.
//
// `go test ./...` already replays the seed corpus -- that is what a fuzz target
// does when it is run without -fuzz -- but it replays it without a clock, so a
// seed that has become slow enough to kill a fuzz worker passes there and takes
// the whole fuzz gate down separately, on a nightly schedule, with a message that
// names no cause. This is the check that fails on the pull request instead.
//
// Not parallel, and it must stay that way: it is timing what one goroutine's
// calls to fuzzOnce cost, on the getrusage(RUSAGE_SELF) figure that fuzzSeedBudget
// is spent against, which sums every thread the *process* runs. A second
// goroutine doing CPU-bound work of its own inside this same loop -- a t.Parallel
// subtest sharing the binary, say -- would land in that same sum and inflate
// every seed's reading by however much it ran concurrently with. Nothing else in
// this package calls t.Parallel, so nothing does that today; a review adding one
// should re-read this comment first. That is also why this test is a package of
// its own, tests/fuzzdeadline: a sibling test in the same binary that burned CPU
// concurrently would land in the same figure. fuzzOnce's own memory gate
// (tests/internal/testsupport/fuzzgate.go) does run the pipeline on a second
// goroutine and keeps one sampler goroutine parked for the life of the binary,
// but both are part of what a real fuzz execution costs -- FuzzGenerate
// (tests/fuzz) runs seeds through that same gate -- and
// their combined overhead is documented at roughly 1.2us/op, four orders of
// magnitude under the seeds this budget is sized for.
func TestFuzzSeedCorpusFitsTheWorkerDeadline(t *testing.T) {
	if !cpuTimeAvailable {
		t.Skip("this platform has no getrusage(RUSAGE_SELF); see fuzz_cpu_other_test.go")
	}

	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter.New: %v", err)
	}

	type slow struct {
		origin string
		bits   uint8
		took   time.Duration
	}
	var worst slow
	var over []slow
	seeds := 0

	local, external, err := fuzzSeedCorpus(func(origin string, schema []byte) {
		for _, bits := range fuzzSeedCfgBits {
			seeds++
			start := cpuNanos()
			fuzzOnce(em, bits, schema)
			took := time.Duration(cpuNanos() - start)
			if took > worst.took {
				worst = slow{origin, bits, took}
			}
			if took > fuzzSeedBudget {
				over = append(over, slow{origin, bits, took})
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("timed %d seeds by CPU time (%d local files, %d external test groups); slowest %v, %s with cfgBits 0x%02X",
		seeds, local, external, worst.took.Round(time.Millisecond), worst.origin, worst.bits)

	for _, s := range over {
		t.Errorf("seed %s with cfgBits 0x%02X used %v of CPU time, over the %v budget. Go's fuzzing worker panics "+
			"after ten seconds on one input and the worker binary is coverage-instrumented and several times "+
			"slower than this one, so a seed here is on its way to taking the fuzz gate down entirely -- "+
			"see issue #233. This is CPU time, not wall-clock time, so it is not a machine shared with other "+
			"test binaries that did this; make the pipeline handle this schema faster; do not drop the seed, "+
			"and do not raise the budget",
			s.origin, s.bits, s.took.Round(time.Millisecond), fuzzSeedBudget)
	}
}
