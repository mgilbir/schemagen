// Package testgo is how every test in this module runs the go command.
//
// Tests here compile a great deal of code nobody will build again: a generated
// package per golden file, per round-trip fixture, per external-suite group and
// per co-generated iteration. Where that output goes, and under which flags, is
// decided once, here, for every test binary in the module -- not per call site,
// which is how it drifted before. Only the external-suite and cogen harnesses
// used the shared cache; the ordinary `go run` and `go build` calls in the
// round-trip, golden and cmd/schemagen tests wrote into ~/.cache/go-build
// without -trimpath. One `go test ./tests` added about 3 GB there, and because
// every t.TempDir() is a fresh path, none of it was ever a cache hit. During the
// 2026-09-26 audit that cache reached 165 GB and the volume filled.
//
// Three things are fixed here:
//
//   - GOCACHE is the one shared, swept directory under the temp directory (see
//     CacheDir), claimed by an flock while any test binary is using it and
//     deleted by the last one out.
//   - GOFLAGS is -trimpath and nothing else. -trimpath is what makes the shared
//     cache worth sharing (see Env); and whatever GOFLAGS the developer's shell
//     carries -- -mod=vendor, -race, a build tag -- is a setting for the build
//     they asked for, not for a throwaway module in /tmp, where it either breaks
//     the build outright or changes what is being measured.
//   - GOWORK is off. A throwaway module is not part of anybody's workspace.
//
// A test binary that runs the go command must call Main from its TestMain, so
// the claim is taken before the first test and released after the last; Command
// panics otherwise, because a binary that never releases leaves a cache behind
// for the age-based sweep, which is the leak this exists to end.
// TestNoTestRunsTheGoToolDirectly holds every *_test.go file in the module to
// going through here.
package testgo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	// mainInstalled records that this test binary's TestMain went through Main.
	mainInstalled bool

	// processDirs are the directories MkdirProcessTemp made, removed by Main on
	// the way out.
	processDirsMu sync.Mutex
	processDirs   []string
)

// Main is the TestMain of every test binary that runs the go command: it claims
// the shared cache, runs the tests, removes this process's own directories, and
// hands the cache back -- deleting it when no other test binary still holds it.
// It does not return.
func Main(m *testing.M) {
	os.Exit(Run(m))
}

// Run is Main without the exit, for a TestMain that has more to do on the way
// out. The exit code is m.Run's.
func Run(m *testing.M) int {
	claimSharedCache()
	mainInstalled = true
	warmRuntimeBuild()
	code := m.Run()
	processDirsMu.Lock()
	for _, d := range processDirs {
		os.RemoveAll(d)
	}
	processDirs = nil
	processDirsMu.Unlock()
	releaseSharedCache()
	return code
}

// warmRuntimeBuild builds the runtime module, and with it the standard library
// packages and the two third-party modules the code it imports needs, into the
// shared cache before the first test runs.
//
// Every generated package the tests compile imports the runtime, so the first
// compile in a cold cache builds all of that as well as the package. The tests
// bound a compile at 30 seconds, and a `go test ./...` starts some twenty test
// binaries at once on a cold cache: the first compile of each is the one that
// paid, on a busy machine that is the one that ran out of time, and it failed
// with "signal: killed" on a build that takes a second when the cache is warm
// (tests/golden, TestCompile, the first golden file). The bound is right for a
// compile, and stays; the cost of the shared code moves out from under it.
//
// It is best effort: a failure here is reported by the first test that needs the
// build, in the words of the build.
func warmRuntimeBuild() {
	dir, err := RuntimeDir()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := Command(ctx, dir, "build", "./...")
	_ = cmd.Run()
}

// Command returns a go command -- `go <args...>` run in dir -- in the
// environment Env describes. ctx bounds it like exec.CommandContext.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	requireMain()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = Env()
	return cmd
}

// MakeCommand returns `make <args...>` run in dir, in the same environment:
// the Makefile's recipes run the go tool, and a test driving one is a go build
// like any other.
func MakeCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	requireMain()
	cmd := exec.CommandContext(ctx, "make", args...)
	cmd.Dir = dir
	cmd.Env = Env()
	return cmd
}

// Env is the environment every command a test runs that reaches the go tool
// must use. Command and MakeCommand set it.
//
// It is the caller's environment with three variables replaced:
//
//   - GOCACHE is the shared cache (CacheDir). The code these builds compile is
//     generated, unique to one test case, and has no business in the
//     developer's own cache.
//   - GOFLAGS is exactly -trimpath. A build without it records the absolute
//     directory of its source in what it produces, so the action ID takes in
//     the temp directory the test happened to draw -- and every test draws a
//     fresh one. Two runs compiling byte-identical source then share nothing:
//     measured at +2 MB of new cache content for a second copy of a ten-line
//     program in a different directory, and +0 with -trimpath. Over the
//     external suite's ~27,000 compilations that is the difference between one
//     cache and a cache per run: two concurrent full runs peaked at 1.9G with
//     it, against 34.6G and 37.1G without. It is set through GOFLAGS rather
//     than on each command line so no call site can forget it, and GOFLAGS
//     flags a subcommand does not take are ignored by the go command, so it is
//     safe for every subcommand. Whatever GOFLAGS the caller had is dropped, for
//     the reason the package comment gives. Nothing measured anywhere depends on
//     the paths -trimpath erases; the only thing that gets shorter is the file
//     name in a panic trace from a generated program.
//   - GOWORK is off.
//
// Every other variable -- GOTOOLCHAIN, GOEXPERIMENT, GOPROXY, TMPDIR -- is the
// caller's, deliberately: those choose which Go the tests are about, and a run
// under GOTOOLCHAIN=go1.25.5 has to compile its generated code with go1.25.5.
func Env() []string {
	requireMain()
	var env []string
	for _, e := range os.Environ() {
		switch {
		case strings.HasPrefix(e, "GOCACHE="),
			strings.HasPrefix(e, "GOFLAGS="),
			strings.HasPrefix(e, "GOWORK="):
			continue
		}
		env = append(env, e)
	}
	return append(env, "GOCACHE="+sharedCacheDir, "GOFLAGS=-trimpath", "GOWORK=off")
}

// CacheDir is the shared GOCACHE this process is using.
func CacheDir() string {
	requireMain()
	return sharedCacheDir
}

// requireMain refuses to hand out the shared environment in a test binary that
// will never give it back.
func requireMain() {
	if !mainInstalled {
		panic("testgo: this test binary runs the go command but its TestMain does not call testgo.Main " +
			"(or testgo.Run), so it would never release the shared build cache. Add\n\n" +
			"\tfunc TestMain(m *testing.M) { testgo.Main(m) }\n")
	}
}

// MkdirProcessTemp creates a directory that lives as long as this test binary
// -- a CLI built once and shared by every test, say -- and is removed when Main
// returns. Its name carries ProcessDirPrefix, which the sweep knows, so a binary
// killed before Main returns leaves something the next run reclaims instead of
// a directory nobody can attribute. Such directories accumulated on the audit
// machine at 252 of them, 863 MB, under two prefixes nothing recognised.
func MkdirProcessTemp() (string, error) {
	requireMain()
	dir, err := os.MkdirTemp("", ProcessDirPrefix+"*")
	if err != nil {
		return "", err
	}
	processDirsMu.Lock()
	processDirs = append(processDirs, dir)
	processDirsMu.Unlock()
	return dir, nil
}

// MkdirWorkTemp creates a work directory for one generated module under one of
// the swept work-directory prefixes. The caller removes it when the command it
// was made for has finished; the sweep is only for a process that died first.
// prefix must be one of WorkDirPrefixes.
func MkdirWorkTemp(prefix string) (string, error) {
	if !isWorkPrefix(prefix) {
		return "", fmt.Errorf("testgo: %q is not a work-directory prefix the sweep knows (%s); "+
			"a directory under it would never be reclaimed", prefix, strings.Join(WorkDirPrefixes, ", "))
	}
	return os.MkdirTemp("", prefix+"*")
}

func isWorkPrefix(prefix string) bool {
	for _, p := range WorkDirPrefixes {
		if p == prefix {
			return true
		}
	}
	return false
}
