//go:build unix

package tests

import (
	"os"
	"syscall"
)

// The three things this suite needs from the operating system to look after the
// directories it leaves in /tmp: a lock that says who is using the shared build
// cache, the free space on the volume it lives on, and -- for the sweep's own
// guards -- a file whose reader blocks. All are unix calls, and each has a
// stated fallback in cachefs_other_test.go for a platform that does not offer
// it.

// cacheLockingSupported says the lock below is a real answer, which is what
// lets a run delete the shared cache when it is the last one holding it.
const cacheLockingSupported = true

// flockShared takes a shared advisory lock on f, blocking until it is granted.
//
// Blocking is safe here because the only thing that ever holds the exclusive
// lock holds it across a single rename and then lets go, so the wait is
// microseconds. It is not a lock protecting the cache's *contents* -- Go's
// build cache needs no such thing -- only one that answers "is a run using this
// directory", which cannot be answered by looking at the directory.
func flockShared(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_SH) == nil
}

// flockExclusiveNB tries to take the exclusive lock without waiting. False
// means a live run holds the shared lock, which is the answer that keeps a
// cache from being deleted underneath it.
func flockExclusiveNB(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// makeBlockingFile creates a named pipe at path, and reports whether it could.
//
// It is fixture machinery rather than harness machinery. A generated program
// reads its fixture with os.ReadFile, and an os.ReadFile of a FIFO blocks in the
// open until a writer arrives -- which is what lets a guard hold a *real* work
// directory genuinely mid-use, module compiled and program running inside it,
// at an instant it chooses rather than an instant it hopes for. Sleeping for a
// build that usually takes three seconds is the alternative, and a guard that
// silently stops covering the thing it names on a loaded machine is not a
// guard.
func makeBlockingFile(path string) bool { return syscall.Mkfifo(path, 0o600) == nil }

// freeBytes reports the space available to this user on the filesystem holding
// path.
//
// Bavail rather than Bfree: the reserved blocks a filesystem keeps for root are
// not space this run can write into, and counting them would make the
// precondition report headroom that does not exist.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
