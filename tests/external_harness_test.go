package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// The tests in this file exercise the external harness's own decisions -- which
// generator configuration a suite file is asking about, which files are walked
// at all, and when a known-failure entry has gone stale. They need no corpus and
// run under plain `go test ./...`, which is the point: each of these decisions
// is otherwise only exercised by a 50-minute run that would have to be sabotaged
// to see the guard fire once, and a guard never watched failing proves nothing.

// TestFormatPostureForFile pins which of the suite's two format postures each
// file asks about.
//
// The pair that matters is the last two. "optional/format-annotation.json" is
// v1's opt-out and "optional/format/..." is 2019-09 and 2020-12's opt-in, and
// the first name starts with the second's prefix while meaning the opposite. A
// substring test in the wrong order silently generates v1's annotation cases
// under forced assertion, which turns 20 documents the file marks valid into
// rejections -- the one failure this repository treats as worse than a missing
// check.
func TestFormatPostureForFile(t *testing.T) {
	for _, tt := range []struct {
		file                  string
		assertion, annotation bool
	}{
		{"format.json", false, false},
		{"properties.json", false, false},
		{"format/email.json", false, false}, // v1: required, so the dialect's own answer
		{"optional/bignum.json", false, false},
		{"optional/format/email.json", true, false},
		{"optional/format/idn-hostname.json", true, false},
		{"optional/format-assertion.json", true, false},
		{"optional/format-annotation.json", false, true},
	} {
		t.Run(tt.file, func(t *testing.T) {
			assertion, annotation := formatPostureFor(tt.file)
			if assertion != tt.assertion || annotation != tt.annotation {
				t.Errorf("formatPostureFor(%q) = (assertion=%v, annotation=%v), want (%v, %v)",
					tt.file, assertion, annotation, tt.assertion, tt.annotation)
			}
		})
	}
	t.Run("windows separators", func(t *testing.T) {
		// filepath.Walk hands back the host separator, and every rule above is
		// written with forward slashes.
		if assertion, _ := formatPostureFor(filepath.FromSlash("optional/format/email.json")); !assertion {
			t.Error("a backslash-separated path must reach the same arm as a slash-separated one")
		}
	})
}

// TestListJSONFilesSkipsProposals checks that the walk leaves tests/v1/proposals
// out and takes everything else.
//
// The directory holds tests for keywords that are still proposals to the
// specification -- propertyDependencies today -- which no dialect this generator
// reads defines. Every draft says to ignore a keyword it does not know, so those
// schemas generate correct-but-permissive types and their must-reject documents
// would be reported as defects against a keyword nobody has agreed on.
func TestListJSONFilesSkipsProposals(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"type.json",
		"optional/bignum.json",
		"format/email.json",
		"proposals/propertyDependencies/propertyDependencies.json",
		"proposals/README.md",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	var got []string
	for _, f := range listJSONFiles(t, root) {
		got = append(got, filepath.ToSlash(f))
	}
	want := []string{"format/email.json", "optional/bignum.json", "type.json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listJSONFiles = %v, want %v", got, want)
	}
}

// TestStaleKnownFailureKeys covers the sweep that the #121 corpus bump showed
// was missing.
//
// checkKnownFailure judges a key some case still carries, in both directions. It
// cannot say anything about an entry naming a case the corpus no longer has, and
// that is what a suite bump produces: upstream renamed draft 3's "ECMA 262 has
// no support for lookbehind" and flipped it to valid, so the entry covering it
// stopped being consulted and went on reading as an outstanding defect while
// suppressing nothing.
func TestStaleKnownFailureKeys(t *testing.T) {
	ledger := newKeyLedger()
	for _, key := range []string{"d/f/g/still here", "d/f/g/also here"} {
		ledger.visit(ledger.offer(key))
	}
	// The renamed case: offered under its new description, so the old key is
	// carried by nothing.
	ledger.visit(ledger.offer("d/f/g/renamed upstream"))

	known := map[string]string{
		"d/f/g/still here":      "a real outstanding failure",
		"d/f/g/gone upstream":   "names a case the corpus no longer has",
		"d/f/g/never existed":   "a typo in the key",
		"d/f/g/renamed upsteam": "the same case, spelt as it used to be",
	}
	got := staleKnownFailureKeys(known, ledger)
	want := []string{"d/f/g/gone upstream", "d/f/g/never existed", "d/f/g/renamed upsteam"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("staleKnownFailureKeys = %v, want %v", got, want)
	}
	if len(staleKnownFailureKeys(map[string]string{}, ledger)) != 0 {
		t.Error("an empty known-failure map has no stale entries")
	}
}

// TestKeyLedgerCompleteness checks the guard that stops the sweep from firing on
// a filtered run.
//
// A `-run` filter selecting some groups or some cases walks the corpus and then
// tests part of it, so keys are offered and never reached. Sweeping then would
// report every unselected entry as vanished upstream, which is the wrong answer
// loudly -- and it is the failure mode that would train someone to delete a live
// entry.
func TestKeyLedgerCompleteness(t *testing.T) {
	full := newKeyLedger()
	for _, key := range []string{"a", "b", "c"} {
		full.visit(full.offer(key))
	}
	if !full.complete() {
		t.Error("a run that reached every key it offered is complete")
	}

	filtered := newKeyLedger()
	for _, key := range []string{"a", "b", "c"} {
		filtered.offer(key)
	}
	filtered.visit("b")
	if filtered.complete() {
		t.Error("a run that reached one of the three keys it offered is not complete")
	}

	// Two groups in one file can share a description, and so can two cases in
	// one group, which makes the same key legal twice. Counting offers rather
	// than distinct keys would leave such a run permanently "incomplete" and
	// silently disable the sweep for the whole corpus.
	duplicated := newKeyLedger()
	duplicated.visit(duplicated.offer("a"))
	duplicated.visit(duplicated.offer("a"))
	if !duplicated.complete() {
		t.Error("a key offered twice and reached twice is still a complete run")
	}
}

// flakyFixtureGroup is one test group as the four external tests see it, which
// is not the same way twice: TestExternalParsing visits every group, the other
// three skip the ones that are not code-gen-suitable, TestExternalRoundTrip
// visits only the cases the suite marks valid, and TestExternalValidation visits
// no case at all unless the group produced a Validate(). Those disagreements are
// the whole difficulty knownFlakyTests's sweep has to survive, so the fixture
// carries them rather than pretending the four tests walk the same corpus.
type flakyFixtureGroup struct {
	draft, file, group string
	cases              []string // every case, in suite order
	valid              []string // the subset the suite marks valid
	suitable           bool     // isCodeGenSuitable(schema)
	validates          bool     // code generation produced a Validate()
}

// flakyFixture is a slice of the real corpus, transcribed from the pinned
// checkout, and it is real for a reason: a guard watched failing against a
// fixture the real case never resembles has not been watched at all. Each of the
// three groups is a shape that actually occurs and that reaches a different
// subset of the four tests.
//
//   - boolean schema 'true' is not code-gen-suitable, so parsing is the only test
//     that ever carries its key. It is the case a per-test sweep gets wrong.
//   - the CSS colors group is suitable but resolves to `any`, so it carries no
//     Validate() and TestExternalValidation never reaches its cases; it is in
//     knownUnvalidatedRejections for exactly that reason.
//   - the integer group is the ordinary one that all four tests reach.
var flakyFixture = []flakyFixtureGroup{{
	draft: "draft2020-12", file: "boolean_schema", group: "boolean schema 'true'",
	cases: []string{"number is valid", "string is valid"},
	valid: []string{"number is valid", "string is valid"},
}, {
	draft: "draft3", file: "optional/format/color", group: "validation of CSS colors",
	cases:    []string{"a valid CSS color name", "an invalid CSS color name"},
	valid:    []string{"a valid CSS color name"},
	suitable: true,
}, {
	draft: "draft2020-12", file: "type", group: "integer type matches integers",
	cases:     []string{"an integer is an integer", "a float is not an integer"},
	valid:     []string{"an integer is an integer"},
	suitable:  true,
	validates: true,
}}

// replayFlakyConsumer drives the sweep the way the named external test drives
// it: keys offered outside the subtest, reached inside it, and the walk declared
// complete at the end.
func replayFlakyConsumer(s *flakySweepState, consumer string, groups []flakyFixtureGroup) {
	for _, g := range groups {
		groupKey := failureKey(g.draft, g.file, g.group)
		switch consumer {
		case "TestExternalParsing":
			s.record(consumer, s.offer(groupKey))
		case "TestExternalCodeGen":
			if g.suitable {
				s.record(consumer, s.offer(groupKey))
			}
		case "TestExternalRoundTrip":
			if g.suitable {
				for _, c := range g.valid {
					s.record(consumer, s.offer(failureKey(groupKey, c)))
				}
			}
		case "TestExternalValidation":
			if g.suitable && g.validates {
				for _, c := range g.cases {
					s.record(consumer, s.offer(failureKey(groupKey, c)))
				}
			}
		}
	}
	s.markWalked(consumer)
}

// replayFlakyRun replays every consumer in flakyConsumers over the fixture.
func replayFlakyRun(groups []flakyFixtureGroup) *flakySweepState {
	s := newFlakySweepState()
	for _, consumer := range flakyConsumers {
		replayFlakyConsumer(s, consumer, groups)
	}
	return s
}

// TestFlakySweepIsCorpusWide is the reason knownFlakyTests's sweep is not four
// per-test sweeps.
//
// The other four maps each belong to one test, so each can be swept against a
// ledger that test filled. knownFlakyTests is read by all four, and no one of
// them reaches all of its keys: "boolean schema 'true'" is not code-gen-suitable,
// so TestExternalParsing is the only test that ever carries it. A sweep run at
// the end of any other test would call that entry stale and invite whoever read
// the message to delete a live suppression -- the wrong answer, loudly, which is
// worse than the silence #143 is about.
//
// The second half plants exactly that: the naive design, a ledger holding one
// test's keys, judging the same map. It has to report the entry stale, or this
// test is asserting nothing.
func TestFlakySweepIsCorpusWide(t *testing.T) {
	const parsingOnly = "draft2020-12/boolean_schema/boolean schema 'true'"
	const roundTripOnly = "draft3/optional/format/color/validation of CSS colors/a valid CSS color name"
	known := map[string]bool{parsingOnly: true, roundTripOnly: true}

	shared := replayFlakyRun(flakyFixture)
	if got := shared.verdict(known); len(got.stale) != 0 {
		t.Errorf("the corpus-wide sweep reported %v stale; every one of those keys was carried by one of "+
			"the four tests, so none of them is", got.stale)
	}

	perTest := newFlakySweepState()
	replayFlakyConsumer(perTest, "TestExternalValidation", flakyFixture)
	// The other three are declared walked without replaying them, which is the
	// per-test sweep this design rejects: one test's ledger, judged as if it were
	// the corpus.
	for _, consumer := range flakyConsumers {
		perTest.markWalked(consumer)
	}
	got := perTest.verdict(known)
	want := []string{parsingOnly, roundTripOnly}
	if !reflect.DeepEqual(got.stale, want) {
		t.Errorf("a sweep over one test's ledger reported %v stale, want %v — if it reports nothing, this "+
			"test no longer demonstrates why the ledger is shared", got.stale, want)
	}
}

// TestFlakySweepReportsStaleEntry covers the defect the sweep exists for: an
// entry naming a case the corpus no longer has, which checkKnownFailure cannot
// say anything about because it is never consulted. That is what the draft 3
// lookbehind entry became when upstream renamed the case, and nothing said so.
func TestFlakySweepReportsStaleEntry(t *testing.T) {
	s := replayFlakyRun(flakyFixture)
	known := map[string]bool{
		// Still carried, by TestExternalCodeGen among others.
		"draft3/optional/format/color/validation of CSS colors": true,
		// Renamed upstream: the run offers "a float is not an integer".
		"draft2020-12/type/integer type matches integers/a float is no integer": true,
		// A whole file that no longer exists.
		"draft2020-12/definitions/valid definition": true,
	}
	got := s.verdict(known)
	want := []string{
		"draft2020-12/definitions/valid definition",
		"draft2020-12/type/integer type matches integers/a float is no integer",
	}
	if !reflect.DeepEqual(got.stale, want) {
		t.Errorf("verdict.stale = %v, want %v", got.stale, want)
	}
}

// TestFlakySweepStandsDownOnPartialRuns checks the two shapes of partial run,
// because reporting false staleness would train someone to delete a live entry.
//
// A -run filter above the group level (a draft, a file, or a whole test) stops
// keys being offered at all, so the ledger cannot see the gap and the file count
// has to; one below it walks the corpus and then tests a subset, which the ledger
// sees and the file count cannot. Neither check sees the other's case.
func TestFlakySweepStandsDownOnPartialRuns(t *testing.T) {
	stale := map[string]bool{"draft2020-12/type/integer type matches integers/renamed upstream": true}

	t.Run("a test that has not run", func(t *testing.T) {
		s := newFlakySweepState()
		for _, consumer := range flakyConsumers[:2] {
			replayFlakyConsumer(s, consumer, flakyFixture)
		}
		got := s.verdict(stale)
		if want := flakyConsumers[2:]; !reflect.DeepEqual(got.pending, want) {
			t.Errorf("verdict.pending = %v, want %v", got.pending, want)
		}
		if len(got.stale) != 0 {
			t.Errorf("a run missing two of the four consumers reported %v stale; it cannot know", got.stale)
		}
	})

	t.Run("a test that ran on part of the corpus", func(t *testing.T) {
		// finish() is what compares the walk against countCorpusFiles, so this
		// is the state it leaves behind: the consumer never marked walked.
		s := newFlakySweepState()
		for _, consumer := range flakyConsumers {
			replayFlakyConsumer(s, consumer, flakyFixture)
		}
		delete(s.walked, "TestExternalRoundTrip")
		if got := s.verdict(stale); len(got.stale) != 0 || len(got.pending) != 1 {
			t.Errorf("verdict = %+v, want one pending consumer and no staleness", got)
		}
	})

	t.Run("a filter below the file level", func(t *testing.T) {
		s := replayFlakyRun(flakyFixture)
		// A group or case the run offered and never reached, which is what a
		// -run filter on a group name leaves behind.
		s.offer("draft2020-12/type/number type matches numbers")
		got := s.verdict(stale)
		if !got.incomplete {
			t.Error("a run that offered a key it never reached is incomplete")
		}
		if len(got.stale) != 0 {
			t.Errorf("an incomplete run reported %v stale; it cannot tell an unselected case from a "+
				"vanished one", got.stale)
		}
	})
}

// TestFlakySweepReportsStrayConsumer covers the assumption flakyConsumers is:
// that those four tests are the only ones that read knownFlakyTests.
//
// A fifth test added later would read the map through checkKnownFailure like the
// others, but the sweep would not wait for it and would not know which keys only
// it carries -- so a run that filtered it out would report those keys as
// vanished. The list cannot be observed (a test that did not run leaves no
// trace), but a test that reads the map while absent from the list can be, and
// the first full run after it is written says so.
func TestFlakySweepReportsStrayConsumer(t *testing.T) {
	s := replayFlakyRun(flakyFixture)
	s.record("TestExternalSomethingNew", "draft2020-12/type/integer type matches integers")

	got := s.verdict(map[string]bool{})
	if want := []string{"TestExternalSomethingNew"}; !reflect.DeepEqual(got.stray, want) {
		t.Errorf("verdict.stray = %v, want %v", got.stray, want)
	}
	if got := s.verdict(map[string]bool{"draft2020-12/definitions/gone": true}); len(got.stale) != 0 {
		t.Errorf("a run with an untracked consumer reported %v stale; it does not know what that "+
			"consumer carries", got.stale)
	}
}

// TestTopLevelTest pins the name the sweep files a subtest under. Getting this
// wrong would file every subtest under its own name, so no consumer would ever
// match flakyConsumers and every run would report four stray consumers.
func TestTopLevelTest(t *testing.T) {
	for name, want := range map[string]string{
		"TestExternalParsing":                                  "TestExternalParsing",
		"TestExternalRoundTrip/draft3/type/an object":          "TestExternalRoundTrip",
		"TestExternalValidation/v1/optional/format-annotation": "TestExternalValidation",
	} {
		if got := topLevelTest(name); got != want {
			t.Errorf("topLevelTest(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestEveryKnownFailureMapIsClassified holds the line TestUnsweptKnownFailureMaps-
// AreEmpty used to hold on its own.
//
// That test names the maps it guards, so a map added to
// external_known_failures.go tomorrow and listed nowhere would be guarded by
// nothing at all -- neither swept nor held empty -- which is the silence this
// whole mechanism exists to end, arriving through the one door nobody watches.
// Every map declared in that file has to be classified as swept or unswept, and
// the classification is checked against the source rather than against a second
// list, so a rename breaks it too.
func TestEveryKnownFailureMapIsClassified(t *testing.T) {
	const src = "external_known_failures.go"
	file, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	declared := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value := spec.(*ast.ValueSpec)
			if !valueSpecIsMap(value) {
				continue
			}
			for _, name := range value.Names {
				declared[name.Name] = true
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("found no map declarations in %s — the parse found nothing to classify, so this test "+
			"would pass however many maps went unguarded", src)
	}

	// Sorted, so that a change adding two unclassified maps reports them in the
	// same order twice running.
	for _, name := range sortedKeys(declared) {
		_, swept := sweptKnownFailureMaps[name]
		_, unswept := unsweptKnownFailureMaps[name]
		switch {
		case swept && unswept:
			t.Errorf("%s is listed as both swept and unswept", name)
		case !swept && !unswept:
			t.Errorf("%s is declared in %s but is in neither sweptKnownFailureMaps nor "+
				"unsweptKnownFailureMaps, so nothing guards it: no staleness sweep reports an entry of "+
				"it that has gone stale, and TestUnsweptKnownFailureMapsAreEmpty does not hold it empty. "+
				"Give it a sweep and list it in the first, or list it in the second", name, src)
		}
	}
	for _, listed := range []map[string]bool{keysOf(sweptKnownFailureMaps), keysOf(unsweptKnownFailureMaps)} {
		for _, name := range sortedKeys(listed) {
			if !declared[name] {
				t.Errorf("%s is classified but is not declared in %s — it was renamed or moved, and the "+
					"classification now describes nothing", name, src)
			}
		}
	}
}

// valueSpecIsMap reports whether a var declares a map, whether the type is
// written out or left to the composite literal on the right.
func valueSpecIsMap(spec *ast.ValueSpec) bool {
	if _, ok := spec.Type.(*ast.MapType); ok {
		return true
	}
	for _, value := range spec.Values {
		if lit, ok := value.(*ast.CompositeLit); ok {
			if _, ok := lit.Type.(*ast.MapType); ok {
				return true
			}
		}
	}
	return false
}

// keysOf is the set of keys of a map, so the two classification registries can
// be walked without caring what they hold.
func keysOf[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// sortedKeys is keysOf's counterpart for reporting: map iteration order is
// deliberately random, and a failure list that reorders itself between runs
// cannot be diffed.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
