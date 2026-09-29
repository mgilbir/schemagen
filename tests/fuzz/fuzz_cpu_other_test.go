//go:build !unix

package fuzz

// cpuTimeAvailable is false here: getrusage(RUSAGE_SELF) is a unix call and
// there is no portable equivalent that stays correct on every platform this
// could build for. Falling back to the wall clock instead would silently
// reintroduce the exact failure this file exists to remove -- the wall clock
// counts time spent waiting for a free CPU, not only time spent using one --
// so TestFuzzSeedCorpusFitsTheWorkerDeadline skips on a platform where it
// cannot make that measurement, rather than passing on one that looks like it
// but is not.
//
// This never applies to CI: every workflow under .github/workflows runs
// ubuntu-latest exclusively.
const cpuTimeAvailable = false

// cpuNanos is never called: every caller checks cpuTimeAvailable first and
// skips instead. It exists so this file and fuzz_cpu_unix_test.go present the
// same two names and the caller does not need its own build tag.
func cpuNanos() int64 {
	panic("cpuNanos: not available on this platform; check cpuTimeAvailable first")
}
