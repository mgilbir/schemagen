package generator

import "testing"

// TestJSONPropertyToGoNameExportsEveryName is issue #254 at the level of the
// function that owns it.
//
// JSONPropertyToGoName's doc comment promises "a Go exported field name", and
// capitalizing is not that promise -- it is an attempt at it that a script
// without case defeats silently. unicode.ToUpper is the identity on 日, on ا and
// on ก, so a property named 日本語 produced a Go field named 日本語: a legal
// identifier, an unexported one, and therefore a field encoding/json ignores
// whatever its struct tag says. The property was dropped in both directions.
//
// The table is one assertion -- every output is exported -- stated as the exact
// name each input must produce, because "starts with an upper-case letter" is
// satisfied by a name that lost information on the way there. Each caseless case
// names the script it stands for: the defect is a property of the script, not of
// the language, so one representative per writing system is what says the fix is
// not a special case for Japanese.
//
// The controls carry the weight here. Cyrillic, Greek and Latin-1 have case, so
// they were always exported and a fix that prefixed them anyway would rename
// every field in every generated type that has ever used them -- a far worse
// regression than the bug. They are checked against their literal expected
// output for that reason.
func TestJSONPropertyToGoNameExportsEveryName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// The defect: scripts with no upper case. Before the fix each of these
		// returned its input unchanged, unexported.
		{"japanese", "日本語", "X日本語"},
		{"chinese", "中文", "X中文"},
		{"korean", "한국어", "X한국어"},
		{"arabic", "العربية", "Xالعربية"},
		{"hebrew", "עברית", "Xעברית"},
		{"thai", "ภาษาไทย", "Xภาษาไทย"},
		{"devanagari", "कमल", "Xकमल"},
		{"devanagari with combining marks", "हिन्दी", "Xहनद"}, // the marks are not identifier characters
		// Caseless first, cased tail: still unexported, because only the first
		// rune decides. This is the "merely contains caseless characters"
		// half of the pair, and it is the half that changes.
		{"caseless first, cased tail", "日本語abc", "X日本語abc"},
		// Devanagari digits (category Nd). Not a letter, so not a legal
		// identifier start either; see TestSanitizeGoIdentifier for the byte-vs-
		// rune bug that used to let this one through and break the emitter.
		{"devanagari digits", "१२३", "X१२३"},

		// Controls: these were already exported and must come out byte-identical.
		{"cased first, caseless tail", "abc日本語", "Abc日本語"},
		{"cyrillic has case", "привет", "Привет"},
		{"greek has case", "Ωμέγα", "Ωμέγα"},
		{"greek lower-case first", "ωμέγα", "Ωμέγα"},
		{"latin-1", "café", "Café"},
		{"ascii", "ok", "Ok"},
		{"ascii acronym", "api_url", "APIURL"},
		{"leading digit", "1a", "X1a"},
		{"leading underscore", "_x", "X"},
		{"empty", "", "X"},
		{"no identifier characters", "🎉", "X"},
		{"go keyword", "type", "Type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := JSONPropertyToGoName(tt.input)
			if got != tt.want {
				t.Errorf("JSONPropertyToGoName(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if !isExportedGoIdentifier(got) {
				t.Errorf("JSONPropertyToGoName(%q) = %q, which is not an exported Go identifier", tt.input, got)
			}
		})
	}
}

// TestSchemaNameToGoNameExportsACaselessName states the same promise for type
// names, which reach the identical code path by a different route: a $defs entry
// or a title in a caseless script named an unexported type, and an unexported
// type is not usable from the package the caller writes. Asserted separately
// because SchemaNameToGoName is a distinct exported entry point, and a future
// change that gives it its own derivation would otherwise regress unwatched.
func TestSchemaNameToGoNameExportsACaselessName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"日本語型", "X日本語型"},
		{"한국어-형", "X한국어형"},
		{"МойТип", "МойТип"}, // control: Cyrillic has case
		{"my-type", "MyType"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SchemaNameToGoName(tt.input)
			if got != tt.want {
				t.Errorf("SchemaNameToGoName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestCaselessNamesStayDistinct is the collision half of the "X" prefix, and it
// is deliberately not the claim that the prefix is injective, because it is not.
//
// This function's mapping has been lossy since long before #254: it strips what
// is not an identifier character and folds word boundaries, so "a-b" and "ab"
// have always arrived at "AB" together. The prefix adds exactly one new pair to
// that set -- a caseless name and the same name spelled with a leading X, 日本語
// and X日本語 -- and nothing else, because "X"+y can only equal z when z was
// already spelled that way. Two names that reduce to one Go name are a clash the
// generator has always had an answer for: generateStruct numbers them apart, and
// reports an error when a third property has taken the numbered name, so the
// outcome is distinct fields or a diagnostic, never a silent merge. The
// behavioural proof is TestCaselessPropertyNamesRoundTripAtEveryPosition, whose
// fixture declares 日本語, X日本語 and 日-本-語 in one object and requires all three
// values back.
//
// What this asserts is the boundary: names that do NOT differ only by that
// prefix must not be folded together by it. A fix that reached for something
// lossier -- transliteration, which sends 日本語 and 日本 to overlapping Latin --
// is what this would catch.
func TestCaselessNamesStayDistinct(t *testing.T) {
	distinct := []string{
		"日本語", "中文", "한국어", "日本", "語",
		"abc日本語", "日本語abc", // differ by where the cased run sits, not by a prefix
		"Xひらがな", // an X-spelled name whose twin is not in this list
	}
	seen := make(map[string]string, len(distinct))
	for _, n := range distinct {
		got := JSONPropertyToGoName(n)
		if other, dup := seen[got]; dup {
			t.Errorf("property names %q and %q both derive the Go name %q", other, n, got)
		}
		seen[got] = n
	}
}
