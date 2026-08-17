package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The guards for `schemagen --version`.
//
// They run the built binary, for the reason main_test.go gives: the version
// line crosses a stream boundary, and an in-process test that captures
// cmd.OutOrStdout() sees whichever copy cobra wrote there whether or not that
// is the stream a terminal reads. #322 was exactly that, one stream out.
//
// The second guard is the one that matters. A test that only runs the binary as
// built proves that *some* string is printed, and it would go on passing if the
// -X path stopped reaching the variable -- the binary would quietly report the
// pseudo-version or "dev" forever, and the release build would be the one that
// nobody looks at until a bug report quotes the wrong version. So the stamp is
// applied and then read back out of a real process.

// runVersion runs the given binary with the given arguments and returns its
// stdout, insisting it exited 0 and said nothing on stderr. Both are part of
// the claim: --version is a request that succeeded, and its answer is output
// rather than a diagnostic.
func runVersion(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("schemagen %s: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("schemagen %s wrote to stderr, which is where this command's diagnostics go:\n%s",
			strings.Join(args, " "), stderr.String())
	}
	return stdout.String()
}

// versionReported returns the version out of a "schemagen version X" line,
// insisting the output is that one line and nothing else.
func versionReported(t *testing.T, out string) string {
	t.Helper()
	line, rest, ok := strings.Cut(out, "\n")
	if !ok {
		t.Fatalf("the version output is not a terminated line: %q", out)
	}
	if rest != "" {
		t.Fatalf("the version output carries more than the one line:\n%s", out)
	}
	const prefix = "schemagen version "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("the version line does not name the tool: got %q, want it to start with %q", line, prefix)
	}
	v := strings.TrimPrefix(line, prefix)
	if v == "" {
		t.Fatalf("the version line names the tool and no version: %q", line)
	}
	return v
}

// The binary as the plain `go build` in schemagenBinary produces it: no linker
// stamp, so this is what resolveVersion falls back to. It asserts the shape of
// the line rather than a particular version, because the answer is a property
// of how the binary was built -- a pseudo-version in a checkout, "dev" outside
// one -- and pinning either would make this a test of the build environment.
func TestTheVersionFlagNamesTheToolAndAVersion(t *testing.T) {
	v := versionReported(t, runVersion(t, schemagenBinary(t), "--version"))

	// The two strings a build reports when it could not identify itself, but
	// which are not what devVersion says. "(devel)" is the toolchain's
	// placeholder for a main module with no version; the empty stamp would
	// print the tool's name and a blank, which versionReported already refuses.
	if v == "(devel)" {
		t.Errorf("--version reports the toolchain's placeholder %q rather than a version or the honest default", v)
	}
	// #322's shape, one command over: the line must be printed once.
	out := runVersion(t, schemagenBinary(t), "--version")
	if got := strings.Count(out, "schemagen version "); got != 1 {
		t.Errorf("the version line appears %d times, want 1:\n%s", got, out)
	}

	// cobra gives --version the -v shorthand on the root command, which claims
	// no other. `schemagen generate -v` is still --verbose; a root-level
	// persistent flag taking -v is what would break this half without touching
	// the other.
	if short := versionReported(t, runVersion(t, schemagenBinary(t), "-v")); short != v {
		t.Errorf("schemagen -v reports %q and schemagen --version reports %q; they are the same flag", short, v)
	}
}

// The mechanism the Makefile's build and install targets depend on: -X reaching
// the variable resolveVersion reads first. Built with an override and read back
// out of the process, because that is the only thing that can tell a stamp that
// arrived from one that was silently dropped -- a wrong -X path is not an
// error, the linker simply sets nothing.
func TestTheLinkerStampIsWhatTheVersionFlagPrints(t *testing.T) {
	// Nothing any derivation would produce, so a stamp that never arrived
	// cannot be mistaken for one that did.
	const stamped = "v9.9.9-stamp-guard"

	path := filepath.Join(t.TempDir(), "schemagen")
	build := exec.Command("go", "build",
		// The same variable the Makefile's GO_LDFLAGS names. Spelled out here
		// rather than read from the Makefile: this guard is about the path from
		// an -X to the printed line, and the Makefile is checked by using it.
		"-ldflags", "-X github.com/mgilbir/schemagen/cmd/schemagen.version="+stamped,
		"-o", path, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building with a version stamp: %v\n%s", err, out)
	}

	if got := versionReported(t, runVersion(t, path, "--version")); got != stamped {
		t.Errorf("the stamped binary reports %q, want %q -- the -X did not reach the version variable", got, stamped)
	}
}

// The same question of the Makefile, which is what a release is actually built
// with. The guard above proves an -X can reach the variable; this one proves the
// one the release build passes does, and those are different claims: the
// Makefile spells an import path, a mistyped or renamed one is not an error, and
// the linker sets nothing. The failure is silent and lands at exactly the wrong
// moment -- the tagged binary reports "dev" or a commit hash, and nobody looks
// at a version they did not doubt.
func TestTheMakefileStampReachesTheBinary(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skipf("make is not installed: %v", err)
	}

	// VERSION overrides the git derivation, which is the documented way to
	// build a release, and lets this assert an exact string rather than
	// whatever the checkout happens to describe as.
	const stamped = "v9.9.9-makefile-guard"
	// Not the name the Makefile builds by default, so a guard run does not
	// leave a sentinel-versioned bin/schemagen behind for someone to pick up.
	const binary = "schemagen-version-guard"

	out, err := exec.Command("make", "build", "VERSION="+stamped, "BINARY="+binary).CombinedOutput()
	if err != nil {
		t.Fatalf("make build: %v\n%s", err, out)
	}
	path, err := filepath.Abs(filepath.Join("bin", binary))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })

	if got := versionReported(t, runVersion(t, path, "--version")); got != stamped {
		t.Errorf("the binary `make build VERSION=%s` produced reports %q; the Makefile's -X names something other than the version variable", stamped, got)
	}
}
