//go:build unix

package fuzz

import "syscall"

// cpuTimeAvailable says cpuNanos below is a real measurement, not a stand-in.
// See fuzz_cpu_other_test.go for the platforms where it is not one.
const cpuTimeAvailable = true

// cpuNanos is the CPU time -- user plus system -- this process has consumed
// since it started, read with getrusage(RUSAGE_SELF). TestFuzzSeedCorpusFitsTheWorkerDeadline
// times each seed by the delta between two calls to this instead of the wall
// clock, because RUSAGE_SELF counts only time the process's threads actually
// ran on a CPU: it does not grow while a thread is runnable but waiting for
// one, which is what the other packages `go test ./...` runs alongside this
// one add. It does grow for GC the seed's own allocation triggers, which is
// correct -- that is part of what the seed costs, not contention.
func cpuNanos() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		panic(err)
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}
