//go:build unix

package tests

import (
	"os"
	"syscall"
)

// The two things this suite needs from the operating system to look after the
// shared build cache: a lock that says who is using it, and the free space on
// the volume it lives on. Both are unix calls, and both have a stated fallback
// in cachefs_other_test.go for a platform that does not offer them.

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
