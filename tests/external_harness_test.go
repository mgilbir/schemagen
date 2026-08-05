package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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
// The three that matter are the last three. "optional/format-annotation.json"
// is v1's opt-out and "optional/format/..." is 2019-09 and 2020-12's opt-in, and
// the first name starts with the second's prefix while meaning the opposite. A
// substring test in the wrong order silently generates v1's annotation cases
// under forced assertion, which turns 20 documents the file marks valid into
// rejections -- the one failure this repository treats as worse than a missing
// check.
//
// "optional/format-assertion.json" shares that prefix too and asks about neither
// posture: its schemas declare a custom metaschema whose $vocabulary asks for
// assertion, and the file exists to find out whether an implementation reads it.
// Forcing the flag would let it pass with the declaration unread, which is
// exactly how it passed while the generator ignored $vocabulary entirely.
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
		{"optional/format-assertion.json", false, false},
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

// TestMissingDraftDirsRefusesAPartialCheckout covers the one input that makes
// every staleness sweep in this package lie at once.
//
// A missing draft directory used to read as "the corpus does not have this
// draft" on both sides of every sweep: the walks skipped it and the counting
// functions skipped it too, so the counts agreed, nothing looked filtered, the
// sweeps ran, and every known-failure entry naming the absent draft was reported
// stale. Deleting live suppressions on that advice is the failure this whole
// mechanism exists to prevent, arriving through the mechanism itself.
//
// It is theoretical only because the corpus is pinned, which is a guarantee made
// in the Makefile. This is where the dependency on it is written down and
// enforced.
//
// The fixture is bare directories on purpose, and that is faithful rather than
// synthetic: missingDraftDirs stats a directory and reads nothing inside it, so
// a directory is the whole of its input. The half of the check that needs a real
// checkout is below, and takes one when there is one.
func TestMissingDraftDirsRefusesAPartialCheckout(t *testing.T) {
	root := t.TempDir()
	for _, draft := range allDrafts {
		if err := os.MkdirAll(filepath.Join(root, draft), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if got := missingDraftDirs(root); len(got) != 0 {
		t.Fatalf("missingDraftDirs on a complete checkout = %v, want none", got)
	}

	// v1 is the draft this actually happened to: it shipped in the corpus for
	// months while allDrafts named six, and 438 groups went unrun (#121). The
	// opposite state — allDrafts naming it and the checkout not having it — is
	// what this refuses.
	if err := os.RemoveAll(filepath.Join(root, "v1")); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "draft3")); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if got, want := missingDraftDirs(root), []string{"draft3", "v1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missingDraftDirs = %v, want %v — an absent draft directory has to be reported as an "+
			"incomplete checkout, or every sweep reports its entries stale", got, want)
	}

	// And the real checkout, when this repository has one. The pin says all
	// seven are there; a bare-directory fixture cannot say whether the names in
	// allDrafts are the names on disk, and this can.
	if _, err := os.Stat(jstsBaseDir); err == nil {
		if got := missingDraftDirs(jstsBaseDir); len(got) != 0 {
			t.Errorf("the checkout at %s is missing %v — run 'make download-test-suite'", jstsBaseDir, got)
		}
	}
}

// TestUnlistedDraftDirsRefusesAnUnrunDraft is the same coupling read the other
// way, and it is the direction that has already cost something: v1 shipped in
// the corpus for months while allDrafts named six drafts, so 438 groups were
// never run and every figure this suite reports read as full coverage (#121).
func TestUnlistedDraftDirsRefusesAnUnrunDraft(t *testing.T) {
	root := t.TempDir()
	for _, draft := range allDrafts {
		if err := os.MkdirAll(filepath.Join(root, draft), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	// The pinned checkout's own tests/latest, which points at draft2020-12 and
	// must stay skipped: walking it would run that draft twice and double every
	// figure derived from it.
	if err := os.Symlink(filepath.Join(root, "draft2020-12"), filepath.Join(root, "latest")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// A README, because the suite ships one at this level: a file is not a draft.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := unlistedDraftDirs(root)
	if err != nil {
		t.Fatalf("unlistedDraftDirs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unlistedDraftDirs on the pinned layout = %v, want none — a symlink to a listed draft and "+
			"a plain file are not unrun drafts", got)
	}

	// The plant: the next draft arrives upstream and nobody adds it to allDrafts.
	if err := os.MkdirAll(filepath.Join(root, "v2"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err = unlistedDraftDirs(root)
	if err != nil {
		t.Fatalf("unlistedDraftDirs: %v", err)
	}
	if want := []string{"v2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unlistedDraftDirs = %v, want %v — a draft directory nothing walks is a corpus this suite "+
			"reports on without running", got, want)
	}

	if _, err := os.Stat(jstsBaseDir); err == nil {
		got, err := unlistedDraftDirs(jstsBaseDir)
		if err != nil {
			t.Fatalf("unlistedDraftDirs: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("the checkout at %s has %v, which allDrafts does not name and nothing runs", jstsBaseDir, got)
		}
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

	// The plant for the count comparison this used to be. Three offered, three
	// visited, and one of the three never reached: len(offered) == len(visited)
	// answers "complete" over a gap the sweep would then read as three vanished
	// cases. Only containment sees it.
	miscounted := newKeyLedger()
	for _, key := range []string{"a", "b", "c"} {
		miscounted.offer(key)
	}
	miscounted.visit("a")
	miscounted.visit("b")
	miscounted.visit("never offered")
	if len(miscounted.offered) != len(miscounted.visited) {
		t.Fatalf("this plant is only about a count comparison if the counts coincide: offered %d, visited %d",
			len(miscounted.offered), len(miscounted.visited))
	}
	if miscounted.complete() {
		t.Error("a run that never reached \"c\" is not complete, whatever the two sizes are — complete() has " +
			"gone back to comparing counts, and the sweep will report live entries as vanished upstream")
	}
	if got, want := miscounted.unofferedVisits(), []string{"never offered"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unofferedVisits = %v, want %v", got, want)
	}
	if got := full.unofferedVisits(); len(got) != 0 {
		t.Errorf("a run that offered every key it reached has no unoffered visits, got %v", got)
	}
}

// TestUnofferedVisitDoesNotSweepAStaleEntryAway is the second half of the same
// defect, and the sharper one.
//
// An unoffered visit does not only hide a gap from complete(); it marks the key
// as carried, so an entry naming it is swept up as live. The ledger would then be
// reporting on a case the corpus does not have, which is precisely the lie the
// whole mechanism exists to catch, arriving from inside the mechanism.
func TestUnofferedVisitDoesNotSweepAStaleEntryAway(t *testing.T) {
	l := newKeyLedger()
	l.visit(l.offer("draft2020-12/type/integer type matches integers/an integer is an integer"))
	// Reached without being offered: the shape a refactor produces when a key is
	// built in the subtest rather than handed to it.
	l.visit("draft2020-12/type/integer type matches integers/a float is no integer")

	known := map[string]string{"draft2020-12/type/integer type matches integers/a float is no integer": "renamed upstream"}
	if got := staleKnownFailureKeys(known, l); len(got) != 0 {
		t.Fatalf("staleKnownFailureKeys = %v; a visited key reads as carried however it was reached, "+
			"which is why the ledger has to notice the visit was never offered", got)
	}
	if got := l.unofferedVisits(); len(got) != 1 {
		t.Errorf("unofferedVisits = %v, want the one key that was reached without being offered — without "+
			"it nothing at all reports that stale entry", got)
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

// replayValidationRun builds what an unfiltered TestExternalValidation leaves
// behind for its two end-of-run sweeps: the corpus walk's own answer, the fate
// of every group it walked, and the ledger of every case key it offered.
//
// It replays the placement, which is the property under test — the fate and the
// offers are recorded during the walk, outside the group subtest, so that a -run
// filter below the file level shows up as a case not reached rather than a case
// the corpus has lost. Nothing here needs a subtest, because nothing here is
// supposed to depend on one.
func replayValidationRun(groups []flakyFixtureGroup) (map[string][]string, map[string]groupFate, *keyLedger) {
	corpus := make(map[string][]string)
	fates := make(map[string]groupFate)
	l := newKeyLedger()
	for _, g := range groups {
		if !g.suitable {
			// Not code-gen-suitable: TestExternalValidation does not count it,
			// and corpusCodeGenSuitableGroups does not return it.
			continue
		}
		key := failureKey(g.draft, g.file, g.group)
		corpus[key] = g.cases
		switch {
		case g.validates:
			fates[key] = fateValidated
			for _, c := range g.cases {
				l.visit(l.offer(failureKey(key, c)))
			}
		case len(g.valid) < len(g.cases):
			fates[key] = fateUnvalidated
		default:
			fates[key] = fateNoRejection
		}
	}
	return corpus, fates, l
}

// TestWalkRecordGapsCatchesARecordFilledInsideTheSubtest plants the refactor that
// would make both of TestExternalValidation's sweeps report live entries as
// stale, and that nothing used to notice.
//
// Both sweeps judge a record filled during the walk. Both are safe under a -run
// filter selecting some groups or some cases only because the record is filled
// *outside* the group subtest: such a filter still walks every file, so the
// coverage check sees the whole corpus and stands nobody down. Move either write
// inside the subtest and the record shrinks to the selected groups while every
// other check still reads as a full run — reportStaleUnvalidated then calls every
// unselected allow-list entry a group the suite no longer has, and the ledger,
// offering and reaching the same shrunken set, reads as complete and calls every
// unselected knownValidationFailures entry a case that vanished upstream.
//
// The two plants are that refactor under the two filter shapes it survives
// today: one selecting a group, one selecting a case.
func TestWalkRecordGapsCatchesARecordFilledInsideTheSubtest(t *testing.T) {
	const colors = "draft3/optional/format/color/validation of CSS colors"
	const integers = "draft2020-12/type/integer type matches integers"

	corpus, fates, ledger := replayValidationRun(flakyFixture)
	if unrecorded, unoffered := walkRecordGaps(corpus, fates, ledger); len(unrecorded) != 0 || len(unoffered) != 0 {
		t.Fatalf("an unfiltered run has no gaps: unrecorded %v, unoffered %v — if this fires, the rest of "+
			"this test is measuring the fixture rather than the guard", unrecorded, unoffered)
	}

	t.Run("a fate recorded inside the group subtest", func(t *testing.T) {
		corpus, fates, ledger := replayValidationRun(flakyFixture)
		// `-run TestExternalValidation/.../integer type matches integers` with
		// the fates write moved into the subtest: the colours group is walked,
		// counted, and no longer recorded.
		delete(fates, colors)
		unrecorded, unoffered := walkRecordGaps(corpus, fates, ledger)
		if want := []string{colors}; !reflect.DeepEqual(unrecorded, want) {
			t.Errorf("unrecorded = %v, want %v — this is the group whose knownUnvalidatedRejections entry "+
				"reportStaleUnvalidated would now call stale", unrecorded, want)
		}
		if len(unoffered) != 0 {
			t.Errorf("unoffered = %v; the colours group produces no Validate(), so it offers no case key "+
				"and must not be asked for one", unoffered)
		}
	})

	t.Run("a case key offered inside the group subtest", func(t *testing.T) {
		corpus, fates, ledger := replayValidationRun(flakyFixture)
		// `-run '.../an integer is an integer'` with the offer moved into the
		// subtest: the other case is neither offered nor reached, so the ledger
		// reads as complete over a corpus it only half walked.
		dropped := failureKey(integers, "a float is not an integer")
		delete(ledger.offered, dropped)
		delete(ledger.visited, dropped)
		if !ledger.complete() {
			t.Fatal("the ledger has to read as complete here, or the sweep would stand down on its own " +
				"and this plant would be demonstrating the wrong thing")
		}
		unrecorded, unoffered := walkRecordGaps(corpus, fates, ledger)
		if len(unrecorded) != 0 {
			t.Errorf("unrecorded = %v, want none: every group was still walked and recorded", unrecorded)
		}
		if want := []string{dropped}; !reflect.DeepEqual(unoffered, want) {
			t.Errorf("unoffered = %v, want %v — this is the case a knownValidationFailures entry would now "+
				"be told had vanished upstream", unoffered, want)
		}
	})

	t.Run("a case key reached but never offered", func(t *testing.T) {
		corpus, fates, ledger := replayValidationRun(flakyFixture)
		// The other way the offer goes missing: the call is dropped and the
		// visit inside the subtest is left to stand for it. The offer is the
		// record a -run filter is measured against and a visit cannot stand in
		// for it, so this has to be reported as a missing offer even though the
		// key is in the ledger.
		for key := range ledger.offered {
			delete(ledger.offered, key)
		}
		_, unoffered := walkRecordGaps(corpus, fates, ledger)
		want := []string{
			failureKey(integers, "an integer is an integer"),
			failureKey(integers, "a float is not an integer"),
		}
		if !reflect.DeepEqual(unoffered, want) {
			t.Errorf("unoffered = %v, want %v — the check has to read the offers; reading the visits too "+
				"lets a run that offers nothing at all look fully recorded", unoffered, want)
		}
	})
}

// TestStaleCaseCauseNamesTheRegression covers the second half of a stale report:
// what it tells the reader to do.
//
// A group that regresses to producing no Validate() takes its knownValidation-
// Failures entries out of the run, and they are reported stale — word for word
// the message a case renamed upstream produces, and the two ask for opposite
// actions. Deleting the entry is right for the rename and wrong for the
// regression, where it discards the record of a defect that has just stopped
// being measured. The coverage floor reports the same event separately, so
// nothing is missed; what the reader sees is two unrelated complaints, with the
// louder one pointing away from the cause. fates already knows which it is.
func TestStaleCaseCauseNamesTheRegression(t *testing.T) {
	const colors = "draft3/optional/format/color/validation of CSS colors"
	const integers = "draft2020-12/type/integer type matches integers"
	const anchor = "draft2020-12/anchor/same $anchor with different base uri"
	const siblingID = "draft7/ref/$ref prevents a sibling $id from changing the base uri"

	fates := map[string]groupFate{
		integers:  fateValidated,
		colors:    fateUnvalidated,
		anchor:    fateCodeGenError,
		siblingID: fateValidated,
	}

	for _, tt := range []struct {
		name, key string
		fates     map[string]groupFate
		want      []string
		absent    []string
	}{{
		name:  "a case renamed upstream",
		key:   integers + "/a float is no integer",
		fates: fates,
		// The one reading the sweep was written for, and the only one where
		// deleting the key is the right answer.
		want:   []string{`"` + integers + `"`, "which this run did test", "delete the key"},
		absent: []string{"is not stale"},
	}, {
		name:  "a group that stopped producing a Validate()",
		key:   colors + "/an invalid CSS color name",
		fates: fates,
		want: []string{"is not stale", `"` + colors + `"`, "no Validate() method",
			"coverage regression", "rather than deleting the key"},
	}, {
		name:   "a group that stopped compiling",
		key:    anchor + "/$ref resolves to /$defs/A/allOf/1",
		fates:  fates,
		want:   []string{"is not stale", `"` + anchor + `"`, "fails code generation", "TestExternalCodeGen"},
		absent: []string{"no Validate() method"},
	}, {
		// A case description carrying a slash, which 56 of them do in the pinned
		// corpus. Cutting the key at its last "/" would name a group that does
		// not exist and fall through to "no group in this run walked it" — the
		// answer that hides the regression, arrived at through a parse rather
		// than a lookup.
		name:   "a case description containing a slash",
		key:    siblingID + "/$ref resolves to /definitions/base_foo, data does not validate",
		fates:  fates,
		want:   []string{`"` + siblingID + `"`, "which this run did test"},
		absent: []string{"matches no case of any group"},
	}, {
		name:   "a group the corpus no longer has",
		key:    "draft2020-12/definitions/valid definition/valid definition schema",
		fates:  fates,
		want:   []string{"matches no case of any group this run walked", "delete the key"},
		absent: []string{"is not stale"},
	}, {
		// A sweep with no record of what became of each group cannot choose, and
		// must not appear to: it names both possibilities, as it always did.
		name:   "no record of the groups",
		key:    integers + "/a float is no integer",
		fates:  nil,
		want:   []string{"matches no case this run tested", "or its group stopped being tested at all"},
		absent: []string{"is not stale"},
	}} {
		t.Run(tt.name, func(t *testing.T) {
			got := staleCaseCause(tt.key, tt.fates)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("staleCaseCause(...) = %q,\n  which does not contain %q", got, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("staleCaseCause(...) = %q,\n  which must not contain %q", got, absent)
				}
			}
		})
	}
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
