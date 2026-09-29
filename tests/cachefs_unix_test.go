//go:build unix

package tests

import "syscall"

// makeBlockingFile creates a named pipe at path, and reports whether it could.
//
// It is fixture machinery for the sweep's own guards. A generated program
// reads its fixture with os.ReadFile, and an os.ReadFile of a FIFO blocks in
// the open until a writer arrives -- which is what lets a guard hold a *real*
// work directory genuinely mid-use, module compiled and program running inside
// it, at an instant it chooses rather than an instant it hopes for. Sleeping
// for a build that usually takes three seconds is the alternative, and a guard
// that silently stops covering the thing it names on a loaded machine is not a
// guard. The lock and free-space halves this file used to hold live in
// internal/testgo, which every test binary in the module now shares.
func makeBlockingFile(path string) bool { return syscall.Mkfifo(path, 0o600) == nil }
