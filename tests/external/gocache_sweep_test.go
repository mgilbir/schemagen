package external

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// workDirFixtureSchema is the schema the work-directory fixtures are generated
// from.
//
// It is written for what it makes the generator emit, not for what it says: a
// root object with a Validate() method, string, numeric, array and object
// constraints, a pattern, and an asserted `format: email`. The format block is
// what puts the ECMA-262 engine and x/net/idna in the helper file, so the
// module carries the same external requirements as the ones the corpus
// produces and is compiled the same way rather than being a cheap stand-in.
const workDirFixtureSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "a": {"type": "string", "minLength": 2, "pattern": "^a.*", "format": "email"},
    "b": {"type": "integer", "minimum": 1, "maximum": 100},
    "c": {"type": "array", "items": {"type": "string"}, "minItems": 1},
    "d": {"type": "object", "additionalProperties": {"type": "number"}}
  },
  "required": ["a", "b"]
}`

// buildRealWorkDir writes a work directory the way tryValidation writes one:
// the real generator and emitter over a real schema, the real helper-file
// companion, the real go.mod and go.sum, and the real generated main().
//
// A hand-made directory is not a fixture for this, for the same reason a
// hand-made GOCACHE was not a fixture for the cache sweep -- and that mistake
// has already shipped here once, in a test whose "live" control was a bare
// directory while the sweep it guarded would have deleted every long-running
// cache on the box. What the sweep has to spare is a directory with a
// compilable module in it and a `go` process working inside it, so that is what
// this builds.
//
// fixture.json is made a named pipe rather than a file. The generated program
// reads it with os.ReadFile, which blocks in the open until a writer arrives,
// so the caller decides exactly when the program stops being mid-run -- the
// directory is genuinely in use, at an instant chosen rather than hoped for.
// The returned path is that pipe.
func buildRealWorkDir(t *testing.T, dir string) string {
	t.Helper()
	code, err := tryGenerateWithValidation(json.RawMessage(workDirFixtureSchema), generator.Config{
		PackageName: "testpkg", OmitEmpty: true, Draft: schema.Draft202012, FormatAssertion: true,
	})
	if err != nil {
		t.Fatalf("generating the fixture module: %v", err)
	}
	if code == "" {
		t.Fatalf("the fixture schema produced no Validate(); a work directory without one is not what the harness compiles")
	}
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		t.Fatalf("no root type in the generated fixture")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	main := strings.Replace(code, "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(main), 0o644); err != nil {
		t.Fatalf("write types.go: %v", err)
	}
	if err := writeSharedHelpersErr(dir, main); err != nil {
		t.Fatalf("write helpers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(generateValidateMain(rootType)), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := writeTestGoMod(dir, "validate_test"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	// The fixture is worthless if it is not the real shape, so it says so here
	// rather than passing quietly with three files in it.
	for _, name := range []string{"types.go", "schemagen_helpers.go", "main.go", "go.mod", "go.sum"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("the fixture work directory has no %s (%v); it is not a module the harness would have written", name, err)
		}
	}
	helpers, err := os.ReadFile(filepath.Join(dir, "schemagen_helpers.go"))
	if err != nil {
		t.Fatalf("read helpers: %v", err)
	}
	for _, dep := range []string{"goecma262", "golang.org/x/net/idna"} {
		if !strings.Contains(string(helpers), dep) {
			t.Fatalf("the fixture module does not import %s, so it is cheaper to build than the ones the corpus produces and is not the thing being spared", dep)
		}
	}

	fixture := filepath.Join(dir, "fixture.json")
	if !makeBlockingFile(fixture) {
		t.Fatalf("could not create the blocking fixture at %s", fixture)
	}
	return fixture
}

// TestSweepSparesAWorkDirectoryALiveRunIsUsing is the half of #158 that adding
// four prefixes to a list does not get right on its own.
//
// A work directory's mtimes are stamped once, when the harness finishes writing
// the generated module into it, and nothing afterwards advances them: the
// compile and the run that follow write into GOCACHE and into go run's own
// scratch space, never into the directory they are working in. Measured over a
// live one at 250ms intervals through a cold `go run` -- every sample reporting
// a root age and a cacheLastActive age equal to its own elapsed time, and every
// child still stamped at the moment it was written. So a work directory has
// exactly the property that made the first cache sweep wrong: judged on mtimes,
// a directory in use is indistinguishable from one a killed run left behind.
//
// The commands are bounded -- 30s for a compile or a round trip, 90s for a
// generated program, 10m for the bowtie oracle -- and the cutoff is an hour, so
// today the arithmetic holds. It holds by a margin nobody is consulting when
// they raise a timeout, and a `go run` killed at its context whose child still
// holds the output pipe is bounded by nothing at all. So the claim decides, and
// what is asserted here is the claim deciding: the fixture is aged past the
// cutoff so that age says "delete", and only the lock says otherwise.
//
// The control is the same directory a moment later with the run gone. If that
// one does not disappear, this test proves nothing, because it would mean the
// age check had spared it all along.
func TestSweepSparesAWorkDirectoryALiveRunIsUsing(t *testing.T) {
	if !testgo.LockingSupported {
		t.Skip("without flock nothing can claim a run, so the sweep judges work directories by age alone here")
	}
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "schemagen-val-liveworkdir")
	fixture := buildRealWorkDir(t, dir)

	// A real run, in the real directory, with the real arguments and the real
	// cache. It gets as far as reading its fixture and stops there.
	cmd := testgo.Command(context.Background(), dir, "run", ".")
	type runResult struct {
		out []byte
		err error
	}
	finished := make(chan runResult, 1)
	go func() {
		out, err := cmd.CombinedOutput()
		finished <- runResult{out, err}
	}()

	// Opening the pipe for writing returns only once the program has opened it
	// for reading, so this is the build having succeeded and the program being
	// alive inside dir -- known, not assumed.
	opened := make(chan *os.File, 1)
	go func() {
		w, err := os.OpenFile(fixture, os.O_WRONLY, 0)
		if err != nil {
			opened <- nil
			return
		}
		opened <- w
	}()
	var w *os.File
	select {
	case w = <-opened:
		if w == nil {
			t.Fatalf("could not open the fixture pipe for writing")
		}
	case r := <-finished:
		t.Fatalf("the run ended before the generated program read its fixture: %v\n%s", r.err, r.out)
	case <-time.After(5 * time.Minute):
		t.Fatalf("the generated program never reached its fixture")
	}

	// Aged past the cutoff, which is what a work directory whose command has
	// outlived the sweep's patience looks like -- and what every live one would
	// look like if a timeout were ever raised past an hour.
	testgo.AgeTree(t, dir, 3*time.Hour)

	releaseRun := testgo.HoldRunClaim(t, tmp)
	testgo.SweepIn(tmp, time.Now().Add(-testgo.StaleAge))
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the sweep deleted a work directory a live run was compiling in (%v); that group now fails to build and reports itself as a schema failure", err)
	}
	for _, name := range []string{"types.go", "schemagen_helpers.go", "main.go", "go.mod", "go.sum"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("the sweep emptied a work directory a live run was using: %s is gone (%v)", name, err)
		}
	}
	testgo.AssertNoDoomedLeftovers(t, tmp)

	// Let it finish. The verdict is the proof that the directory really was
	// usable throughout, rather than merely still present.
	if _, err := w.Write([]byte(`{"a":"a@example.com","b":5,"c":["x"],"d":{"e":1.5}}`)); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	w.Close()
	select {
	case r := <-finished:
		if r.err != nil {
			t.Fatalf("the run that survived the sweep then failed: %v\n%s", r.err, r.out)
		}
		if got := strings.TrimSpace(string(r.out)); got != "VALID" {
			t.Fatalf("the generated program said %q, not VALID; the directory it ran in was not intact", got)
		}
	case <-time.After(5 * time.Minute):
		t.Fatalf("the generated program never finished after its fixture arrived")
	}

	// The control. Same directory, same ages, nobody running: it must go, or
	// the lock was not what spared it above and this proves nothing.
	releaseRun()
	testgo.AgeTree(t, dir, 3*time.Hour)
	testgo.SweepIn(tmp, time.Now().Add(-testgo.StaleAge))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a work directory of exactly the shape spared above, with no run left to own it, survived the sweep (%v); either the lock was not what spared it or /tmp keeps the debris #158 is about", err)
	}
	testgo.AssertNoDoomedLeftovers(t, tmp)
}

// TestCacheHeadroomRefusesAVolumeTooSmallForTheRun watches the precondition
// fire, on a volume that really does have less free space than it is asked for.
//
// The requirement is raised past what the volume holds rather than the volume
// being filled: the predicate is the same one a full disk trips, and filling a
// 394G volume to watch a check fire is the harm this check exists to prevent.
// The message is asserted on because the message is the whole value of the
// check -- the failure it replaces was 144 "no space left on device" errors
// attributed to individual schemas.
func TestCacheHeadroomRefusesAVolumeTooSmallForTheRun(t *testing.T) {
	dir := t.TempDir()
	free, err := testgo.FreeBytes(dir)
	if err != nil {
		t.Skipf("free space is not measurable here: %v", err)
	}

	msg, measured := cacheShortfall(dir, free+(1<<30))
	if !measured {
		t.Fatalf("the volume holding %s reported free space and then could not be measured", dir)
	}
	if msg == "" {
		t.Fatalf("a volume with %.1f GiB free passed a check for %.1f GiB", float64(free)/(1<<30), float64(free+(1<<30))/(1<<30))
	}
	for _, want := range []string{dir, "no space left on device", "free", "requires"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, and a message that does not name the requirement is why this failure was read as schema failures:\n%s", want, msg)
		}
	}

	// The control. Without it a check that refuses everything would pass the
	// test above, and refusing every run is not an improvement on dying halfway
	// through one.
	if msg, measured := cacheShortfall(dir, 1); !measured || msg != "" {
		t.Errorf("a run needing one byte was refused on a volume with %.1f GiB free: %s", float64(free)/(1<<30), msg)
	}
}
