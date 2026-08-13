package schemagen

import (
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about two schema locations of one document whose Go
// names fold onto one string -- "X" and "!", "a~b" and "a/b", a nested a.b and a
// flat a_b -- where nothing the document wrote separates them once the name
// derivation has dropped the punctuation and the case.
//
// Left folded they were one Go type, and every reference to either of them
// reached it: the first location generated claimed the name and the re-entrancy
// guard turned the rest away, so a position was decoded and validated by a schema
// its document wrote somewhere else. It generated cleanly and exited 0. The
// direction of the harm follows the surviving schema -- a document the schema
// permits is refused when the survivor is the stricter one, and one it forbids is
// accepted when the survivor is the looser -- and each direction is exercised
// below, because only the first is visible from the outside.
//
// Issue #271. The names are numbered rather than qualified: two keys that fold
// have no third thing to be named after, which is what makes them fold. Which one
// keeps the unnumbered name is decided by the claims themselves, so it does not
// move with the order the inputs were listed or the keys were written.

// ---------- two $defs keys of one document ----------

// The reproducer, from the issue. "!" has no letters at all, so its Go name is
// the "X" that sanitizeGoIdentifier substitutes for an empty one, and it claimed
// the type $defs/X asked for. {"a":"s","b":1} -- which the document accepts --
// came back "cannot unmarshal string into Go value of type X": a had been given
// the *integer* definition.
func TestTwoDefsKeysFoldingOntoOneNameValidateTheirOwnSchema(t *testing.T) {
	_, paths := writeSchemas(t, "x.json", `{
		"title": "X",
		"$defs": {"X": {"type": "string"}, "!": {"type": "integer"}},
		"type": "object",
		"properties": {"a": {"$ref": "#/$defs/X"}, "b": {"$ref": "#/$defs/!"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "X",
		[]docInstance{
			{`{"a":"s","b":1}`, true, `{"a":"s","b":1}`},
			{`{"a":1}`, false, ""},
			{`{"b":"s"}`, false, ""},
		})
}

// The same fold with the definitions the other way round, which is the dangerous
// direction and the one no verdict can show from outside the generated package: the
// surviving type is the *looser* of the two, so the position it takes over accepts
// every document the schema it lost forbids. Here $defs/X bounds a string and "!"
// does not, "!" is generated first, and {"b":"AB"} -- two characters, upper case,
// forbidden by both minLength and pattern -- was accepted with no error at all.
func TestFoldedDefinitionsCannotAcceptWhatTheSchemaForbids(t *testing.T) {
	_, paths := writeSchemas(t, "fa.json", `{
		"title": "FA",
		"$defs": {
			"!": {"type": "string"},
			"X": {"type": "string", "minLength": 3, "pattern": "^[a-z]+$"}
		},
		"type": "object",
		"properties": {"a": {"$ref": "#/$defs/!"}, "b": {"$ref": "#/$defs/X"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "Fa",
		[]docInstance{
			{`{"b":"abc"}`, true, `{"b":"abc"}`},
			// Both of the bounds b carries, each of which the merge dropped.
			{`{"b":"AB"}`, false, ""},
			{`{"b":"ABC"}`, false, ""},
			// And a keeps its own definition, which states neither of them.
			{`{"a":"AB"}`, true, `{"a":"AB"}`},
		})
}

// The diagnostic, verbatim. A caller whose two definitions were about to be one
// type has to be told they are not, which of them kept the name, and what to
// change -- and the remedy is neither of the two the other shapes name: there is
// no root type in contention here and no second keyword, so both --root-name and
// "delete one of them" would be advice that does not apply.
//
// The line that says which key keeps the name is the whole of the rule. $defs/X
// keeps X because its key is already spelled as the Go name it derives, which is
// a property of the keys and not of the order they arrived in.
func TestFoldedDefsKeysReportWhatWasSplit(t *testing.T) {
	dir, paths := writeSchemas(t, "x.json", `{
		"title": "Doc",
		"$defs": {"X": {"type": "string"}, "!": {"type": "integer"}},
		"type": "object",
		"properties": {"a": {"$ref": "#/$defs/X"}, "b": {"$ref": "#/$defs/!"}}
	}`)

	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", filepath.Join(dir, "gen"), "-p", "gen")...)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := "warning: " + paths[0] + " declares the Go type name X in 2 places, and those declarations do not describe the same type, so they cannot be one:\n" +
		"  " + paths[0] + " $defs/! becomes X2\n" +
		"  " + paths[0] + " $defs/X keeps X\n" +
		"one Go package holds one type per name, so declaring them all as X would have given every $ref whichever was generated first and discarded the rest -- a position typed by a schema the document never wrote there. " +
		"These keys derive one Go name -- the derivation drops what separates them, as it does for \"my-type\" and \"my_type\", for the two spellings of a JSON Pointer escape, and for a key with no letters in it at all -- so the ones after the first are numbered. " +
		"Make the definitions identical if they were meant to be one type, or rename one of the keys in the schema -- to something that differs by more than punctuation or case -- to choose the Go names yourself.\n"
	if stderr != want {
		t.Errorf("stderr =\n%q\nwant\n%q", stderr, want)
	}
}

// The two spellings of a JSON Pointer escape. "a~b" and "a/b" are different keys
// naming different schemas, ~0 and ~1 reach them unambiguously, and both derive
// AB.
func TestPointerEscapesFoldingOntoOneNameValidateTheirOwnSchema(t *testing.T) {
	_, paths := writeSchemas(t, "t.json", `{
		"title": "T",
		"$defs": {"a~b": {"type": "string"}, "a/b": {"type": "integer"}},
		"type": "object",
		"properties": {"x": {"$ref": "#/$defs/a~0b"}, "y": {"$ref": "#/$defs/a~1b"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "T",
		[]docInstance{
			{`{"x":"s","y":1}`, true, `{"x":"s","y":1}`},
			{`{"x":1}`, false, ""},
			{`{"y":"s"}`, false, ""},
		})
}

// Three keys folding onto one name are numbered in turn, and each keeps its own
// schema. The third is what says the numbering is a sequence and not a swap.
func TestThreeFoldedDefsKeysAreNumberedInTurn(t *testing.T) {
	dir, paths := writeSchemas(t, "p.json", `{
		"title": "P",
		"$defs": {
			"X": {"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]},
			"!": {"type": "object", "properties": {"b": {"type": "integer"}}, "required": ["b"]},
			"?": {"type": "object", "properties": {"c": {"type": "boolean"}}, "required": ["c"]}
		},
		"type": "object",
		"properties": {
			"p": {"$ref": "#/$defs/X"}, "q": {"$ref": "#/$defs/!"}, "r": {"$ref": "#/$defs/?"}
		}
	}`)

	out := filepath.Join(dir, "gen")
	if _, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen")...); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "P,X,X2,X3" {
		t.Errorf("declared types = %s, want P,X,X2,X3", got)
	}

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "P",
		[]docInstance{
			{`{"p":{"a":"s"},"q":{"b":1},"r":{"c":true}}`, true, `{"p":{"a":"s"},"q":{"b":1},"r":{"c":true}}`},
			{`{"p":{"b":1}}`, false, ""},
			{`{"q":{"c":true}}`, false, ""},
			{`{"r":{"a":"s"}}`, false, ""},
		})
}

// Two keys that fold and describe the same schema are left as one type. There is
// nothing to separate: a reference to either reaches a type that says what its own
// schema says, which is the whole of what a name split buys. Splitting anyway
// would emit a second declaration of one type and report a defect that is not
// there -- and this is the common case, a document that spells one definition two
// ways.
func TestFoldedDefsKeysThatDescribeOneSchemaStillShareOneType(t *testing.T) {
	dir, paths := writeSchemas(t, "s.json", `{
		"title": "S",
		"$defs": {"foo-bar": {"type": "string", "minLength": 2}, "foo_bar": {"minLength": 2, "type": "string"}},
		"type": "object",
		"properties": {"a": {"$ref": "#/$defs/foo-bar"}, "b": {"$ref": "#/$defs/foo_bar"}}
	}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: these two keys hold one definition", stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "FooBar,S" {
		t.Errorf("declared types = %s, want FooBar,S", got)
	}
}

// Three keys folding onto one name where two of them hold the same definition.
// Agreement is judged between the folded claims too, not only before the fold is
// reached: the two that agree are one type and take one name, and only the third
// is numbered. adversarial/naming/defs-collision.json is this shape.
func TestFoldedDefsKeysThatAgreeTakeOneNameAmongThemselves(t *testing.T) {
	dir, paths := writeSchemas(t, "m.json", `{
		"title": "M",
		"$defs": {
			"MyType": {"type": "object", "properties": {"c": {"type": "boolean"}}, "required": ["c"]},
			"my-type": {"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]},
			"my_type": {"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]}
		},
		"type": "object",
		"properties": {
			"x": {"$ref": "#/$defs/my-type"},
			"y": {"$ref": "#/$defs/my_type"},
			"z": {"$ref": "#/$defs/MyType"}
		}
	}`)

	out := filepath.Join(dir, "gen")
	if _, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen")...); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "M,MyType,MyType2" {
		t.Errorf("declared types = %s, want M,MyType,MyType2: the two identical keys are one type", got)
	}

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "M",
		[]docInstance{
			{`{"x":{"a":"s"},"y":{"a":"s"},"z":{"c":true}}`, true, `{"x":{"a":"s"},"y":{"a":"s"},"z":{"c":true}}`},
			{`{"x":{"c":true}}`, false, ""},
			{`{"z":{"a":"s"}}`, false, ""},
		})
}

// A document's root type is never numbered. Two documents of one package claiming
// one root name is a collision of a different kind -- there is no name to rewrite,
// since the root type name is what the caller asked for -- and it is refused with
// the advice that fits it. Reporting a fold here would name a type that does not
// exist and a remedy that is not the one.
func TestTwoDocumentsClaimingOneRootNameAreNotNumbered(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", `{"title": "Thing", "type": "object", "properties": {"k": {"type": "string"}}}`,
		"b.json", `{"title": "Thing", "type": "object", "properties": {"n": {"type": "integer"}}}`)

	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...),
		"-o", filepath.Join(dir, "gen"), "-p", "gen", "--shared-types")...)
	if err == nil {
		t.Fatal("two documents claiming one root name must be refused")
	}
	if !strings.Contains(err.Error(), "give each schema a distinct root name") {
		t.Errorf("unexpected refusal: %v", err)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("stderr = %q, want no warning: neither root moved", stderr)
	}
}

// Which key keeps the unnumbered name is read off the keys, so writing the same
// document's $defs in the other order generates the same package. A rule that
// took the first key it happened to see would swap the two types here and rename
// half the API on a whitespace-level edit.
func TestFoldedNamesDoNotFollowTheOrderTheKeysAreWritten(t *testing.T) {
	dir, paths := writeSchemas(t,
		"one.json", `{
			"title": "One",
			"$defs": {"X": {"type": "string"}, "!": {"type": "integer"}},
			"type": "object",
			"properties": {"a": {"$ref": "#/$defs/X"}, "b": {"$ref": "#/$defs/!"}}
		}`,
		"two.json", `{
			"title": "One",
			"$defs": {"!": {"type": "integer"}, "X": {"type": "string"}},
			"type": "object",
			"properties": {"a": {"$ref": "#/$defs/X"}, "b": {"$ref": "#/$defs/!"}}
		}`)

	var got [2]string
	for i, path := range paths {
		out := filepath.Join(dir, "gen", strings.TrimSuffix(filepath.Base(path), ".json"))
		if _, err := runGenerateCapturing(t, path, "-o", out, "-p", "gen"); err != nil {
			t.Fatalf("generate %s: %v", path, err)
		}
		got[i] = strings.Join(declaredTypeNames(t, out), ",")
	}
	if got[0] != got[1] {
		t.Errorf("declared types = %s and %s, want the same", got[0], got[1])
	}
	if got[0] != "One,X,X2" {
		t.Errorf("declared types = %s, want One,X,X2 -- the key spelled as the name it derives keeps it", got[0])
	}
}

// The same, across the documents of a --shared-types run: the numbering has to be
// a property of the documents and not of the order they were listed in, which is
// the trap issue #228 came out of.
func TestFoldedNamesDoNotFollowTheOrderTheInputsAreListed(t *testing.T) {
	dir, paths := writeSchemas(t,
		"alpha.json", `{
			"title": "Alpha",
			"$defs": {"X": {"type": "string"}, "!": {"type": "integer"}},
			"type": "object",
			"properties": {"a": {"$ref": "#/$defs/X"}, "b": {"$ref": "#/$defs/!"}}
		}`,
		"beta.json", `{
			"title": "Beta",
			"$defs": {"X": {"type": "boolean"}},
			"type": "object",
			"properties": {"c": {"$ref": "#/$defs/X"}}
		}`)

	var got [2]string
	for i, order := range [][]string{{paths[0], paths[1]}, {paths[1], paths[0]}} {
		out := filepath.Join(dir, "gen", strings.Repeat("x", i+1))
		args := append(append([]string{}, order...), "-o", out, "-p", "gen", "--shared-types")
		if _, err := runGenerateCapturing(t, args...); err != nil {
			t.Fatalf("generate %v: %v", order, err)
		}
		got[i] = strings.Join(declaredTypeNames(t, out), ",")
	}
	if got[0] != got[1] {
		t.Errorf("declared types = %s and %s, want the same", got[0], got[1])
	}
}

// ---------- a definition folding onto the root type name ----------

// Issue #268. #263 answered the shape whose $defs key *is* the root type name;
// this is the same collision reached by capitalisation, where the key is "thing"
// and only becomes Thing after the derivation. The definitions are generated
// first, so the definition claimed the name and the root type the caller asked
// for was never declared at all: the package held one Thing, the definition's,
// and {"t":"not-an-object"} was accepted by a document that requires t to be that
// object.
func TestDefinitionFoldingOntoTheRootTypeNameKeepsBoth(t *testing.T) {
	_, paths := writeSchemas(t, "thing.json", `{
		"title": "Thing",
		"$defs": {"thing": {"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}},
		"type": "object",
		"properties": {"t": {"$ref": "#/$defs/thing"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "Thing",
		[]docInstance{
			{`{"t":{"x":"s"}}`, true, `{"t":{"x":"s"}}`},
			{`{"t":"not-an-object"}`, false, ""},
			{`{"t":{}}`, false, ""},
		})

	if names := generatedTypeNames(t, paths); names != "DefsThing,Thing" {
		t.Errorf("declared types = %s, want DefsThing,Thing", names)
	}
}

// ---------- a definition and a position inside the same document ----------

// The name a document's own root gives a property is derived the same way, so a
// $defs key can fold onto it: "root_t" and the position Root.t both derive RootT.
// Nothing outside the generator can see this collision -- the position's name is
// written in no document, so no caller can pin it apart -- and the property was
// silently given the definition's type.
func TestDefinitionAndInlinePositionFoldingOntoOneNameGetATypeEach(t *testing.T) {
	_, paths := writeSchemas(t, "c.json", `{
		"title": "Root",
		"$defs": {"root_t": {"type": "object", "properties": {"k": {"type": "string"}}, "required": ["k"]}},
		"type": "object",
		"properties": {
			"t": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]},
			"d": {"$ref": "#/$defs/root_t"}
		}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "Root",
		[]docInstance{
			{`{"t":{"n":1},"d":{"k":"s"}}`, true, `{"d":{"k":"s"},"t":{"n":1}}`},
			// t's own schema, which the definition's type does not describe.
			{`{"t":{"k":"s"}}`, false, ""},
			{`{"d":{"n":1}}`, false, ""},
		})
}

// A numbered name must not land on a definition the document already declares.
// Here "X2" is a key in its own right, so the fold between "X" and "!" has to
// step over it -- otherwise the name minted to separate two definitions merges a
// third, which is the same defect one name along.
func TestANumberedNameStepsOverADefinitionThatHoldsIt(t *testing.T) {
	dir, paths := writeSchemas(t, "n2.json", `{
		"title": "N",
		"$defs": {
			"X": {"type": "string"},
			"!": {"type": "integer"},
			"X2": {"type": "boolean"}
		},
		"type": "object",
		"properties": {
			"a": {"$ref": "#/$defs/X"}, "b": {"$ref": "#/$defs/!"}, "c": {"$ref": "#/$defs/X2"}
		}
	}`)

	out := filepath.Join(dir, "gen")
	if _, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen")...); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "N,X,X2,X3" {
		t.Errorf("declared types = %s, want N,X,X2,X3", got)
	}

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "N",
		[]docInstance{
			{`{"a":"s","b":1,"c":true}`, true, `{"a":"s","b":1,"c":true}`},
			{`{"b":true}`, false, ""},
			{`{"c":1}`, false, ""},
		})
}

// A position whose type one of the wrapper arms mints -- here a "type" union,
// which builds its def and marks it generated without going through
// generateTypeDef -- holds its name as much as any other. The flat a_b derives
// the name the nested a.b union took, and used to be given it: an object
// requiring "z" was decoded and validated as a string-or-integer.
func TestAPositionHeldByAWrapperArmIsNotTakenOver(t *testing.T) {
	_, paths := writeSchemas(t, "w.json", `{
		"title": "Root",
		"type": "object",
		"properties": {
			"a": {"type": "object", "properties": {"b": {"type": ["string", "integer"]}}},
			"a_b": {"type": "object", "properties": {"z": {"type": "string"}}, "required": ["z"]}
		}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "Root",
		[]docInstance{
			{`{"a":{"b":"s"},"a_b":{"z":"s"}}`, true, `{"a":{"b":"s"},"a_b":{"z":"s"}}`},
			{`{"a":{"b":1}}`, true, `{"a":{"b":1}}`},
			// a_b is an object with a required property, not the union.
			{`{"a_b":"s"}`, false, ""},
			{`{"a_b":{}}`, false, ""},
		})
}

// Two positions of one document, which is the half of the fold that has no $defs
// key in it at all: the nested a.b and the flat a_b both derive RootAB. Only the
// generator can answer this one, and it answers it the same way. Issue #271,
// adversarial/name2/nested-obj-name-collision.json.
func TestTwoInlinePositionsFoldingOntoOneNameGetATypeEach(t *testing.T) {
	_, paths := writeSchemas(t, "n.json", `{
		"title": "Root",
		"type": "object",
		"properties": {
			"a": {"type": "object", "properties": {
				"b": {"type": "object", "properties": {"c": {"type": "string"}}, "required": ["c"]}
			}},
			"a_b": {"type": "object", "properties": {"c": {"type": "integer"}}, "required": ["c"]}
		}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "Root",
		[]docInstance{
			{`{"a":{"b":{"c":"s"}},"a_b":{"c":1}}`, true, `{"a":{"b":{"c":"s"}},"a_b":{"c":1}}`},
			{`{"a_b":{"c":"s"}}`, false, ""},
			{`{"a":{"b":{"c":1}}}`, false, ""},
		})
}
