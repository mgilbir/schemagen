package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/emitter"
	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// jstsBaseDir is the path to the JSON Schema Test Suite tests directory,
// relative to the tests/ directory where these tests run.
const jstsBaseDir = "../testdata/external/JSON-Schema-Test-Suite/tests"

// jstsRemotesDir is the path to the remotes directory in the test suite.
const jstsRemotesDir = "../testdata/external/JSON-Schema-Test-Suite/remotes"

// remoteBaseURL is the base URL that the JSTS expects for remote schemas.
const remoteBaseURL = "http://localhost:1234"

// metaSchemaDir holds the JSON Schema meta-schemas, fetched by
// "make download-metaschemas". The suite refers to them but does not ship them.
const metaSchemaDir = "../testdata/external/metaschemas"

// Module metadata for the temp go.mod files the harnesses write.
//
// These are the modules *generated code* imports: the ECMA-262 engine for
// `pattern` and `format: regex`, and x/net/idna for the two hostname formats
// (which pulls x/text). They are pinned here as well as in the repository's own
// go.mod because the harness builds a throwaway module per test group, and a
// module with no go.sum entry cannot build offline.
const (
	goecma262Version = "v0.0.0-20260219184840-8bfa4bb752b0"
	goecma262H1      = "h1:g5uVjex1bABu72M6R0A//gQDoVXPSatqP50yZDX5wUQ="
	goecma262GoMod   = "h1:wQvOAFchLrhVSiF4JsSzH+yE6eLpc8gOBrvpuahNucI="

	// Pinned to the newest pair whose own go directive is 1.23, which is what
	// the throwaway modules below declare and what generated code should not
	// need more than. A later x/net raises the floor to Go 1.25 and would make
	// that the requirement for anyone whose schema names a hostname; the idna
	// behaviour is identical across the range, measured against this corpus.
	xnetVersion = "v0.38.0"
	xnetH1      = "h1:vRMAPTMaeGqVhG5QyLJHqNDwecKTomGeqbnfZyKlBI8="
	xnetGoMod   = "h1:ivrbrMbzFq5J41QOQh0siUuly180yBYtLp+CKbEaFx8="

	xtextVersion = "v0.24.0"
	xtextH1      = "h1:dd5Bzh4yt5KYA8f9CJHCP4FB4D51c2c6JvN37xJJkJ0="
	xtextGoMod   = "h1:L8rBsPeo2pSS+xqN0d5u2ikmjtmoJbDBT1b7nHvFCdU="
)

// writeTestGoMod writes a go.mod and go.sum in dir naming every module the
// generated code may import. moduleName is the module name for the temp project
// (e.g. "compile_test", "roundtrip_test").
//
// All three are required unconditionally rather than by inspecting the emitted
// source: an unused require is harmless, a missing one is a build failure in a
// throwaway module nobody will read the go.mod of.
func writeTestGoMod(dir, moduleName string) error {
	// x/text is indirect: nothing generated imports it, but x/net/idna does, and
	// a module that names only its direct requirements does not build offline.
	goMod := fmt.Sprintf("module %s\n\ngo 1.23.0\n\nrequire (\n\tgithub.com/mgilbir/goecma262 %s\n\tgolang.org/x/net %s\n)\n\nrequire golang.org/x/text %s // indirect\n",
		moduleName, goecma262Version, xnetVersion, xtextVersion)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(testGoSum()), 0o644); err != nil {
		return fmt.Errorf("write go.sum: %w", err)
	}
	return nil
}

// testGoSum is the go.sum body naming every module writeTestGoMod requires.
func testGoSum() string {
	return fmt.Sprintf(
		"github.com/mgilbir/goecma262 %s %s\ngithub.com/mgilbir/goecma262 %s/go.mod %s\n"+
			"golang.org/x/net %s %s\ngolang.org/x/net %s/go.mod %s\n"+
			"golang.org/x/text %s %s\ngolang.org/x/text %s/go.mod %s\n",
		goecma262Version, goecma262H1, goecma262Version, goecma262GoMod,
		xnetVersion, xnetH1, xnetVersion, xnetGoMod,
		xtextVersion, xtextH1, xtextVersion, xtextGoMod)
}

// allDrafts lists all draft directories in the test suite.
//
// Every directory under tests/ is here, and it has to stay that way: "v1"
// shipped in the corpus for months while this list named six drafts, so 438
// groups and 1988 cases were never run and nothing said so. A list named
// allDrafts that is not all of them is the quietest way to stop measuring
// something. If a directory ever has to be left out, leave it in this list and
// skip it where the skip can carry a reason -- an absent name carries none.
var allDrafts = []string{"draft3", "draft4", "draft6", "draft7", "draft2019-09", "draft2020-12", "v1"}

// draftFromDir maps a test-suite directory name to a schema.Draft constant.
func draftFromDir(dir string) schema.Draft {
	switch dir {
	case "draft3":
		return schema.Draft03
	case "draft4":
		return schema.Draft04
	case "draft6":
		return schema.Draft06
	case "draft7":
		return schema.Draft07
	case "draft2019-09":
		return schema.Draft201909
	case "draft2020-12":
		return schema.Draft202012
	case "v1":
		return schema.DraftV1
	default:
		return schema.DraftUnknown
	}
}

// jstsTestGroup represents a single test group from the JSTS.
type jstsTestGroup struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Tests       []jstsTestCase  `json:"tests"`
}

// jstsTestCase represents a single test case within a test group.
type jstsTestCase struct {
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
	Valid       bool            `json:"valid"`
}

// loadRemoteSchemas walks the remotes/ directory and builds a map of URL → *Schema.
// This allows the generator to resolve $ref values pointing to http://localhost:1234/...
func loadRemoteSchemas(t *testing.T) map[string]*schema.Schema {
	t.Helper()
	schemas := make(map[string]*schema.Schema)
	err := filepath.Walk(jstsRemotesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var s schema.Schema
		if err := json.Unmarshal(data, &s); err != nil {
			// Skip unparseable schemas (some may be non-schema JSON).
			return nil
		}
		s.Normalize()

		// Build the URL key: remoteBaseURL + relative path from remotes dir.
		rel, err := filepath.Rel(jstsRemotesDir, path)
		if err != nil {
			return err
		}
		// Use forward slashes for URL path.
		urlKey := remoteBaseURL + "/" + filepath.ToSlash(rel)
		schemas[urlKey] = &s
		return nil
	})
	if err != nil {
		t.Logf("warning: could not load remote schemas: %v", err)
	}
	return schemas
}

// loadMetaSchemas reads the downloaded meta-schemas, keyed by the $id each one
// declares rather than by its filename. The download URL and the URI a $ref
// resolves to are then the same string by construction, so there is no
// path-to-URI table to drift. Drafts before 2019-09 spell it "id", and several
// carry a trailing "#" that MappingResolver strips before lookup.
func loadMetaSchemas(t *testing.T) map[string]*schema.Schema {
	t.Helper()
	schemas := make(map[string]*schema.Schema)
	entries, err := os.ReadDir(metaSchemaDir)
	if err != nil {
		return schemas
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(metaSchemaDir, e.Name()))
		if err != nil {
			t.Fatalf("reading meta-schema %s: %v", e.Name(), err)
		}
		var s schema.Schema
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("parsing meta-schema %s: %v", e.Name(), err)
		}
		s.Normalize()
		id := s.ID
		if id == "" {
			id = s.LegacyID
		}
		if id == "" {
			t.Fatalf("meta-schema %s declares no $id", e.Name())
		}
		schemas[strings.TrimSuffix(id, "#")] = &s
	}
	return schemas
}

// remotesResolver returns a SchemaResolver for the test suite's remote schemas
// and the meta-schemas they reference.
//
// It fails rather than returning nil when nothing loads. requireTestSuite has
// already established that the suite is on disk, so an empty remotes directory
// means the checkout is damaged — and with strict refs a nil resolver turns
// every remote $ref into a code-generation failure, which TestExternalValidation
// counts as a skip. A hundred groups would quietly stop being validated and the
// run would still be green.
func remotesResolver(t *testing.T) schema.SchemaResolver {
	t.Helper()
	schemas := loadRemoteSchemas(t)
	if len(schemas) == 0 {
		t.Fatalf("no remote schemas loaded from %s; the suite checkout is incomplete", jstsRemotesDir)
	}
	metas := loadMetaSchemas(t)
	if len(metas) == 0 {
		t.Fatalf("no meta-schemas loaded from %s; run 'make download-metaschemas'", metaSchemaDir)
	}
	for uri, s := range metas {
		schemas[uri] = s
	}
	return schema.NewMappingResolver(schemas)
}

// requireTestSuite gates the external tests on the corpus being present.
//
// The unset environment variable is a skip: nobody asked for these tests, and
// `go test ./...` must stay usable without a 6 MB checkout. Everything after it
// is a failure, because SCHEMAGEN_RUN_EXTERNAL=1 *is* the request. Skipping a
// requested run reports a green pass having validated nothing, which is the same
// silence this file's coverage gate exists to end — and it is reachable, since
// the variable can be set by hand without going through `make test-external`
// (which depends on both download targets and so is always safe).
func requireTestSuite(t *testing.T) {
	t.Helper()
	if os.Getenv("SCHEMAGEN_RUN_EXTERNAL") != "1" {
		t.Skip("external JSON Schema Test Suite disabled; set SCHEMAGEN_RUN_EXTERNAL=1 or run make test-external")
	}
	if _, err := os.Stat(jstsBaseDir); err != nil {
		t.Fatalf("SCHEMAGEN_RUN_EXTERNAL=1 but the JSON Schema Test Suite is not at %s (%v). Run 'make download-test-suite', or 'make test-external' which does it for you.", jstsBaseDir, err)
	}
	// The suite refers to meta-schemas it does not ship. Without them a large
	// batch of refs cannot resolve, which would look like a wave of real
	// failures rather than a missing prerequisite.
	if _, err := os.Stat(metaSchemaDir); err != nil {
		t.Fatalf("SCHEMAGEN_RUN_EXTERNAL=1 but the meta-schemas are not at %s (%v). Run 'make download-metaschemas', or 'make test-external' which does it for you.", metaSchemaDir, err)
	}
}

// failureKey builds a lookup key for the known-failures maps.
func failureKey(parts ...string) string {
	return strings.Join(parts, "/")
}

// checkKnownFailure implements bidirectional known-failure checking.
//   - Flaky test → t.Skipf (always skip, regardless of outcome)
//   - Known failure that fails → t.Skipf (expected)
//   - Known failure that passes → t.Errorf (remove from list)
//   - Unknown failure → t.Errorf (regression)
//   - Unknown pass → OK
func checkKnownFailure(t *testing.T, key string, err error, knownFailures map[string]string) {
	t.Helper()
	// The one place knownFlakyTests is read is the one place its ledger is
	// written, so a consumer cannot consult the map without being counted. See
	// flakySweepState for why that matters.
	flakySweep.visit(t, key)
	// Skip flaky tests that non-deterministically pass/fail due to Go map iteration order.
	if _, flaky := knownFlakyTests[key]; flaky {
		if err != nil {
			t.Skipf("flaky test (failed): %v", err)
		} else {
			t.Skipf("flaky test (passed)")
		}
		return
	}
	reason, isKnown := knownFailures[key]
	if err != nil {
		if isKnown {
			t.Skipf("known failure: %v (reason: %s)", err, reason)
		} else {
			t.Errorf("unexpected failure: %v\n  key: %s", err, key)
		}
	} else {
		if isKnown {
			t.Errorf("test passed but is in known-failures list — remove key %q (reason was: %s)", key, reason)
		}
	}
}

// keyLedger records which known-failure keys a run offered and which it
// actually reached.
//
// checkKnownFailure is bidirectional per case -- an entry whose case fails is a
// skip, one whose case passes is an error -- but it can only judge a key some
// case still carries. About an entry naming a case the corpus no longer has it
// says nothing at all, and that is the state a suite bump produces routinely:
// upstream renamed draft 3's "ECMA 262 has no support for lookbehind" to
// "ECMA 262 supports lookbehind since ES2018" and flipped it to valid, and the
// entry covering it stopped being consulted without a word. It would have gone
// on reading as an outstanding defect while suppressing nothing -- the same
// class of lie reportStaleUnvalidated was written to end for the other list.
//
// The offered set is what makes the sweep safe under a -run filter. A filtered
// run reaches fewer keys than it offers, and a sweep over part of the corpus
// cannot tell a case that vanished upstream from one that was simply not
// selected. Keys are offered outside the subtests they are handed to, so a
// filter shows up as the difference between the two sets.
type keyLedger struct {
	offered map[string]bool
	visited map[string]bool
}

func newKeyLedger() *keyLedger {
	return &keyLedger{offered: make(map[string]bool, 8192), visited: make(map[string]bool, 8192)}
}

// offer records a key the run is about to hand to a subtest, and returns it so
// it can be used in the same expression.
func (l *keyLedger) offer(key string) string {
	l.offered[key] = true
	return key
}

// visit records that a subtest reached the key. Call it before
// checkKnownFailure, which can end the subtest through t.Skipf.
func (l *keyLedger) visit(key string) { l.visited[key] = true }

// complete reports whether every offered key was reached.
func (l *keyLedger) complete() bool { return len(l.offered) == len(l.visited) }

// staleKnownFailureKeys returns the entries of known that no case in this run
// carried, sorted. See keyLedger for why that is not simply "unused".
//
// It is separated from the reporting so it can be tested in milliseconds against
// planted ledgers. The alternative is a guard whose only exercise is a 50-minute
// corpus run that has to be sabotaged to see it fire once.
//
// The value type is a parameter only because knownFlakyTests is a set and the
// other four maps carry a reason string; nothing here reads the value.
func staleKnownFailureKeys[V any](known map[string]V, l *keyLedger) []string {
	var stale []string
	for key := range known {
		if !l.visited[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	return stale
}

// reportStaleKnownFailures errors on every entry of a known-failure map that no
// case in this run carried.
func reportStaleKnownFailures(t *testing.T, mapName string, known map[string]string, l *keyLedger) {
	t.Helper()
	if !l.complete() {
		t.Logf("partial run: reached %d of the %d %s keys the corpus offers (a -run filter on the subtests?). "+
			"The staleness sweep judges the whole corpus and is skipped; run without a subtest filter, "+
			"or via 'make test-external', to exercise it", len(l.visited), len(l.offered), mapName)
		return
	}
	for _, key := range staleKnownFailureKeys(known, l) {
		t.Errorf("stale %s entry: %q matches no case this run tested — the case was renamed or removed "+
			"upstream, or its group stopped being tested at all; delete the key, or find out which", mapName, key)
	}
}

// flakyConsumers names every top-level test that consults knownFlakyTests.
//
// It is a declaration, not an observation, and it has to be: the sweep below
// stands down until all of these have run, and a test that has been filtered out
// leaves no trace to notice. What can be observed is the other direction — a test
// that reads the map without appearing here — and flakySweepState.verdict reports
// that, because such a test may be the only carrier of a key and its absence from
// a run would make that key look vanished.
var flakyConsumers = []string{
	"TestExternalParsing",
	"TestExternalCodeGen",
	"TestExternalRoundTrip",
	"TestExternalValidation",
}

// flakySweepState is knownFlakyTests's staleness sweep.
//
// The other four known-failure maps each belong to one test, so each can be
// swept where it is read, against a ledger that test filled. knownFlakyTests
// belongs to all four -- checkKnownFailure consults it before any of them --
// and that is what stops the same shape from working here. A per-test sweep
// would see, at the end of TestExternalParsing, an entry naming a case only
// TestExternalRoundTrip carries, and would call it stale because the test doing
// the sweeping never reached it. That is false staleness, and false staleness is
// worse than none: it teaches whoever reads it to delete a live entry.
//
// So the ledger is one ledger, shared by the four tests, and the sweep runs from
// whichever of them finishes last rather than from a named one. "Last" is
// decided by the walked set rather than by declaration order: each test marks
// itself when it has walked every file the corpus holds, and the sweep fires on
// the call that completes the set. Nothing depends on the order the tests are
// declared in or on -shuffle leaving it alone.
//
// TestMain is the other candidate for "after the last", and is where this
// process's cache cleanup lives. It was not used: it holds no *testing.T, so a
// stale entry could only be reported by printing to stderr and forcing the exit
// code by hand, which detaches the message from the test that would explain it
// and reports nothing at all when the sweep should merely stand down. The
// last-of-four rule reaches the same point in the run with an ordinary t.Errorf.
//
// The keys stay in the format the four maps use -- draft/file/group[/case], with
// no test name in them. Namespacing per test was the alternative and would make
// the sweep per-test again, but an entry would then have to name the test that
// suppresses it, which is information the flakiness does not have: the
// non-determinism these entries covered came from map iteration order inside the
// generator, which is not a property of the test observing it. It would also
// leave knownFlakyTests's keys in a format subtly different from the four maps it
// shadows, so a key copied across from knownRoundTripFailures -- the obvious way
// to write one -- would match nothing.
type flakySweepState struct {
	mu       sync.Mutex
	ledger   *keyLedger
	walked   map[string]bool // consumer -> walked every file the corpus holds
	observed map[string]bool // consumer -> read knownFlakyTests at least once
	reported map[string]bool // stray consumer -> already named once
}

var flakySweep = newFlakySweepState()

func newFlakySweepState() *flakySweepState {
	return &flakySweepState{
		ledger:   newKeyLedger(),
		walked:   make(map[string]bool, len(flakyConsumers)),
		observed: make(map[string]bool, len(flakyConsumers)),
		reported: make(map[string]bool, len(flakyConsumers)),
	}
}

// offer records a key the run is about to hand to a subtest. As with keyLedger,
// call it outside the subtest, so that a -run filter shows up as offered and not
// visited rather than as a case the corpus has lost.
func (s *flakySweepState) offer(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledger.offer(key)
}

// visit records that a subtest of the named test reached the key.
func (s *flakySweepState) visit(t *testing.T, key string) {
	s.record(topLevelTest(t.Name()), key)
}

// record is visit with the consuming test named directly, so the sweep can be
// driven without a real subtest tree behind it.
func (s *flakySweepState) record(consumer, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ledger.visit(key)
	s.observed[consumer] = true
}

// markReported claims the right to name a stray consumer, and reports whether
// this call is the one that got it.
func (s *flakySweepState) markReported(consumer string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reported[consumer] {
		return false
	}
	s.reported[consumer] = true
	return true
}

// markWalked records that the named test walked every file the corpus holds, and
// so offered every key it has to offer.
func (s *flakySweepState) markWalked(consumer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.walked[consumer] = true
}

// topLevelTest returns the name of the top-level test a (sub)test name belongs
// to: "TestExternalRoundTrip/draft3/type/an object" -> "TestExternalRoundTrip".
func topLevelTest(name string) string {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return name
}

// flakySweepVerdict is what the sweep concluded, separated from the reporting so
// that every arm of it can be exercised in milliseconds against a planted state
// rather than only by sabotaging a 50-minute corpus run.
//
// At most one of stray, pending and incomplete is a reason to stand down, and
// they are checked in that order; stale is only ever filled when none of them is.
type flakySweepVerdict struct {
	stray      []string // tests that read the map but are not in flakyConsumers
	pending    []string // consumers that have not walked the whole corpus yet
	incomplete bool     // keys were offered and never reached (a -run filter below file level)
	stale      []string // entries no case in the run carried
	offered    int
	visited    int
}

// verdict judges knownFlakyTests against everything the four tests have recorded
// so far.
func (s *flakySweepState) verdict(known map[string]bool) flakySweepVerdict {
	s.mu.Lock()
	defer s.mu.Unlock()

	v := flakySweepVerdict{offered: len(s.ledger.offered), visited: len(s.ledger.visited)}
	for consumer := range s.observed {
		if !slices.Contains(flakyConsumers, consumer) {
			v.stray = append(v.stray, consumer)
		}
	}
	sort.Strings(v.stray)
	for _, consumer := range flakyConsumers {
		if !s.walked[consumer] {
			v.pending = append(v.pending, consumer)
		}
	}
	if len(v.stray) > 0 || len(v.pending) > 0 {
		return v
	}
	if !s.ledger.complete() {
		v.incomplete = true
		return v
	}
	v.stale = staleKnownFailureKeys(known, s.ledger)
	return v
}

// finish is called by each of the four tests once its walk is over. It records
// whether that walk covered the whole corpus, and sweeps if this call is the one
// that completed the set.
func (s *flakySweepState) finish(t *testing.T, walkedFiles, corpusFiles int) {
	t.Helper()
	consumer := topLevelTest(t.Name())

	// A -run filter selecting some drafts or some files stops their groups from
	// being walked at all, so their keys are never offered and the ledger cannot
	// see the gap. This is the same pairing TestExternalValidation already makes
	// between countCodeGenSuitableGroups and its own ledger, for the same reason:
	// neither check sees the other's case.
	if walkedFiles != corpusFiles {
		t.Logf("partial run: %s walked %d of the corpus's %d files (a -run filter on the subtests?). "+
			"The knownFlakyTests staleness sweep judges the whole corpus across every test that consults "+
			"the map, and is skipped; run without a subtest filter, or via 'make test-external', to exercise it",
			consumer, walkedFiles, corpusFiles)
		return
	}
	s.markWalked(consumer)

	v := s.verdict(knownFlakyTests)
	switch {
	case len(v.stray) > 0:
		// Once per stray consumer, not once per consumer that finishes after it:
		// every remaining test would otherwise repeat the same paragraph.
		for _, name := range v.stray {
			if s.markReported(name) {
				t.Errorf("%s consults knownFlakyTests but is not listed in flakyConsumers, so the staleness "+
					"sweep does not wait for it and does not know which keys it carries — a run that filtered "+
					"it out would report every entry only it carries as stale. Add it to flakyConsumers and "+
					"give it a flakySweep.finish call", name)
			}
		}
	case len(v.pending) > 0:
		t.Logf("knownFlakyTests staleness sweep deferred: %s %s not yet walked the whole corpus; "+
			"the sweep runs from the last of the %d tests that consult the map",
			strings.Join(v.pending, ", "), pluralHave(len(v.pending)), len(flakyConsumers))
	case v.incomplete:
		t.Logf("partial run: reached %d of the %d knownFlakyTests keys the corpus offers (a -run filter on "+
			"the subtests?). The staleness sweep judges the whole corpus and is skipped; run without a "+
			"subtest filter, or via 'make test-external', to exercise it", v.visited, v.offered)
	case len(v.stale) > 0:
		for _, key := range v.stale {
			t.Errorf("stale knownFlakyTests entry: %q matches no case any of the %d tests that consult the "+
				"map tested — the case was renamed or removed upstream, or its group stopped being reached "+
				"at all; delete the key, or find out which", key, len(flakyConsumers))
		}
	default:
		// Said out loud on a clean run, because a sweep that reports nothing and
		// a sweep that ran over nothing read identically otherwise — which is
		// the failure #119 was about, one level down.
		t.Logf("knownFlakyTests staleness sweep: %d keys carried across %d tests, %d entr(ies) in the map, none stale",
			v.visited, len(flakyConsumers), len(knownFlakyTests))
	}
}

// pluralHave picks the verb for a list of names, so the deferral message reads
// as a sentence in both the "one test left" and "three tests left" cases.
func pluralHave(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

// countCorpusFiles counts the (draft, file) pairs the four external tests walk
// when nothing filters them.
//
// It is the file-level counterpart to countCodeGenSuitableGroups, and it is a
// file count rather than a group count because all four tests walk every file
// while disagreeing about which groups inside one they visit. Missing draft
// directories are skipped here exactly as the tests skip them, so a corpus
// checkout without one is not mistaken for a filtered run.
func countCorpusFiles(t *testing.T) int {
	t.Helper()
	var n int
	for _, draft := range allDrafts {
		draftDir := filepath.Join(jstsBaseDir, draft)
		if _, err := os.Stat(draftDir); os.IsNotExist(err) {
			continue
		}
		n += len(listJSONFiles(t, draftDir))
	}
	return n
}

// checkUnvalidatedRejection is checkKnownFailure's counterpart for a group that
// produced no Validate() method while the suite marks one of its documents
// invalid. There is no per-case outcome to judge here — the defect is the
// absence itself — so the two arms are "allow-listed" and "not".
//
//   - allow-listed → t.Skipf (a recorded, outstanding gap)
//   - not allow-listed → t.Errorf (a provable defect, or a regression)
//
// The other direction, an allow-list entry that no longer describes anything,
// is checked once at the end of the run by reportStaleUnvalidated: it needs the
// whole corpus walked before it can tell a fixed group from a vanished one.
func checkUnvalidatedRejection(t *testing.T, key string) {
	t.Helper()
	if reason, ok := knownUnvalidatedRejections[key]; ok {
		t.Skipf("known gap: no Validate() produced for a group the suite says must reject a document (reason: %s)", reason)
		return
	}
	t.Errorf("no Validate() method was produced, but the suite marks at least one document in this group invalid — "+
		"the generated code accepts a document that must be rejected\n"+
		"  key: %s\n"+
		"  if this is a genuine outstanding gap rather than a regression, add the key to knownUnvalidatedRejections", key)
}

// groupFate records what became of every code-gen-suitable group in a run, so
// that a stale knownUnvalidatedRejections entry can be reported with the reason
// it went stale rather than a bare "unused".
type groupFate int

const (
	fateUnvalidated  groupFate = iota // no Validate(), and a document must be rejected — the allow-listed state
	fateValidated                     // a Validate() was produced and its cases ran
	fateNoRejection                   // no Validate(), but no document has to be rejected either
	fateCodeGenError                  // code generation failed; TestExternalCodeGen owns this one
)

// reportStaleUnvalidated errors on every knownUnvalidatedRejections entry that
// did not describe a group in this run. A stale entry is the same class of lie
// as the silent skip the list replaced: it reads as "this is still broken" while
// suppressing nothing, and it hides the next regression that lands on that key.
func reportStaleUnvalidated(t *testing.T, fates map[string]groupFate) {
	t.Helper()
	keys := make([]string, 0, len(knownUnvalidatedRejections))
	for key := range knownUnvalidatedRejections {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fate, seen := fates[key]
		if seen && fate == fateUnvalidated {
			continue
		}
		switch {
		case !seen:
			t.Errorf("stale allow-list entry: %q matches no group in the test suite — "+
				"it was renamed or removed upstream; delete the key from knownUnvalidatedRejections", key)
		case fate == fateValidated:
			t.Errorf("stale allow-list entry: %q now produces a Validate() method and its cases are being tested — "+
				"delete the key from knownUnvalidatedRejections", key)
		case fate == fateNoRejection:
			t.Errorf("stale allow-list entry: %q no longer carries a document the suite marks invalid, "+
				"so nothing about it is provable — delete the key from knownUnvalidatedRejections", key)
		case fate == fateCodeGenError:
			t.Errorf("stale allow-list entry: %q now fails code generation outright rather than producing "+
				"a Validate()-less type; that is a code-generation regression (see TestExternalCodeGen), "+
				"and the key no longer belongs in knownUnvalidatedRejections", key)
		}
	}
}

// countCodeGenSuitableGroups walks the corpus and counts the groups
// TestExternalValidation would visit if nothing filtered it.
//
// The aggregate gates at the end of that test — the coverage floor and the
// allow-list staleness sweep — are statements about the whole corpus, and a
// `go test -run TestExternalValidation/draft3` run would fail both of them for
// no reason at all: five drafts never walked reads identically to five drafts
// that stopped producing Validate(). Comparing the visited count against this
// one distinguishes the two, so a filtered run says so and stays quiet rather
// than crying wolf.
func countCodeGenSuitableGroups(t *testing.T) int {
	t.Helper()
	var n int
	for _, draft := range allDrafts {
		draftDir := filepath.Join(jstsBaseDir, draft)
		if _, err := os.Stat(draftDir); os.IsNotExist(err) {
			continue
		}
		for _, file := range listJSONFiles(t, draftDir) {
			for _, group := range loadTestGroups(t, filepath.Join(draftDir, file)) {
				if isCodeGenSuitable(group.Schema) {
					n++
				}
			}
		}
	}
	return n
}

// groupHasRejectingCase reports whether the suite marks any document in the
// group invalid. Without one there is nothing to prove: a group of accept-only
// cases against a type carrying no Validate() is uninformative, not wrong.
func groupHasRejectingCase(group jstsTestGroup) bool {
	for _, tc := range group.Tests {
		if !tc.Valid {
			return true
		}
	}
	return false
}

// listJSONFiles returns the relative paths of all .json files in a directory, recursively.
// Paths are relative to dir (e.g., "minLength.json", "optional/bignum.json").
//
// tests/v1/proposals/ is the one thing left out, and it is left out here rather
// than by omitting a draft from allDrafts so that the reason travels with the
// decision. The suite's own README calls that directory tests for keywords that
// are still proposals to the specification, "volatile while the proposal is in
// development", and says implementations are expected to pass them only if they
// claim to support the proposal. Its whole content today is propertyDependencies
// (8 groups, 38 cases), which is not a keyword of any dialect this generator
// reads; every draft says to ignore a keyword it does not know, so the schemas
// would generate correct-but-permissive types and the 20-odd must-reject
// documents would be reported as defects against a keyword nobody has agreed on.
// Supporting a proposal is a decision to take deliberately, not one to be forced
// into by a directory appearing upstream.
func listJSONFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == "proposals" {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".json") {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("walking directory %s: %v", dir, err)
	}
	return files
}

// filenameWithoutExt strips the .json extension from a filename or relative path.
func filenameWithoutExt(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// isBignumFile returns true if the file path is for tests that require BigIntSupport.
// This includes optional/bignum.json (arbitrary-precision integers) and
// optional/float-overflow.json (1e308 overflows int64 but fits in big.Int).
func isBignumFile(file string) bool {
	return strings.Contains(file, "optional/bignum") || strings.Contains(file, "optional/float-overflow")
}

// formatPostureFor reports which of the two "format" postures a suite file is
// asking about, as the pair of generator switches that select it. Both false
// means the file is asking about the default, which is whatever the dialect
// named in its schemas says.
//
// The suite states both postures and keeps them apart by directory, and which
// one is the default flipped with v1. For 2019-09 and 2020-12 the non-optional
// format.json says what a format means by default: the format-annotation
// vocabulary makes it an annotation, so "2962" satisfies {"format":"email"} and
// every one of those cases is marked valid. optional/format/*.json says what
// the assertion means for an implementation that opts in, and marks the same
// document invalid. v1 drops vocabularies, moves format/*.json up to the
// required level where "2962" is invalid, and files the annotation reading under
// optional/format-annotation.json instead. Neither posture is wrong in either
// place; they describe different configurations, which is what "optional" means
// here.
//
// Running either set under the wrong configuration would ask this generator to
// fail. In the assertion direction it would have to accept a document the file
// says must be refused, which understates the checks; in the annotation
// direction it would have to reject a document its own dialect permits, and
// rejecting what the schema permits is the one thing this repository does not
// trade away. Selecting the configuration per file asks the question the file is
// actually about. This is the same per-file switch isBignumFile already makes,
// for the same reason.
//
// The order of the two arms is load-bearing, and it is the reason this is a
// switch rather than two independent tests: "optional/format-annotation.json"
// begins with "optional/format" and means the opposite of it, so the narrower
// name has to be tried first. draft2020-12/optional/format-assertion.json does
// belong in the assertion arm: its schemas name a custom metaschema declaring
// the format-assertion vocabulary, which this generator does not read, so the
// flag stands in for the vocabulary the file is about.
func formatPostureFor(file string) (assertion, annotation bool) {
	p := filepath.ToSlash(file)
	switch {
	case strings.Contains(p, "optional/format-annotation"):
		return false, true
	case strings.Contains(p, "optional/format"):
		return true, false
	default:
		return false, false
	}
}

// loadTestGroups reads and parses a JSTS test file.
func loadTestGroups(t *testing.T, path string) []jstsTestGroup {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading test file %s: %v", path, err)
	}
	var groups []jstsTestGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		t.Fatalf("parsing test file %s: %v", path, err)
	}
	return groups
}

// isCodeGenSuitable checks if a schema can produce a Go type definition.
// All JSON object schemas are suitable. Boolean schemas (true/false) are not.
func isCodeGenSuitable(schemaJSON json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(schemaJSON))
	if trimmed == "true" {
		// Boolean true schema accepts everything — no validation possible.
		return false
	}
	if trimmed == "false" {
		// Boolean false schema rejects everything — generates a forbidden type.
		return true
	}
	// Any JSON object schema can produce a type definition (struct, alias, enum, etc.).
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// isJSONObject checks if a JSON value is an object (starts with '{').
func isJSONObject(data json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(data))
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// extractRootTypeNameFromCode finds the root type in generated code.
// Prefers struct types with JSON tags, then any struct, then type aliases named "Root".
// Returns empty string if none found (does not call t.Fatal).
func extractRootTypeNameFromCode(code string) string {
	lines := strings.Split(code, "\n")

	// The generator always names the root type "Root". Check for it first
	// across all type declarations (struct, alias, defined type).
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "type Root ") {
			return "Root"
		}
	}

	// Fallback: find the last struct with JSON-tagged fields.
	var lastType string
	var currentType string
	var hasJSONTag bool

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, " struct {") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				currentType = parts[1]
				hasJSONTag = false
			}
		}
		if currentType != "" && strings.Contains(trimmed, "`json:\"") {
			hasJSONTag = true
		}
		if trimmed == "}" && currentType != "" {
			if hasJSONTag {
				lastType = currentType
			}
			currentType = ""
		}
	}

	if lastType == "" {
		// Fallback: just find the last struct
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, " struct {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					lastType = parts[1]
				}
			}
		}
	}

	if lastType == "" {
		// Final fallback: look for any type declaration (aliases, defined types).
		var lastAlias string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "type ") && !strings.Contains(trimmed, " struct {") && !strings.Contains(trimmed, " interface {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 3 {
					lastAlias = parts[1]
				}
			}
		}
		lastType = lastAlias
	}

	return lastType
}

// ephemeralCacheDir holds a per-process temporary GOCACHE directory that is
// cleaned up at process exit. Using a shared ephemeral cache (instead of the
// user's persistent ~/.cache/go-build) prevents the ~14,000 unique compilations
// from bloating the build cache by hundreds of gigabytes.
var ephemeralCacheDir string

// staleCacheAge is how old an abandoned cache must be before this process will
// delete it. It has to exceed the longest a live run can go without touching
// its own directory -- the suite compiles continuously, so minutes would do --
// and stay far below the gap between one developer's runs. An hour is well
// clear on both sides.
const staleCacheAge = time.Hour

// cacheLastActive reports when a cache directory was last written to.
//
// It is not the directory's own mtime, which is the obvious reading and the
// wrong one. Go lays out GOCACHE as 256 subdirectories plus trim.txt on first
// use and writes every entry *inside* those, so the root's mtime is stamped once
// when the cache is created and never advances again however hard the cache is
// worked -- measured at six consecutive builds over fifteen seconds, all five
// after the first leaving the root untouched. Judging by it makes a run that
// outlives staleCacheAge indistinguishable from an abandoned one, so a
// concurrently starting run deletes a live cache out from under it. That is the
// "sweeping too much" direction this function's comment calls the worse and
// quieter of the two: nothing fails visibly, the robbed run just recompiles from
// nothing and looks inexplicably slow.
//
// The immediate children do track activity -- each build touches the bucket it
// writes into, and trim.txt is rewritten periodically -- so the newest of the
// root and its children is the answer. Reading one level is enough and is
// bounded: 257 entries for a Go cache, and no recursion into the thousands of
// files below.
func cacheLastActive(dir string, root os.FileInfo) time.Time {
	newest := root.ModTime()
	children, err := os.ReadDir(dir)
	if err != nil {
		return newest
	}
	for _, c := range children {
		info, err := c.Info()
		if err != nil {
			continue
		}
		if t := info.ModTime(); t.After(newest) {
			newest = t
		}
	}
	return newest
}

// sweepStaleCaches deletes ephemeral cache directories left behind by earlier
// runs.
//
// TestMain removes this process's directory on the way out, which handles the
// common path. It cannot handle any other: a -timeout kill, a SIGTERM from a
// harness, an interrupt, or a crash on a full disk all skip it, and each one
// strands roughly 2G. That compounds -- a full disk kills runs, and each kill
// strands another cache -- and it has already exhausted a 394G volume, at which
// point the suite fails with "no space left on device" reported against
// individual test keys, so a dead run reads like a set of real validation
// failures.
//
// So the sweep happens on the way in, where it works no matter how the previous
// process died. Errors are ignored throughout: reclaiming space is best-effort,
// and a directory another user owns is not this process's to worry about.
func sweepStaleCaches() { sweepStaleCachesIn(os.TempDir(), time.Now().Add(-staleCacheAge)) }

// sweepStaleCachesIn is sweepStaleCaches with the directory and cutoff supplied,
// so a test can drive it without touching the real temp directory or waiting an
// hour for something to age.
func sweepStaleCachesIn(tmp string, cutoff time.Time) {
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "schemagen-gocache-") && !strings.HasPrefix(name, "schemagen-cogen-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(tmp, name)
		if cacheLastActive(path, info).After(cutoff) {
			continue
		}
		os.RemoveAll(path)
	}
}

func init() {
	sweepStaleCaches()
	dir, err := os.MkdirTemp("", "schemagen-gocache-*")
	if err != nil {
		panic(fmt.Sprintf("creating ephemeral GOCACHE: %v", err))
	}
	ephemeralCacheDir = dir
}

// TestMain cleans up the ephemeral cache directory after all tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	if ephemeralCacheDir != "" {
		os.RemoveAll(ephemeralCacheDir)
	}
	os.Exit(code)
}

// ephemeralCacheEnv returns a copy of the current environment with GOCACHE
// pointed at an ephemeral temporary directory. This prevents external go
// build/run invocations from bloating the user's persistent build cache —
// each test compiles unique generated code that will never be reused.
func ephemeralCacheEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GOCACHE=") {
			env = append(env, e)
		}
	}
	return append(env, "GOCACHE="+ephemeralCacheDir)
}

// tryParse attempts to parse a JSTS schema into our schema.Schema type.
func tryParse(schemaJSON json.RawMessage) error {
	// Handle boolean schemas
	trimmed := strings.TrimSpace(string(schemaJSON))
	if trimmed == "true" || trimmed == "false" {
		// Boolean schemas are valid JSON Schema but our parser expects objects
		return nil
	}

	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	s.Normalize()
	return nil
}

// tryGenerateAndCompile attempts the full pipeline: parse → generate IR → emit → compile.
func tryGenerateAndCompile(schemaJSON json.RawMessage, resolver schema.SchemaResolver, bigInt bool) error {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	// Refs are strict here, matching the CLI default: a ref no resolver can
	// serve fails generation rather than degrading to any. Leniency let a
	// schema the harness had silently emptied out still count as a pass, so
	// the suite measured a validator built from a schema nobody wrote.
	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver, BigIntSupport: bigInt}
	gen := generator.New(cfg)
	ir, err := gen.Generate(&s)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	em, err := emitter.New()
	if err != nil {
		return fmt.Errorf("emitter: %w", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return fmt.Errorf("emit: %w", err)
	}

	// Compile in temp dir
	tmpDir, err := os.MkdirTemp("", "schemagen-external-*")
	if err != nil {
		return fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	content := strings.Replace(string(src), "package testpkg", "package compile_test", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write types: %w", err)
	}
	if err := writeSharedHelpersErr(tmpDir, content); err != nil {
		return fmt.Errorf("write helpers: %w", err)
	}
	if err := writeTestGoMod(tmpDir, "compile_test"); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", ".")
	cmd.Dir = tmpDir
	cmd.Env = ephemeralCacheEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile: %s\n%s", err, string(output))
	}
	return nil
}

// tryRoundTrip attempts the full round-trip: parse → generate → compile → unmarshal → marshal → compare.
func tryRoundTrip(schemaJSON, dataJSON json.RawMessage, resolver schema.SchemaResolver, bigInt bool) error {
	var s schema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	// Refs are strict here, matching the CLI default: a ref no resolver can
	// serve fails generation rather than degrading to any. Leniency let a
	// schema the harness had silently emptied out still count as a pass, so
	// the suite measured a validator built from a schema nobody wrote.
	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver, BigIntSupport: bigInt}
	gen := generator.New(cfg)
	ir, err := gen.Generate(&s)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	em, err := emitter.New()
	if err != nil {
		return fmt.Errorf("emitter: %w", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return fmt.Errorf("emit: %w", err)
	}

	rootType := extractRootTypeNameFromCode(string(src))
	if rootType == "" {
		return fmt.Errorf("could not find root struct type in generated code")
	}

	tmpDir, err := os.MkdirTemp("", "schemagen-rt-*")
	if err != nil {
		return fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mainContent := strings.Replace(string(src), "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(mainContent), 0o644); err != nil {
		return fmt.Errorf("write types: %w", err)
	}
	if err := writeSharedHelpersErr(tmpDir, mainContent); err != nil {
		return fmt.Errorf("write helpers: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "fixture.json"), dataJSON, 0o644); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(generateRoundTripMain(rootType)), 0o644); err != nil {
		return fmt.Errorf("write main: %w", err)
	}
	if err := writeTestGoMod(tmpDir, "roundtrip_test"); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	cmd.Env = ephemeralCacheEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("round-trip: %s\n%s", err, string(output))
	}
	if programOutput(output) != "PASS" {
		return fmt.Errorf("round-trip mismatch:\n%s", string(output))
	}
	return nil
}

// trivialRootValidate matches a root Validate() whose entire body is
// "return nil" — a method that exists, satisfies hasValidateMethod, and checks
// nothing. The receiver name and pointer-ness are left open; everything else is
// pinned, because the point of the pattern is that the body has no statements
// but the return.
var trivialRootValidate = regexp.MustCompile(`(?m)^func \([A-Za-z_]\w* \*?(\w+)\) Validate\(\) error \{\n\treturn nil\n\}`)

// rootValidateHasNoChecks reports whether the root type's Validate() body is
// exactly "return nil".
//
// Such a group counts as tested, and its rejecting cases pass, but they pass on
// the decoder: the document never fit the Go type. Often that is legitimate and
// complete — {"type":"integer"} becomes `type Root int64`, which genuinely
// cannot hold "foo" — but it is not what "Validate() correctness" sounds like,
// and a change that widened the Go type would turn those rejections into
// unnoticed acceptances. The count is reported so the distinction is visible.
func rootValidateHasNoChecks(code string) bool {
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return false
	}
	for _, m := range trivialRootValidate.FindAllStringSubmatch(code, -1) {
		if m[1] == rootType {
			return true
		}
	}
	return false
}

// hasValidateMethod checks if generated Go code contains a Validate() method.
func hasValidateMethod(code string) bool {
	// Check that the root type (identified by extractRootTypeNameFromCode) has a Validate() method.
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return false
	}
	// Look for "func (<recv> <RootType>) Validate() error {" pattern.
	// The receiver is typically a single lowercase letter.
	return strings.Contains(code, rootType+") Validate() error {")
}

// validationVerdict is how a generated program judged a document: accepted, or
// refused — and if refused, by which of the two mechanisms.
//
// Both refusals are real refusals as far as a caller who decodes JSON is
// concerned, so the suite treats them alike when deciding pass or fail. They are
// counted apart because they prove different things, and one of them proves less
// than the test's name suggests: a document refused by the decoder was refused
// by the shape of the Go type, not by any rule the generator emitted.
type validationVerdict int

const (
	verdictMissing          validationVerdict = iota // the program printed nothing recognisable
	verdictValid                                     // decoded, and Validate() returned nil
	verdictDecodeRejected                            // json.Unmarshal refused it; Validate() never ran
	verdictValidateRejected                          // decoded, and Validate() returned an error
)

// generateValidateMain creates a Go main() that:
// 1. Reads fixture.json
// 2. Unmarshals into the generated type
// 3. Calls Validate()
// 4. Prints "VALID", "UNMARSHAL: <message>", or "INVALID: <message>"
//
// The two refusals print distinct tokens rather than a shared "INVALID:" with a
// prefix to disambiguate, matching the vocabulary the cogen harness already uses
// — a Validate() error is free to begin with any words at all, so a prefix test
// on a shared token would be a guess.
func generateValidateMain(rootType string) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	data, err := os.ReadFile("fixture.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading fixture: %%v\n", err)
		os.Exit(1)
	}

	var obj %s
	if err := json.Unmarshal(data, &obj); err != nil {
		// Any unmarshal error is a type mismatch: the JSON data doesn't
		// fit the generated Go type. This is equivalent to a JSON Schema
		// validation failure (wrong type, missing required field, etc.),
		// but it is the Go type refusing the document rather than a rule
		// the generator emitted, so it reports itself separately.
		fmt.Printf("UNMARSHAL: %%v\n", err)
		return
	}

	if err := obj.Validate(); err != nil {
		fmt.Printf("INVALID: %%v\n", err)
	} else {
		fmt.Println("VALID")
	}
}
`, rootType)
}

// tryGenerateWithValidation attempts: parse → generate → emit, returns generated code
// only if it contains a Validate() method. Returns ("", nil) if no Validate() method
// is found (not an error, just a skip condition).
func tryGenerateWithValidation(schemaJSON json.RawMessage, resolver schema.SchemaResolver, draft schema.Draft, bigInt, formatAssertion, formatAnnotation bool) (string, error) {
	var s schema.Schema
	// Handle boolean false schema: "false" is not a JSON object, so we construct
	// the Schema struct manually with BooleanSchema set to false.
	trimmed := strings.TrimSpace(string(schemaJSON))
	if trimmed == "false" {
		boolVal := false
		s.BooleanSchema = &boolVal
	} else if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	s.Normalize()

	// Refs are strict here, matching the CLI default: a ref no resolver can
	// serve fails generation rather than degrading to any. Leniency let a
	// schema the harness had silently emptied out still count as a pass, so
	// the suite measured a validator built from a schema nobody wrote.
	cfg := generator.Config{PackageName: "testpkg", OmitEmpty: true, Resolver: resolver, Draft: draft, BigIntSupport: bigInt, FormatAssertion: formatAssertion, FormatAnnotation: formatAnnotation}
	gen := generator.New(cfg)
	ir, err := gen.Generate(&s)
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}

	em, err := emitter.New()
	if err != nil {
		return "", fmt.Errorf("emitter: %w", err)
	}
	src, err := em.Emit(ir)
	if err != nil {
		return "", fmt.Errorf("emit: %w", err)
	}

	code := string(src)
	if !hasValidateMethod(code) {
		return "", nil // no validation to test
	}
	return code, nil
}

// tryValidation runs a validation test using pre-generated code.
// The code must already have a Validate() method.
//
// It returns the verdict alongside the pass/fail error so the caller can count
// decode refusals apart from Validate() refusals. The verdict is meaningful even
// when the error is non-nil (that is the "wrong verdict" case); it is
// verdictMissing when the program could not be built or run at all.
func tryValidation(code string, dataJSON json.RawMessage, expectValid bool) (validationVerdict, error) {
	rootType := extractRootTypeNameFromCode(code)
	if rootType == "" {
		return verdictMissing, fmt.Errorf("could not find root type in generated code")
	}

	tmpDir, err := os.MkdirTemp("", "schemagen-val-*")
	if err != nil {
		return verdictMissing, fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mainContent := strings.Replace(code, "package testpkg", "package main", 1)
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(mainContent), 0o644); err != nil {
		return verdictMissing, fmt.Errorf("write types: %w", err)
	}
	if err := writeSharedHelpersErr(tmpDir, mainContent); err != nil {
		return verdictMissing, fmt.Errorf("write helpers: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "fixture.json"), dataJSON, 0o644); err != nil {
		return verdictMissing, fmt.Errorf("write fixture: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(generateValidateMain(rootType)), 0o644); err != nil {
		return verdictMissing, fmt.Errorf("write main: %w", err)
	}
	if err := writeTestGoMod(tmpDir, "validate_test"); err != nil {
		return verdictMissing, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir
	cmd.Env = ephemeralCacheEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return verdictMissing, fmt.Errorf("run: %s\n%s", err, string(output))
	}

	result := programOutput(output)
	var verdict validationVerdict
	switch {
	case result == "VALID":
		verdict = verdictValid
	case strings.HasPrefix(result, "UNMARSHAL:"):
		verdict = verdictDecodeRejected
	case strings.HasPrefix(result, "INVALID:"):
		verdict = verdictValidateRejected
	default:
		return verdictMissing, fmt.Errorf("generated program reported no verdict: %s", result)
	}

	if expectValid {
		if verdict != verdictValid {
			return verdict, fmt.Errorf("expected VALID but got: %s", result)
		}
	} else {
		if verdict == verdictValid {
			return verdict, fmt.Errorf("expected the document to be rejected but got: %s", result)
		}
	}
	return verdict, nil
}

// sweptKnownFailureMaps names every known-failure map that has a staleness
// sweep, against the sweep that carries it. An entry here is a claim that an
// entry in that map naming a case the corpus no longer has will be reported.
var sweptKnownFailureMaps = map[string]string{
	"knownValidationFailures":    "TestExternalValidation, via reportStaleKnownFailures",
	"knownUnvalidatedRejections": "TestExternalValidation, via reportStaleUnvalidated",
	"knownFlakyTests":            "every test in flakyConsumers, via flakySweep.finish",
}

// unsweptKnownFailureMaps names every known-failure map that has none, and is
// held empty by TestUnsweptKnownFailureMapsAreEmpty instead. The sizes are
// behind funcs so that the two registries can be read without depending on
// package variable initialisation order.
var unsweptKnownFailureMaps = map[string]func() int{
	"knownParseFailures":     func() int { return len(knownParseFailures) },
	"knownCodeGenFailures":   func() int { return len(knownCodeGenFailures) },
	"knownRoundTripFailures": func() int { return len(knownRoundTripFailures) },
}

// TestUnsweptKnownFailureMapsAreEmpty keeps the known-failure maps that have no
// staleness sweep from acquiring entries that could go stale unnoticed.
//
// Two of the six maps are swept where they are read, by the one test that owns
// them, and knownFlakyTests is swept across the four tests that share it. These
// three are not, because each would need its own ledger threaded through a
// different test with a different notion of which groups it visits, and all
// three are empty -- writing three sweeps that can never fire, and cannot be
// watched failing against anything real, would be three decorative guards.
//
// Empty is therefore the property to hold, and it is a stronger property than a
// sweep: an empty map has nothing that can go stale. This is where it is held.
// The moment one of them gains an entry, the entry needs the sweep, because an
// entry naming a case upstream has since renamed reads as an outstanding defect
// while suppressing nothing -- which is exactly what the draft 3 lookbehind entry
// did across the #121 corpus bump. knownFlakyTests left this list by acquiring
// the sweep (#143), not by being excused from it, and nothing else may leave it
// on other terms.
//
// It deliberately does not need the corpus on disk, so it runs under plain
// `go test ./...` rather than only under the external gate.
func TestUnsweptKnownFailureMapsAreEmpty(t *testing.T) {
	names := make([]string, 0, len(unsweptKnownFailureMaps))
	for name := range unsweptKnownFailureMaps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if size := unsweptKnownFailureMaps[name](); size != 0 {
			t.Errorf("%s has %d entr(ies) but no staleness sweep, so an entry whose case upstream renames "+
				"would stop being consulted in silence. Give it a keyLedger and a reportStaleKnownFailures "+
				"call in the test that reads it (TestExternalValidation is the worked example, and "+
				"flakySweep is the worked example for a map more than one test reads), then move it from "+
				"unsweptKnownFailureMaps to sweptKnownFailureMaps", name, size)
		}
	}
}

// TestExternalParsing tests that we can parse every schema in the external test suite.
func TestExternalParsing(t *testing.T) {
	requireTestSuite(t)

	var walkedFiles int
	for _, draft := range allDrafts {
		t.Run(draft, func(t *testing.T) {
			draftDir := filepath.Join(jstsBaseDir, draft)
			if _, err := os.Stat(draftDir); os.IsNotExist(err) {
				t.Skipf("draft directory %s not found", draft)
				return
			}

			files := listJSONFiles(t, draftDir)
			for _, file := range files {
				t.Run(filenameWithoutExt(file), func(t *testing.T) {
					walkedFiles++
					groups := loadTestGroups(t, filepath.Join(draftDir, file))
					for _, group := range groups {
						// Offered outside the subtest, so that a -run filter
						// selecting some groups reads as offered-and-not-reached
						// rather than as a corpus that has lost them.
						key := flakySweep.offer(failureKey(draft, filenameWithoutExt(file), group.Description))
						t.Run(group.Description, func(t *testing.T) {
							err := tryParse(group.Schema)
							checkKnownFailure(t, key, err, knownParseFailures)
						})
					}
				})
			}
		})
	}
	flakySweep.finish(t, walkedFiles, countCorpusFiles(t))
}

// TestExternalCodeGen tests that we can generate compilable Go code from object-like schemas.
func TestExternalCodeGen(t *testing.T) {
	requireTestSuite(t)
	resolver := remotesResolver(t)

	var walkedFiles int
	for _, draft := range allDrafts {
		t.Run(draft, func(t *testing.T) {
			draftDir := filepath.Join(jstsBaseDir, draft)
			if _, err := os.Stat(draftDir); os.IsNotExist(err) {
				t.Skipf("draft directory %s not found", draft)
				return
			}

			files := listJSONFiles(t, draftDir)
			for _, file := range files {
				t.Run(filenameWithoutExt(file), func(t *testing.T) {
					walkedFiles++
					groups := loadTestGroups(t, filepath.Join(draftDir, file))
					for _, group := range groups {
						if !isCodeGenSuitable(group.Schema) {
							continue
						}
						key := flakySweep.offer(failureKey(draft, filenameWithoutExt(file), group.Description))
						t.Run(group.Description, func(t *testing.T) {
							err := tryGenerateAndCompile(group.Schema, resolver, isBignumFile(file))
							checkKnownFailure(t, key, err, knownCodeGenFailures)
						})
					}
				})
			}
		})
	}
	flakySweep.finish(t, walkedFiles, countCorpusFiles(t))
}

// TestExternalRoundTrip tests lossless JSON round-tripping through generated code.
func TestExternalRoundTrip(t *testing.T) {
	requireTestSuite(t)
	resolver := remotesResolver(t)

	var walkedFiles int
	for _, draft := range allDrafts {
		t.Run(draft, func(t *testing.T) {
			draftDir := filepath.Join(jstsBaseDir, draft)
			if _, err := os.Stat(draftDir); os.IsNotExist(err) {
				t.Skipf("draft directory %s not found", draft)
				return
			}

			files := listJSONFiles(t, draftDir)
			for _, file := range files {
				t.Run(filenameWithoutExt(file), func(t *testing.T) {
					walkedFiles++
					groups := loadTestGroups(t, filepath.Join(draftDir, file))
					for _, group := range groups {
						if !isCodeGenSuitable(group.Schema) {
							continue
						}

						// Collect valid test cases (objects, primitives, arrays, etc.)
						var validTests []jstsTestCase
						for _, tc := range group.Tests {
							if tc.Valid {
								validTests = append(validTests, tc)
							}
						}
						if len(validTests) == 0 {
							continue
						}

						caseKeys := make([]string, len(validTests))
						for i, tc := range validTests {
							caseKeys[i] = flakySweep.offer(failureKey(draft, filenameWithoutExt(file), group.Description, tc.Description))
						}
						t.Run(group.Description, func(t *testing.T) {
							for i, tc := range validTests {
								key := caseKeys[i]
								t.Run(tc.Description, func(t *testing.T) {
									err := tryRoundTrip(group.Schema, tc.Data, resolver, isBignumFile(file))
									checkKnownFailure(t, key, err, knownRoundTripFailures)
								})
							}
						})
					}
				})
			}
		})
	}
	flakySweep.finish(t, walkedFiles, countCorpusFiles(t))
}

// minValidatedGroups is the number of test groups this test reached a generated
// Validate() for, measured on 2026-08-04 against suite commit cf2e5e0: 2213 of
// the 2252 code-gen-suitable groups (2257 groups in the corpus, less the 5 whose
// schema is boolean `true`, which asserts nothing and so has nothing to test).
//
// It was 1765 of 1799 an hour earlier, and the whole of that jump is corpus
// rather than capability -- which is the one thing this number cannot show on
// its own, so it is recorded here. Adding the v1 draft to allDrafts brought 438
// suitable groups that had shipped in the corpus for months and had never been
// run (#121), and the suite bump from bce6a47 added 15 more across the six
// drafts already walked. 1799 + 438 + 15 = 2252, and 2213 of them produce a
// Validate. The skips went 34 → 39 for the same reason: v1 inherits 2020-12's
// root shapes, so it inherits its two allow-listed gaps and three more that
// carry no rejecting case.
//
// It was 1494 when this gate was written. Compiling the schemas that used to
// become `type X any` to the runtime evaluator raised it to 1586, and the 43
// knownUnvalidatedRejections entries those groups held were deleted. Giving a
// bare {"format":X} the wrapper issue #106 asks for -- a value held as a string
// when the instance is one, with a Validate that judges it -- raised it to 1752
// and took 88 more entries with it, which is what left knownUnvalidatedRejections
// with 23. Each one failed by name, saying it now produces a Validate(), rather
// than sitting in the list unread; that is the mechanism working.
//
// 1765 is the same three issues arriving together. #111 taught the evaluator
// unevaluatedProperties and #115 gave the content vocabulary the string wrapper
// #106 gave a format, so 13 more groups have something to call, the skips fall
// from 47 to 34, and none of them is a code-generation failure. Two more
// knownUnvalidatedRejections entries went stale with them. This number is the
// one the combined run printed, not an estimate: the branches were each measured
// separately or not at all, and no single-branch figure would have been true of
// the merge.
//
// It is a floor, not a target. A group that produces no Validate() produces no
// subtest either, so a change that stopped generating one would remove tests
// from the run rather than fail any — the run would get greener as it measured
// less. Falling below this number fails.
//
// Rising above it does not fail, only logs: coverage improves constantly, and
// every improvement on a group that matters — one carrying a document the suite
// says must be rejected — already fails loudly through its stale
// knownUnvalidatedRejections entry. Two failures for one event would train
// people to bump numbers rather than read them.
const minValidatedGroups = 2213

// TestExternalValidation tests that generated Validate() methods correctly accept
// valid data and reject invalid data according to the JSON Schema.
//
// Test structure:
//   - Schemas that fail code generation are skipped (TestExternalCodeGen owns
//     them) and counted.
//   - Schemas that produce no Validate() method are counted, and — when the
//     suite marks one of their documents invalid — reported as a failure unless
//     the group is allow-listed in knownUnvalidatedRejections. "No Validate()"
//     plus "this document must be rejected" means the generated code accepts a
//     document that must not be accepted, which is a defect whether or not
//     anyone has got around to it.
//   - Schemas that DO produce a Validate() method are tested per-case, with
//     both valid and invalid data. Only these per-case results use the
//     knownValidationFailures bidirectional checking.
//
// The closing summary asserts the coverage floor and reports how the corpus's
// must-reject documents were actually refused — by the decoder or by Validate().
func TestExternalValidation(t *testing.T) {
	requireTestSuite(t)
	resolver := remotesResolver(t)

	var totalGroups, skippedCG, skippedNoValidate, testedGroups int
	var noChecksGroups, noChecksWithRejection int
	var rejectedAtDecode, rejectedByValidate int
	// fates records what became of every group, so a knownUnvalidatedRejections
	// entry that describes none of them can say why it went stale.
	fates := make(map[string]groupFate, 2048)
	// ledger does the same job for knownValidationFailures, whose keys name a
	// single case rather than a whole group.
	ledger := newKeyLedger()
	var walkedFiles int

	for _, draft := range allDrafts {
		t.Run(draft, func(t *testing.T) {
			draftDir := filepath.Join(jstsBaseDir, draft)
			if _, err := os.Stat(draftDir); os.IsNotExist(err) {
				t.Skipf("draft directory %s not found", draft)
				return
			}

			files := listJSONFiles(t, draftDir)
			for _, file := range files {
				t.Run(filenameWithoutExt(file), func(t *testing.T) {
					walkedFiles++
					groups := loadTestGroups(t, filepath.Join(draftDir, file))
					for _, group := range groups {
						if !isCodeGenSuitable(group.Schema) {
							continue
						}
						totalGroups++
						groupKey := failureKey(draft, filenameWithoutExt(file), group.Description)

						// Generate code once per group.
						formatAssertion, formatAnnotation := formatPostureFor(file)
						code, cgErr := tryGenerateWithValidation(group.Schema, resolver, draftFromDir(draft), isBignumFile(file), formatAssertion, formatAnnotation)

						if cgErr != nil {
							skippedCG++
							fates[groupKey] = fateCodeGenError
							continue
						}
						if code == "" {
							skippedNoValidate++
							if !groupHasRejectingCase(group) {
								// Nothing is provable: every document here is
								// meant to be accepted, and a type with no
								// Validate() accepts everything.
								fates[groupKey] = fateNoRejection
								continue
							}
							fates[groupKey] = fateUnvalidated
							t.Run(group.Description, func(t *testing.T) {
								checkUnvalidatedRejection(t, groupKey)
							})
							continue
						}

						testedGroups++
						fates[groupKey] = fateValidated
						if rootValidateHasNoChecks(code) {
							noChecksGroups++
							if groupHasRejectingCase(group) {
								noChecksWithRejection++
							}
						}
						// Keys are offered here, outside the group's subtest,
						// so that a -run filter selecting some groups or some
						// cases shows up as offered-but-unvisited rather than as
						// a corpus that appears to have lost them.
						caseKeys := make([]string, len(group.Tests))
						for i, tc := range group.Tests {
							// Both ledgers see the key: this test's own, which
							// sweeps knownValidationFailures here, and the
							// process-wide one, which sweeps knownFlakyTests
							// once all four consumers have finished.
							caseKeys[i] = ledger.offer(flakySweep.offer(failureKey(draft, filenameWithoutExt(file), group.Description, tc.Description)))
						}
						t.Run(group.Description, func(t *testing.T) {
							for i, tc := range group.Tests {
								key := caseKeys[i]
								t.Run(tc.Description, func(t *testing.T) {
									ledger.visit(key)
									verdict, err := tryValidation(code, tc.Data, tc.Valid)
									if !tc.Valid {
										switch verdict {
										case verdictDecodeRejected:
											rejectedAtDecode++
										case verdictValidateRejected:
											rejectedByValidate++
										}
									}
									checkKnownFailure(t, key, err, knownValidationFailures)
								})
							}
						})
					}
				})
			}
		})
	}

	t.Logf("Validation coverage: %d/%d groups tested (%d skipped: %d codegen failures, %d no Validate() method)",
		testedGroups, totalGroups, skippedCG+skippedNoValidate, skippedCG, skippedNoValidate)

	// How the corpus's must-reject documents were actually refused. A decode
	// refusal is a real refusal — a caller decoding JSON gets an error — but it
	// is the Go type refusing, not a rule the generator emitted, so it proves
	// less than the name of this test suggests. It is reported rather than
	// failed: demanding that Validate() do the refusing would be demanding an
	// implementation, and `type Root int64` refusing "foo" is correct and
	// complete. The risk is drift, and drift is already red — widening such a
	// type turns the refusal into an acceptance, which fails the case outright.
	t.Logf("Rejections: %d by Validate(), %d at decode (json.Unmarshal refused the document; Validate() never ran)",
		rejectedByValidate, rejectedAtDecode)
	t.Logf("Of the %d tested groups, %d have a root Validate() whose body is exactly `return nil`, %d of those with a document the suite marks invalid",
		testedGroups, noChecksGroups, noChecksWithRejection)

	// Before the early return below, because knownFlakyTests's sweep has its own
	// notion of a partial run -- it is the only gate here that spans all four
	// external tests, and standing it down on this test's group count would make
	// it depend on which of the four ran last.
	flakySweep.finish(t, walkedFiles, countCorpusFiles(t))

	// The three gates below judge the whole corpus, so they are meaningless on a
	// run that only walked part of it. This check catches a -run filter that
	// selected some drafts or some files, which stops their groups being walked
	// at all; the ledger's own completeness check catches one that selected some
	// groups or some cases, which walks them and then tests a subset. Both are
	// needed, and neither sees the other's case.
	if corpusGroups := countCodeGenSuitableGroups(t); totalGroups != corpusGroups {
		t.Logf("partial run: walked %d of the corpus's %d code-gen-suitable groups (a -run filter on the subtests?). "+
			"The coverage floor and the two staleness sweeps judge the whole corpus and are skipped; "+
			"run without a subtest filter, or via 'make test-external', to exercise them",
			totalGroups, corpusGroups)
		return
	}

	reportStaleKnownFailures(t, "knownValidationFailures", knownValidationFailures, ledger)

	if testedGroups < minValidatedGroups {
		t.Errorf("validation coverage regressed from %d to %d groups (of %d code-gen-suitable groups): "+
			"%d fewer groups produced a Validate() method, so their cases were never run. "+
			"Fix the regression, or — if the corpus itself shrank — re-measure and lower minValidatedGroups",
			minValidatedGroups, testedGroups, totalGroups, minValidatedGroups-testedGroups)
	} else if testedGroups > minValidatedGroups {
		t.Logf("validation coverage improved from %d to %d groups — raise minValidatedGroups to %d",
			minValidatedGroups, testedGroups, testedGroups)
	}

	reportStaleUnvalidated(t, fates)
}
