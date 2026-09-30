package fuzzmemory

import (
	"testing"

	"github.com/mgilbir/schemagen/tests/internal/testsupport"
)

// TestFuzzSeedCorpusFitsTheMemoryCeiling runs every fuzz seed through the fuzz
// body with the sampler turned up, and fails on any seed that trips the memory
// gate. The body, and the account of why it exists, are
// testsupport.FuzzSeedCorpusFitsTheMemoryCeiling: it reaches into the gate's own
// state, which FuzzGenerate and the deadline sweep reach only through FuzzOnce.
//
// It is a binary of its own because it replays the whole seed corpus, as
// tests/fuzzdeadline does; together with FuzzGenerate's replay and the backstop
// sweep, that is what took tests/fuzz to go test's ten-minute timeout on a
// four-vCPU runner when they shared one.
func TestFuzzSeedCorpusFitsTheMemoryCeiling(t *testing.T) {
	testsupport.FuzzSeedCorpusFitsTheMemoryCeiling(t)
}

// BenchmarkFuzzMemoryGate measures what the memory gate costs the fuzzer; see
// testsupport.FuzzMemoryGateBenchmark.
func BenchmarkFuzzMemoryGate(b *testing.B) {
	testsupport.FuzzMemoryGateBenchmark(b)
}
