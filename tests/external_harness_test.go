package tests

import (
	"os"
	"path/filepath"
	"reflect"
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
