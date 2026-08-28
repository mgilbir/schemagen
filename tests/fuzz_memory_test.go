package tests

import (
	"bytes"
	"fmt"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
)

// The memory half of the fuzz gate. Its sibling is the wall-clock half in
// fuzz_test.go -- fuzzSeedBudget and TestFuzzSeedCorpusFitsTheWorkerDeadline --
// and the two exist for the same reason and catch different things.
//
// What a time gate cannot see: issue #348 was 35 bytes of legal schema,
// {"items":{"$ref":"#","minItems":1}}, that recursed with the same *schema.Schema
// at every frame, minting a type name five characters longer each level. It was
// *fast*. It never came near the ten-second worker deadline; it died of
// allocation, around 18,700 levels deep, as "fatal error: out of memory".
//
// Why that wording is the whole problem. A panic is caught by the fuzz worker
// (testing.tRunner converts it to a t.Errorf while fuzzing, see
// $GOROOT/src/testing/testing.go), sent back over the RPC, and the coordinator
// then minimizes it and writes a reproducing file. A `fatal error` is not: it
// kills the worker process, internal/fuzz leaves the worker's cmd.Stderr nil (a
// "TODO: record and return stderr" still there in Go 1.26,
// $GOROOT/src/internal/fuzz/worker.go), so the message and stack go nowhere, and
// the coordinator reports only
//
//	fuzzing process hung or terminated unexpectedly while minimizing: EOF
//
// against whatever byte-removal candidate the minimizer happened to be holding
// -- which is truncated JSON that reproduces nothing. That message reads exactly
// the same for a runner OOM, a genuine hang and a real defect. The nightly job
// produced it twice, on 2026-08-22 and 2026-08-28, and it cost hours both times.
//
// So the job here is to trip *deliberately, before the runtime does*, in the one
// form the harness can attribute: a panic that names the input and the config
// byte that produced it.
//
// This is not a hypothetical worth of machinery. A 180-second bounded fuzz run
// during the writing of this file, on an unmodified tree, killed a worker with
// that exact message; the same input replayed under the gate is named in 0.08
// seconds as a stack runaway through patternProperties. The gate found a real
// defect the first time it was pointed at the target.

// An unbounded recursion has two ways to reach that unattributable end, and both
// of them are `fatal error`, so the gate watches two figures rather than one.
//
//	{"items":{"$ref":"#","minItems":1}}          -> fatal error: out of memory
//	{"$defs":{"C":{"type":[{"$ref":"#/$defs/C"}]}}} -> fatal error: stack overflow
//
// The first is #348, and it dies of allocation: each frame mints a type name
// five characters longer than the last, so the *names* cost O(n^2) and the heap
// gives out around 18,700 levels deep, while the goroutine stack stays modest.
// The second closes its loop in a single hop through the draft-3
// type-alternatives path, allocates comparatively little, and dies of *stack*:
// measured on a plant, heap objects oscillated between 15 and 270 MiB -- never
// reaching the heap budget -- while the goroutine stack went 0.6 MiB -> 32 -> 64
// -> 128 -> 256 -> 512 -> 768 MiB and hit the runtime's 1 GB per-goroutine limit
// at t=2.0s. A gate on the heap alone watches one of the two doors.
//
// Neither `fatal error` is recoverable and neither is attributable, so which one
// fires matters much less than that both are named.
const (
	fuzzMemoryHeap = iota
	fuzzMemoryStack
	fuzzMemoryDimensions
)

// fuzzMemoryDimension is one figure the sampler watches and the budget it holds
// that figure to.
type fuzzMemoryDimension struct {
	what   string // names the dimension in the panic message
	metric string
	budget uint64
}

// fuzzMemoryBudgets are the two figures and their ceilings.
//
// runtime/metrics for both, rather than runtime.ReadMemStats, because
// ReadMemStats stops the world (stopTheWorld(stwReadMemStats) at
// $GOROOT/src/runtime/mstats.go:358) and metrics.Read does not -- readMetrics
// takes metricsLock and reads the per-P consistent heap stats, with no STW
// anywhere in it ($GOROOT/src/runtime/metrics.go:1013). A gate that stopped the
// world at every sample would be paid for out of the fuzzer's search budget. Both
// figures come from one metrics.Read, so watching two costs no more samples than
// watching one.
//
// Heap objects rather than a process-footprint figure such as
// /memory/classes/total:bytes minus /memory/classes/heap/released:bytes: that one
// is closer to RSS but barely comes back down, because the allocator keeps mapped
// spans and the scavenger releases them lazily. A gate on it would blame
// whichever input happened to run after a big one. Heap objects returns to
// baseline at the next GC, so what it measures is the input in front of it.
//
// Goroutine stacks are heap-allocated in Go, so heap/stacks is available through
// the same call -- but it is *not* included in heap/objects, which is why the
// heap budget cannot see a stack runaway. Verified rather than assumed: the
// figures above were read from the same metrics.Read, and heap/objects never
// crossed its budget on the run that died of stack overflow.
//
// Both budgets are measured, not picked. TestFuzzSeedCorpusFitsTheMemoryCeiling
// samples every seed at fuzzMemoryMeasurePeriod and logs the high-water mark for
// each. Re-take them by running that test; the numbers it logs are these numbers,
// taken through the same code path the fuzzer runs, so the two cannot drift. On
// the corpus as this is written -- 2,306 unique schemas x 5 config bytes = 11,530
// seeds, 690 local files plus 2,265 external test groups:
//
//	heap    123.3 / 118.5 / 116.4 / 126.2 / 103.7 / 111.5 / 126.3 / 114.2 MiB
//	        over eight runs
//	stack   12.0 MiB on every one of three runs, to the tenth of a MiB
//
// Always the same seed for both -- ../testdata/schemas/adversarial/deep/deep-not-2000.json,
// the 2000-deep `not`, which is also the slowest under fuzzSeedBudget at 374ms.
// The heap figure moves with GC timing and the winning config byte varies with
// it; the stack figure does not move at all, because a stack is as deep as the
// recursion and nothing collects it early. Call the legitimate worst case 125 MiB
// of heap and 12 MiB of stack.
//
// The margins are deliberately not larger, because the two ways of being wrong
// here do not cost the same. Tripping on a legitimate mutation writes a
// reproducing file with the input and the config byte in it, and someone reads it
// and says "that is just a very large schema" in five minutes. Not tripping is a
// `fatal error`, which cost hours twice. So the asymmetry is priced in and both
// ceilings sit low.
//
// 512 MiB of heap is four times the measured worst case, and well under what a
// GitHub runner can give one worker: ~7 GB usable across the GOMAXPROCS workers
// `make fuzz` starts, so roughly 1.75 GB each on the standard 4-core image.
// Tripping there leaves about 1.25 GB of headroom -- which matters more than it
// looks, because a tripped goroutine is abandoned and goes on allocating; see
// fuzzMemoryGate.
//
// 64 MiB of stack is 5.3 times its measured worst case, in the same spirit. The
// margin has to absorb one thing the heap figure does not have: heap/stacks is
// jittery, because growing a goroutine stack means allocating the bigger one and
// copying into it, so both are counted for the duration of the copy and the
// figure can read up to twice the stack that actually exists. The plant showed it
// non-monotonic -- 768 MiB, then 256, then 512 -- for exactly that reason. The
// 12 MiB measurement already includes any such transient, since it is the highest
// value ever sampled, so 5.3x is 5.3x of the jittery figure and not of a smooth
// one. Headroom the other way is wider still: 64 MiB is a sixteenth of the
// runtime's 1 GB per-goroutine limit, so a stack runaway is named four doublings
// before the runtime would throw -- measured as t=0.2s against t=2.0s.
var fuzzMemoryBudgets = [fuzzMemoryDimensions]fuzzMemoryDimension{
	fuzzMemoryHeap: {
		what:   "heap",
		metric: "/memory/classes/heap/objects:bytes",
		budget: 512 << 20,
	},
	fuzzMemoryStack: {
		what:   "goroutine stack",
		metric: "/memory/classes/heap/stacks:bytes",
		budget: 64 << 20,
	},
}

// fuzzMemorySamples is the buffer shape metrics.Read wants, in the dimension
// order above so that index d is dimension d.
func fuzzMemorySamples() []metrics.Sample {
	buf := make([]metrics.Sample, fuzzMemoryDimensions)
	for d := range fuzzMemoryBudgets {
		buf[d].Name = fuzzMemoryBudgets[d].metric
	}
	return buf
}

// fuzzMemorySamplePeriod is how often the sampler looks while the fuzzer is
// running, and fuzzMemoryMeasurePeriod is the denser period the seed gate uses
// when it is measuring rather than guarding.
//
// 5ms is set by how fast a real runaway climbs, not by how long an execution
// takes. Both plants climb at about the same speed: reverting the cyclicNodeName
// call in tupleItemCheckFor and replaying
// testdata/schemas/adversarial/cycle/items-self-ref-sibling.json puts on 514 MiB
// of heap in 0.69s, and reverting the one in the ref-name arm and replaying
// testdata/schemas/adversarial/cycle/typeschema-ref-self-def.json puts on 128 MiB
// of stack in 0.3s and 512 MiB in 1.2s. A 5ms sampler gets on the order of a
// hundred looks either way and cannot miss either. The cost is one background
// goroutine doing 200 metrics.Read calls a second, which is not per execution;
// see the throughput note on fuzzMemoryGate for what is.
//
// Sampling can only ever catch what it is looking at, so a spike that opens and
// closes between two samples is invisible to the gate. That is deliberate rather
// than a hole. Nothing brief can get near the budget in the first place: at the
// several GiB/s a Go program can allocate small objects at, half a gigabyte
// takes hundreds of milliseconds, which is tens of samples. The gate is for
// growth that does not stop, and growth that does not stop is by construction
// slow enough to be seen.
//
// The same argument covers the measuring end. TestFuzzSeedCorpusFitsTheMemoryCeiling
// samples at 100us, and most seeds finish in less than that and are never
// sampled at all -- but a seed that finishes in 50us cannot have allocated more
// than a few hundred KiB, so the high-water mark it reports is not hiding
// anything that matters at this scale.
const (
	fuzzMemorySamplePeriod  = 5 * time.Millisecond
	fuzzMemoryMeasurePeriod = 100 * time.Microsecond
)

// fuzzMemoryInFlight is the one execution the sampler is currently watching.
type fuzzMemoryInFlight struct {
	// baseline is each dimension read just before the body was started, and
	// peak the highest growth above it the sampler saw, for the measuring test.
	baseline [fuzzMemoryDimensions]uint64
	peak     [fuzzMemoryDimensions]atomic.Uint64
	cfgBits  uint8
	data     []byte
	trip     chan *fuzzMemoryTripError
}

// fuzzMemoryTripError is what the gate panics with. An error rather than a bare
// string so that both printers render the whole message: the runtime's own
// panic printer calls Error() (preprintpanics), and testing's fuzzing branch
// formats the recovered value with %s.
type fuzzMemoryTripError struct{ msg string }

func (e *fuzzMemoryTripError) Error() string { return e.msg }

// fuzzBodyPanicError carries a panic raised by the pipeline itself back to the
// goroutine the harness is watching.
//
// The gate has to run the pipeline on a second goroutine -- see fuzzMemoryGate
// -- and a panic there would kill the process outright instead of being
// reported, which would break the one property FuzzGenerate exists to test. So
// the body's panic is recovered and re-raised on the calling goroutine. The
// original stack is captured at the recover and carried along, because
// re-raising loses it and because the stack testing appends is the *recovering*
// goroutine's, which is not the interesting one.
type fuzzBodyPanicError struct {
	value any
	stack []byte
}

func (e *fuzzBodyPanicError) Error() string {
	return fmt.Sprintf("%v\n\n%s\n[stack above is the pipeline goroutine's, captured where the panic was "+
		"recovered and re-raised by the fuzz memory gate]", e.value, e.stack)
}

var (
	// fuzzMemoryInFlightSlot is nil whenever nothing is being watched. There is
	// exactly one at a time: a fuzz worker runs one input at a time in one
	// goroutine, and the seed tests run their seeds in sequence.
	fuzzMemoryInFlightSlot atomic.Pointer[fuzzMemoryInFlight]

	// fuzzMemoryWake lets the sampler block instead of spinning when nothing is
	// armed, so the goroutine costs nothing in a `go test ./...` run that never
	// touches the fuzz body.
	fuzzMemoryWake = make(chan struct{}, 1)

	// fuzzMemorySamplerOnce starts the sampler on first use. It is never
	// stopped; one parked goroutine for the life of the test binary.
	fuzzMemorySamplerOnce sync.Once

	// fuzzMemoryPeriodNanos is fuzzMemorySamplePeriod, in a form the measuring
	// test can turn down for its own run.
	fuzzMemoryPeriodNanos atomic.Int64

	// fuzzMemoryPoison is set by the first trip and never cleared. See
	// fuzzMemoryPoisonState.
	fuzzMemoryPoison atomic.Pointer[fuzzMemoryPoisonState]
)

// fuzzMemoryPoisonState is what a process remembers after it has tripped once,
// and it is what makes the finding survive minimization.
//
// The first trip abandons a goroutine that goes on allocating -- see
// fuzzMemoryGate -- so the process is already dying, on a measured budget of
// about (worker share - fuzzMemoryBudget) / 750 MiB per second, which on a
// GitHub runner is under two seconds. Minimization runs next, in that same
// worker, and it is not a formality: the coordinator re-runs the original and
// then hundreds of byte-removal candidates. Letting them all through means the
// worker dies mid-minimization, and then internal/fuzz reports
//
//	fuzzing process hung or terminated unexpectedly while minimizing: EOF
//
// and drops the crasherMsg entirely -- $GOROOT/src/internal/fuzz/fuzz.go only
// attaches it when the minimize error is nil. That is measured, not predicted:
// it is what this gate did before the poison existed, on a synthetic target
// whose runaway allocates at the same rate the real one does.
//
// So after the first trip the process stops searching and becomes a witness. It
// replays the trip for the exact input that caused it, and does nothing at all
// for any other input. Minimization then finds that the original reproduces and
// that no smaller candidate does, which is the answer that makes the coordinator
// write the *original* input with the *original* message. Unminimized, and
// reproducing, which is the right way round: #348's saved artifact was minimized
// and did not reproduce, and that is what cost the hours.
//
// The cost is that a tripped worker cannot find anything else. It does not need
// to: the coordinator stops the whole run on a crash anyway.
type fuzzMemoryPoisonState struct {
	cfgBits uint8
	data    []byte // copied; the harness reuses the buffer it hands in
	err     *fuzzMemoryTripError
}

// fuzzMemoryRead fills the caller's own sample buffer, which is why the read
// allocates nothing per call.
func fuzzMemoryRead(buf []metrics.Sample) {
	metrics.Read(buf)
}

// fuzzMemorySampler watches whatever is armed and trips it once any dimension
// has grown past its budget.
func fuzzMemorySampler() {
	buf := fuzzMemorySamples()
	for {
		inFlight := fuzzMemoryInFlightSlot.Load()
		if inFlight == nil {
			<-fuzzMemoryWake
			continue
		}

		fuzzMemoryRead(buf)
		tripped := false
		for d := range fuzzMemoryBudgets {
			now := buf[d].Value.Uint64()
			if now <= inFlight.baseline[d] {
				continue
			}
			grown := now - inFlight.baseline[d]
			for {
				peak := inFlight.peak[d].Load()
				if grown <= peak || inFlight.peak[d].CompareAndSwap(peak, grown) {
					break
				}
			}
			if grown > fuzzMemoryBudgets[d].budget && !tripped {
				tripped = true
				// Disarm first. The body goroutine is about to be abandoned and
				// will hold this dimension above its baseline indefinitely, so a
				// still-armed slot would trip on every later sample.
				fuzzMemoryInFlightSlot.CompareAndSwap(inFlight, nil)
				select {
				case inFlight.trip <- fuzzMemoryTrip(inFlight, d, now, grown):
				default:
				}
			}
		}
		if tripped {
			// The slot is disarmed, so going round again parks the sampler. The
			// loop above still finished, so the other dimension's peak is
			// recorded too and the measuring test can report both.
			continue
		}

		time.Sleep(time.Duration(fuzzMemoryPeriodNanos.Load()))
	}
}

// fuzzMemoryTrip builds the message. Everything needed to reproduce is in it:
// the config byte, because fuzzConfig turns it into a whole generator.Config and
// #348 only reproduced under three of the five seed bytes, and the input itself,
// because a panic during a `go test ./...` seed replay has no crasher file to
// point at.
func fuzzMemoryTrip(inFlight *fuzzMemoryInFlight, d int, now, grown uint64) *fuzzMemoryTripError {
	dim := fuzzMemoryBudgets[d]
	ends := "`fatal error: out of memory`"
	if d == fuzzMemoryStack {
		ends = "`fatal error: stack overflow`"
	}
	return &fuzzMemoryTripError{msg: fmt.Sprintf(
		"fuzz memory gate: this input grew the %s by %s (baseline %s, now %s), over the %s budget for %s, "+
			"with cfgBits 0x%02X on %d bytes of input:\n%s\n\n"+
			"The gate panicked deliberately, before the runtime could reach %s, which is unrecoverable and "+
			"which the fuzz harness reports as an unattributable \"process hung or terminated unexpectedly\" "+
			"with a truncated, non-reproducing artifact -- see issue #348. Treat this as an unbounded "+
			"recursion in parse -> generate -> emit and fix the pipeline; do not raise the budget.",
		dim.what, fuzzHumanBytes(grown), fuzzHumanBytes(inFlight.baseline[d]), fuzzHumanBytes(now),
		fuzzHumanBytes(dim.budget), dim.metric, inFlight.cfgBits, len(inFlight.data),
		fuzzQuoteInput(inFlight.data), ends)}
}

func fuzzHumanBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// fuzzQuoteInput renders the input for the message. Quoted, because a mutation
// is arbitrary bytes and may well not be printable, and clipped, because a
// mutation may also be a megabyte long and the reproducer of record is the file
// the harness writes.
func fuzzQuoteInput(data []byte) string {
	const max = 1024
	if len(data) <= max {
		return fmt.Sprintf("%q", data)
	}
	return fmt.Sprintf("%q ... (%d bytes truncated in this message; the harness has written the whole input)",
		data[:max], len(data)-max)
}

// fuzzMemoryGate runs body under the memory watchdog, panicking with a
// *fuzzMemoryTripError if it grows the heap past fuzzMemoryBudget.
//
// Why body runs on its own goroutine. The panic has to happen on the goroutine
// the harness is watching -- testing recovers there, and a panic anywhere else
// takes the process down and lands back in the unattributable case this whole
// file exists to escape. But the runaway is deep inside generator recursion,
// which checks nothing and can only be stopped by returning, so it cannot be the
// goroutine that panics. Splitting them is the only arrangement that leaves the
// panic where the harness can see it.
//
// The cost of that arrangement, stated rather than hidden. A goroutine that is
// tripped is *abandoned*: Go cannot stop a goroutine from outside, so it keeps
// allocating until the process dies. That is why the budget is growth above the
// baseline at arming rather than an absolute ceiling -- an absolute one would
// stay breached for ever after the first trip and blame every later input -- and
// it is why fuzzMemoryPoisonState exists, because minimization runs in that same
// dying worker and is the step where the message would otherwise be lost.
//
// Throughput. Per execution this adds one goroutine, two channel operations, one
// metrics.Read and a few atomics; the sampler is one goroutine for the whole
// process, not one per execution, so it does not scale with the exec rate.
// BenchmarkFuzzMemoryGate/overhead measures the fixed part against an empty body
// at 1.09-1.25 us/op, call it 1.2us. Against the 2,100-4,200 execs/sec the last
// nightly managed -- 240-480us per execution -- that is 0.3% to 0.5% of the
// search budget, or on the order of 10-20 execs/sec.
//
// Watching the stack as well as the heap added nothing to that: 1.13us with both
// dimensions against 1.09-1.25us with the heap alone, and 2.98-3.01us on the
// rejected end against 2.98-3.73us. Both figures come out of the same
// metrics.Read, so the second dimension costs one more slot in the sample buffer
// and one more comparison, and neither is measurable.
//
// It is worth stating where that 1.2us is not negligible: an input json.Unmarshal
// rejects at the first byte costs 0.41-0.43us ungated, and the gate takes it to
// 3.0-3.7us -- more than the fixed cost, because the body goroutine's own
// allocation and GC interaction land on top. That end does not set the exec rate
// (a run made only of rejected inputs would be doing hundreds of thousands of
// execs a second, not four thousand) but it is the honest worst case. On anything
// that reaches the generator the gate is unmeasurable:
// BenchmarkFuzzMemoryGate/generated came out 2.84ms gated against 2.87ms ungated
// on one run and 3.19ms against 3.25ms on another -- the gated side landed below
// the ungated side both times, which is to say it is noise.
//
// Checked against the thing itself rather than only against a benchmark: two
// 180-second `-fuzz FuzzGenerate` runs on the same machine, same corpus, two
// workers each, one binary with the gate and one with fuzzOnce calling
// fuzzPipeline directly, both from a cold fuzz cache. 1,656,062 execs ungated
// against 1,608,292 gated -- 2.9%, at 9,200 execs/sec. Those binaries were built
// with `go test -c`, so they run without coverage instrumentation and about
// twice as fast per exec as the nightly's workers do; the cost is a fixed number
// of microseconds per execution, so at the nightly's 240-480us it is closer to
// 1%. No run of either has produced a spurious trip.
//
// A later repeat of that A/B is worth recording for what it says about the gate
// rather than about the cost: the ungated binary died at 1m52s with "fuzzing
// process hung or terminated unexpectedly: exit status 2" -- the nightlies'
// message, verbatim -- while the gated binary completed its 180 seconds and
// 1,573,502 execs. The input behind it, recovered and replayed under the gated
// binary, was a stack runaway through patternProperties that the gate names in
// 0.08s. That is the whole argument for this file in one pair of runs.
func fuzzMemoryGate(cfgBits uint8, data []byte, body func()) {
	if poison := fuzzMemoryPoison.Load(); poison != nil {
		// Already tripped once. Replay it for the input that caused it and run
		// nothing otherwise; fuzzMemoryPoisonState says why.
		if poison.cfgBits == cfgBits && bytes.Equal(poison.data, data) {
			panic(poison.err)
		}
		return
	}

	fuzzMemorySamplerOnce.Do(func() {
		fuzzMemoryPeriodNanos.CompareAndSwap(0, int64(fuzzMemorySamplePeriod))
		go fuzzMemorySampler()
	})

	buf := fuzzMemorySamples()
	fuzzMemoryRead(buf)
	inFlight := &fuzzMemoryInFlight{
		cfgBits: cfgBits,
		data:    data,
		trip:    make(chan *fuzzMemoryTripError, 1),
	}
	for d := range fuzzMemoryBudgets {
		inFlight.baseline[d] = buf[d].Value.Uint64()
	}

	done := make(chan *fuzzBodyPanicError, 1)
	fuzzMemoryInFlightSlot.Store(inFlight)
	select {
	case fuzzMemoryWake <- struct{}{}:
	default:
	}

	go func() {
		var raised *fuzzBodyPanicError
		defer func() {
			if r := recover(); r != nil {
				raised = &fuzzBodyPanicError{value: r, stack: debug.Stack()}
			}
			done <- raised
		}()
		body()
	}()

	select {
	case raised := <-done:
		fuzzMemoryInFlightSlot.CompareAndSwap(inFlight, nil)
		fuzzMemoryRecordPeaks(inFlight)
		if raised != nil {
			panic(raised)
		}
	case trip := <-inFlight.trip:
		fuzzMemoryRecordPeaks(inFlight)
		fuzzMemoryPoison.Store(&fuzzMemoryPoisonState{
			cfgBits: cfgBits,
			data:    bytes.Clone(data),
			err:     trip,
		})
		panic(trip)
	}
}

// fuzzMemoryLastPeak is the growth the sampler attributed to the execution that
// just finished, per dimension. Only the measuring test reads it, and only
// between executions.
var fuzzMemoryLastPeak [fuzzMemoryDimensions]atomic.Uint64

func fuzzMemoryRecordPeaks(inFlight *fuzzMemoryInFlight) {
	for d := range fuzzMemoryBudgets {
		fuzzMemoryLastPeak[d].Store(inFlight.peak[d].Load())
	}
}

// TestFuzzSeedCorpusFitsTheMemoryCeiling runs every fuzz seed through the fuzz
// body with the sampler turned up, and fails on any seed that trips the gate.
//
// This is the seed half, and it is a different job from the watchdog inside
// fuzzOnce even though they share the machinery. The watchdog catches a bad
// *mutation*, during a nightly fuzz run, and is the only thing that can: nobody
// has the mutation in advance. This test catches a bad *seed*, on the pull
// request, before a run starts -- and a bad seed is worse than a bad mutation,
// because `go test -fuzz` replays the entire corpus for baseline coverage before
// it mutates anything, so one seed the worker cannot survive stops the run there
// and no fuzzing happens at all. That is the shape of issue #233, and the reason
// TestFuzzSeedCorpusFitsTheWorkerDeadline exists for time. Both halves are
// warranted for the same reason both halves of the time story were.
//
// It is not hypothetical either: the seeds under
// testdata/schemas/adversarial/cycle/ are the reproducers for both shapes --
// items-self-ref-sibling.json for the heap one and typeschema-ref-self-def.json
// for the stack one -- so a regression of either guard is a *seed* that recurses
// without bound, and this test is what names it.
//
// It also publishes the measurements fuzzMemoryBudgets is set from, one per
// dimension. The numbers it logs are that comment's numbers, taken through the
// same code path the fuzzer runs, which is the only way the two cannot drift.
func TestFuzzSeedCorpusFitsTheMemoryCeiling(t *testing.T) {
	em, err := emitter.New()
	if err != nil {
		t.Fatalf("emitter.New: %v", err)
	}

	// Warm the sampler up and then turn it up, so that seeds measured in
	// microseconds are still looked at. Restored afterwards: the fuzz target and
	// the deadline test run in this same binary and should not pay for it.
	fuzzMemoryGate(0, nil, func() {})
	previous := fuzzMemoryPeriodNanos.Swap(int64(fuzzMemoryMeasurePeriod))
	defer fuzzMemoryPeriodNanos.Store(previous)

	type sample struct {
		origin string
		bits   uint8
		grew   uint64
	}
	var worst [fuzzMemoryDimensions]sample
	seeds := 0
	var tripped *sample
	var trippedMsg string

	local, external, err := fuzzSeedCorpus(func(origin string, schema []byte) {
		if tripped != nil {
			return // see the FailNow below: the run is over, drain the walk
		}
		for _, bits := range fuzzSeedCfgBits {
			seeds++
			func() {
				defer func() {
					r := recover()
					if r == nil {
						return
					}
					trip, ok := r.(*fuzzMemoryTripError)
					if !ok {
						panic(r) // a pipeline panic is FuzzGenerate's finding, not this test's
					}
					tripped = &sample{origin: origin, bits: bits}
					trippedMsg = trip.Error()
				}()
				fuzzOnce(em, bits, schema)
			}()
			for d := range fuzzMemoryBudgets {
				if grew := fuzzMemoryLastPeak[d].Load(); grew > worst[d].grew {
					worst[d] = sample{origin, bits, grew}
				}
			}
			if tripped != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("sampled %d seeds at %v (%d local files, %d external test groups)",
		seeds, fuzzMemoryMeasurePeriod, local, external)
	for d := range fuzzMemoryBudgets {
		t.Logf("  %-16s high-water %9s (budget %s), %s with cfgBits 0x%02X",
			fuzzMemoryBudgets[d].what, fuzzHumanBytes(worst[d].grew),
			fuzzHumanBytes(fuzzMemoryBudgets[d].budget), worst[d].origin, worst[d].bits)
	}

	if tripped != nil {
		// FailNow, not Errorf-and-continue as the deadline test does. A tripped
		// execution leaves a goroutine that cannot be stopped still recursing,
		// so carrying on would measure every later seed against a baseline that
		// is climbing underneath it -- and would eventually take the binary down
		// with the `fatal error` this gate exists to pre-empt.
		t.Fatalf("seed %s with cfgBits 0x%02X tripped the memory gate.\n%s",
			tripped.origin, tripped.bits, trippedMsg)
	}
}

// BenchmarkFuzzMemoryGate measures what the gate costs the fuzzer. The numbers
// in fuzzMemoryGate's comment come from here, and re-taking them is running it.
//
// Three cases, because one would mislead. `overhead` is the gate around an empty
// body and is the only stable figure: it is the fixed scheduling cost, the thing
// that does not depend on what the fuzzer is doing. `generated` and `rejected`
// are the two ends of the real input distribution -- a schema that goes all the
// way through parse -> generate -> emit, and a mutation json.Unmarshal refuses at
// the first byte -- and they bracket where that fixed cost lands as a
// percentage.
func BenchmarkFuzzMemoryGate(b *testing.B) {
	em, err := emitter.New()
	if err != nil {
		b.Fatalf("emitter.New: %v", err)
	}

	noop := []byte(`{"type":"object"}`)
	b.Run("overhead/gated", func(b *testing.B) {
		for b.Loop() {
			fuzzMemoryGate(0, noop, func() {})
		}
	})
	b.Run("overhead/ungated", func(b *testing.B) {
		body := func() {}
		for b.Loop() {
			body()
		}
	})

	cases := []struct {
		name string
		data []byte
	}{
		{"generated", []byte(`{"type":"object","properties":{"a":{"type":"string","minLength":1},` +
			`"b":{"type":"array","items":{"type":"integer"}},"c":{"$ref":"#/$defs/d"}},` +
			`"required":["a"],"$defs":{"d":{"type":"boolean"}}}`)},
		{"rejected", []byte(`{"type":"object",`)},
	}

	for _, c := range cases {
		b.Run(c.name+"/gated", func(b *testing.B) {
			for b.Loop() {
				fuzzOnce(em, 0x1F, c.data)
			}
		})
		b.Run(c.name+"/ungated", func(b *testing.B) {
			for b.Loop() {
				fuzzPipeline(em, 0x1F, c.data)
			}
		})
	}
}
