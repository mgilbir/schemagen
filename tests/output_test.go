package tests

import "strings"

// programOutput returns the output of a generated test program with Go
// toolchain progress lines removed.
//
// These tests build a throwaway module and run it, capturing combined output.
// When the module cache is cold, the build prints lines like
//
//	go: downloading github.com/mgilbir/goecma262 v0.0.0-...
//
// onto the same stream as the program's own output, so an exact-match
// assertion against "PASS" (or "VALID") fails purely because of cache state.
// Locally the cache is warm and the tests pass; on a fresh CI runner the first
// generated build to need a dependency picks up the noise, which made the
// outcome depend on which package happened to run first.
func programOutput(out []byte) string {
	lines := strings.Split(string(out), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "go: ") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
