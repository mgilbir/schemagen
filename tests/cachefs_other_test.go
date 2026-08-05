//go:build !unix

package tests

import (
	"errors"
	"os"
)

// The fallbacks for a platform with neither flock nor statfs, and what each one
// costs there.
//
// cacheLockingSupported false says nobody can claim the shared cache, and the
// callers read it: no run deletes the cache on its way out, and the sweep falls
// back to judging it by age alone -- the behaviour #136 shipped, over one
// directory instead of one per process. The alternative reading, that an
// unclaimable cache is a claimed one, would make it immortal and fill the
// volume, which is the failure being fixed.
const cacheLockingSupported = false

func flockShared(*os.File) bool { return false }

func flockExclusiveNB(*os.File) bool { return false }

// freeBytes cannot answer here. The caller reports the precondition as
// unchecked rather than treating an unknown as enough space.
func freeBytes(string) (uint64, error) {
	return 0, errors.New("free space is not measurable on this platform")
}
