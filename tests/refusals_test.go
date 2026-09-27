package tests

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Corpus sweeps -- a check run over every schema under testdata/schemas --
// have to skip a schema the generator refuses: half the adversarial corpus is
// malformed on purpose, and refusing is a legitimate answer. But "refused" and
// "skipped" are the same word for a sweep, and that made every one of them
// shrinkable in silence. A generator change that started refusing every schema
// carrying an enum, a const or a oneOf took TestGeneratedCorpusCompiles from 61
// refused schemas to 103 in the 2026-09-26 audit's plant, and it still passed:
// 42 schemas had quietly left the one gate that compiles the corpus.
//
// So each sweep's refusals are pinned here, by path, and compared in both
// directions. A schema refused that is not listed is coverage lost, and fails.
// A listed schema that now generates is coverage gained, and fails too, so the
// list is kept exact rather than allowed to drift into an allowance -- delete
// the entry (the failure prints the literal to paste). A refusal whose reason
// changed while it stayed refused is not caught here; the reasons are logged
// so a reader can see them.

// pinnedRefusals is, per sweep, every corpus schema the generator refuses under
// that sweep's configuration. The comment on a corpus/default entry is the
// reason given when it was pinned.
var pinnedRefusals = map[string][]string{
	// generateForCompile: the CLI default, with the file resolver. Shared by
	// TestGeneratedCorpusCompiles and TestGeneratedCorpusIsFieldAligned.
	"corpus/default": {
		"testdata/schemas/adversarial/degen/null-dependentschemas-val.json",                   // #/dependentSchemas/a: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/degen/null-in-allof.json",                               // #/allOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/degen/null-in-defs.json",                                // #/$defs/a: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/degen/null-in-oneof.json",                               // #/oneOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/degen/null-in-prefixitems.json",                         // #/prefixItems/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/degen/null-in-properties.json",                          // #/properties/a: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/degen/null-patternprops-val.json",                       // #/patternProperties/^a: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/malformed/dynamicref-nonexistent.json",                  // cannot resolve $dynamicRef "#nope"
		"testdata/schemas/adversarial/malformed/recursiveref-nonexistent.json",                // cannot resolve $recursiveRef "#nope"
		"testdata/schemas/adversarial/malformed/ref-allof-oob.json",                           // cannot resolve $ref "#/allOf/17"
		"testdata/schemas/adversarial/malformed/ref-bad-escape.json",                          // cannot resolve $ref "#/%zz/foo"
		"testdata/schemas/adversarial/malformed/ref-deep-nonexistent.json",                    // cannot resolve $ref "#/$defs/a/b/c/d"
		"testdata/schemas/adversarial/malformed/ref-fragment-only-anchor.json",                // cannot resolve $ref "#missing-anchor"
		"testdata/schemas/adversarial/malformed/ref-huge-index.json",                          // cannot resolve $ref "#/items/99999999999999999999"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-allof-nonempty.json",       // cannot resolve $ref "#/allOf/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-allof.json",                // cannot resolve $ref "#/allOf/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-anyof.json",                // cannot resolve $ref "#/anyOf/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-extension.json",            // cannot resolve $ref "#/x-ext/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-items-draft7.json",         // cannot resolve $ref "#/items/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-items.json",                // cannot resolve $ref "#/items/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-oneof.json",                // cannot resolve $ref "#/oneOf/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-prefixitems-nonempty.json", // cannot resolve $ref "#/prefixItems/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-index-overflow-prefixitems.json",          // cannot resolve $ref "#/prefixItems/9227000000000000000"
		"testdata/schemas/adversarial/malformed/ref-items-oob.json",                           // cannot resolve $ref "#/items/5"
		"testdata/schemas/adversarial/malformed/ref-just-hash-slash.json",                     // cannot resolve $ref "#/"
		"testdata/schemas/adversarial/malformed/ref-negative-index.json",                      // cannot resolve $ref "#/items/-1"
		"testdata/schemas/adversarial/malformed/ref-nonexistent.json",                         // cannot resolve $ref "#/$defs/nope"
		"testdata/schemas/adversarial/malformed/ref-only-slash.json",                          // cannot resolve $ref "/"
		"testdata/schemas/adversarial/malformed/ref-prefixitems-oob.json",                     // cannot resolve $ref "#/prefixItems/99"
		"testdata/schemas/adversarial/malformed/ref-remote-http.json",                         // cannot resolve $ref "http://example.invalid/schema.json"
		"testdata/schemas/adversarial/malformed/ref-tilde-invalid.json",                       // cannot resolve $ref "#/$defs/a~9b"
		"testdata/schemas/adversarial/malformed/ref-to-defs-map.json",                         // cannot resolve $ref "#/$defs"
		"testdata/schemas/adversarial/malformed/ref-to-extension-oob.json",                    // cannot resolve $ref "#/examples/99"
		"testdata/schemas/adversarial/malformed/ref-to-required.json",                         // cannot resolve $ref "#/required/0"
		"testdata/schemas/adversarial/malformed/ref-to-type-keyword.json",                     // cannot resolve $ref "#/type"
		"testdata/schemas/adversarial/malformed/ref-urn.json",                                 // cannot resolve $ref "urn:uuid:deadbeef"
		"testdata/schemas/adversarial/malformed/ref-with-space.json",                          // cannot resolve $ref "#/$defs/a b"
		"testdata/schemas/adversarial/nil2/allof-null-and-obj.json",                           // #/allOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/anyof-null.json",                                   // #/anyOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/definitions-null.json",                             // #/$defs/a: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/defs-null-noref.json",                              // #/$defs/a: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/dependentschemas-null-min.json",                    // #/dependentSchemas/a: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/extends-array-null.json",                           // #/allOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/if-allof-null.json",                                // #/if/allOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/items-allof-null.json",                             // #/items/allOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/items-array-null.json",                             // #/items/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/nested-allof-null.json",                            // #/properties/a/allOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/nested-defs-null.json",                             // #/$defs/a/$defs/b: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/nested-dependentschemas-null.json",                 // #/properties/a/dependentSchemas/x: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/nested-oneof-null.json",                            // #/properties/a/oneOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/nested-patternprops-null.json",                     // #/properties/a/patternProperties/^x: schema is null (a schema must be an object or bool...
		"testdata/schemas/adversarial/nil2/not-allof-null.json",                               // #/not/allOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/oneof-null-min.json",                               // #/oneOf/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/patternprops-null-min.json",                        // #/patternProperties/^a: schema is null (a schema must be an object or boolean)
		"testdata/schemas/adversarial/nil2/prefixitems-null-min.json",                         // #/prefixItems/0: schema is null (a schema must be an object or boolean)
		"testdata/schemas/regression/two_callers_2019/a.json",                                 // generating root type: type Root: a $recursiveRef under this schema is answered by the $...
		"testdata/schemas/regression/two_callers_2019/b.json",                                 // generating root type: type Root: a $recursiveRef under this schema is answered by the $...
		"testdata/schemas/regression/two_callers_2019/main.json",                              // generating root type: type Root: a $recursiveRef under this schema is answered by the $...
		"testdata/schemas/regression/two_callers_2020/a.json",                                 // generating root type: type Root: a $dynamicRef under this schema is answered by $dynami...
		"testdata/schemas/regression/two_callers_2020/b.json",                                 // generating root type: type Root: a $dynamicRef under this schema is answered by $dynami...
		"testdata/schemas/regression/two_callers_2020/main.json",                              // generating root type: type Root: a $dynamicRef under this schema is answered by $dynami...
	},
	// generateExact: the CLI default plus --exact-numbers. The same set as the
	// default today; the flag refuses nothing on its own.
	"corpus/exact-numbers": {
		"testdata/schemas/adversarial/degen/null-dependentschemas-val.json",
		"testdata/schemas/adversarial/degen/null-in-allof.json",
		"testdata/schemas/adversarial/degen/null-in-defs.json",
		"testdata/schemas/adversarial/degen/null-in-oneof.json",
		"testdata/schemas/adversarial/degen/null-in-prefixitems.json",
		"testdata/schemas/adversarial/degen/null-in-properties.json",
		"testdata/schemas/adversarial/degen/null-patternprops-val.json",
		"testdata/schemas/adversarial/malformed/dynamicref-nonexistent.json",
		"testdata/schemas/adversarial/malformed/recursiveref-nonexistent.json",
		"testdata/schemas/adversarial/malformed/ref-allof-oob.json",
		"testdata/schemas/adversarial/malformed/ref-bad-escape.json",
		"testdata/schemas/adversarial/malformed/ref-deep-nonexistent.json",
		"testdata/schemas/adversarial/malformed/ref-fragment-only-anchor.json",
		"testdata/schemas/adversarial/malformed/ref-huge-index.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-allof-nonempty.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-allof.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-anyof.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-extension.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-items-draft7.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-items.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-oneof.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-prefixitems-nonempty.json",
		"testdata/schemas/adversarial/malformed/ref-index-overflow-prefixitems.json",
		"testdata/schemas/adversarial/malformed/ref-items-oob.json",
		"testdata/schemas/adversarial/malformed/ref-just-hash-slash.json",
		"testdata/schemas/adversarial/malformed/ref-negative-index.json",
		"testdata/schemas/adversarial/malformed/ref-nonexistent.json",
		"testdata/schemas/adversarial/malformed/ref-only-slash.json",
		"testdata/schemas/adversarial/malformed/ref-prefixitems-oob.json",
		"testdata/schemas/adversarial/malformed/ref-remote-http.json",
		"testdata/schemas/adversarial/malformed/ref-tilde-invalid.json",
		"testdata/schemas/adversarial/malformed/ref-to-defs-map.json",
		"testdata/schemas/adversarial/malformed/ref-to-extension-oob.json",
		"testdata/schemas/adversarial/malformed/ref-to-required.json",
		"testdata/schemas/adversarial/malformed/ref-to-type-keyword.json",
		"testdata/schemas/adversarial/malformed/ref-urn.json",
		"testdata/schemas/adversarial/malformed/ref-with-space.json",
		"testdata/schemas/adversarial/nil2/allof-null-and-obj.json",
		"testdata/schemas/adversarial/nil2/anyof-null.json",
		"testdata/schemas/adversarial/nil2/definitions-null.json",
		"testdata/schemas/adversarial/nil2/defs-null-noref.json",
		"testdata/schemas/adversarial/nil2/dependentschemas-null-min.json",
		"testdata/schemas/adversarial/nil2/extends-array-null.json",
		"testdata/schemas/adversarial/nil2/if-allof-null.json",
		"testdata/schemas/adversarial/nil2/items-allof-null.json",
		"testdata/schemas/adversarial/nil2/items-array-null.json",
		"testdata/schemas/adversarial/nil2/nested-allof-null.json",
		"testdata/schemas/adversarial/nil2/nested-defs-null.json",
		"testdata/schemas/adversarial/nil2/nested-dependentschemas-null.json",
		"testdata/schemas/adversarial/nil2/nested-oneof-null.json",
		"testdata/schemas/adversarial/nil2/nested-patternprops-null.json",
		"testdata/schemas/adversarial/nil2/not-allof-null.json",
		"testdata/schemas/adversarial/nil2/oneof-null-min.json",
		"testdata/schemas/adversarial/nil2/patternprops-null-min.json",
		"testdata/schemas/adversarial/nil2/prefixitems-null-min.json",
		"testdata/schemas/regression/two_callers_2019/a.json",
		"testdata/schemas/regression/two_callers_2019/b.json",
		"testdata/schemas/regression/two_callers_2019/main.json",
		"testdata/schemas/regression/two_callers_2020/a.json",
		"testdata/schemas/regression/two_callers_2020/b.json",
		"testdata/schemas/regression/two_callers_2020/main.json",
	},
	// TestHelperFileDeclaresEveryHelperCalled over testdata/schemas without the
	// adversarial corpus, format as the dialect says. Both two_callers pairs are
	// refused on purpose: see tests/two_callers_test.go.
	"helper-file/dialect": {
		"testdata/schemas/regression/two_callers_2019/a.json",
		"testdata/schemas/regression/two_callers_2019/b.json",
		"testdata/schemas/regression/two_callers_2019/main.json",
		"testdata/schemas/regression/two_callers_2020/a.json",
		"testdata/schemas/regression/two_callers_2020/b.json",
		"testdata/schemas/regression/two_callers_2020/main.json",
	},
	// The same, with --format-assertion.
	"helper-file/format-assertion": {
		"testdata/schemas/regression/two_callers_2019/a.json",
		"testdata/schemas/regression/two_callers_2019/b.json",
		"testdata/schemas/regression/two_callers_2019/main.json",
		"testdata/schemas/regression/two_callers_2020/a.json",
		"testdata/schemas/regression/two_callers_2020/b.json",
		"testdata/schemas/regression/two_callers_2020/main.json",
	},
}

// refusalLedger collects one sweep's refusals.
type refusalLedger struct {
	sweep   string
	refused map[string]string // path -> reason
	checked int
}

func newRefusalLedger(sweep string) *refusalLedger {
	return &refusalLedger{sweep: sweep, refused: map[string]string{}}
}

// generated records a schema the sweep was able to check.
func (l *refusalLedger) generated() { l.checked++ }

// refuse records a schema the generator declined, and why.
func (l *refusalLedger) refuse(path string, reason error) {
	l.refused[corpusRel(path)] = fmt.Sprint(reason)
}

// corpusRel is a corpus path as it is spelled in pinnedRefusals: relative to
// the repository root, with forward slashes.
func corpusRel(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "../")
}

// check holds the ledger to the pinned set.
func (l *refusalLedger) check(t *testing.T) {
	t.Helper()
	pinned, ok := pinnedRefusals[l.sweep]
	if !ok {
		t.Fatalf("no pinned refusal set for sweep %q", l.sweep)
	}
	want := map[string]bool{}
	for _, p := range pinned {
		want[p] = true
	}
	var gained, lost []string
	for p := range l.refused {
		if !want[p] {
			lost = append(lost, p)
		}
	}
	for p := range want {
		if _, still := l.refused[p]; !still {
			gained = append(gained, p)
		}
	}
	sort.Strings(lost)
	sort.Strings(gained)
	t.Logf("%s: %d schemas checked, %d refused by the generator (pinned: %d)", l.sweep, l.checked, len(l.refused), len(pinned))
	for _, p := range lost {
		t.Errorf("%s: the generator now refuses %s (%s), which this sweep used to check; "+
			"a refusal is coverage lost, so it is a failure unless it is meant -- if it is, add it to "+
			"pinnedRefusals[%q]", l.sweep, p, l.refused[p], l.sweep)
	}
	for _, p := range gained {
		t.Errorf("%s: %s is pinned as refused but now generates, so this sweep checks it; "+
			"remove it from pinnedRefusals[%q]", l.sweep, p, l.sweep)
	}
	if len(lost) > 0 || len(gained) > 0 {
		var b strings.Builder
		all := make([]string, 0, len(l.refused))
		for p := range l.refused {
			all = append(all, p)
		}
		sort.Strings(all)
		fmt.Fprintf(&b, "the refusal set as it stands, to paste once the change is understood:\n\t%q: {\n", l.sweep)
		for _, p := range all {
			fmt.Fprintf(&b, "\t\t%q,\n", p)
		}
		b.WriteString("\t},")
		t.Log(b.String())
	}
}
