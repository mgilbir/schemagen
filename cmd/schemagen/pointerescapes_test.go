package schemagen

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about one rule: a $ref's fragment is a URI
// component, so a JSON Pointer inside it is percent-decoded first (RFC 3986
// §3.5) and only then read as a pointer (RFC 6901 §6). Every part of schemagen
// that asks "which member of the document does this pointer name?" has to apply
// the two decodings in that order, because the orders are not interchangeable:
// "%7E1" percent-decodes to "~1" and then unescapes to "/", while unescaping
// first finds no literal "~1" and leaves a token naming "~1". Two different
// members of the same document.
//
// pkg/schema.UnescapePointerToken is now the single implementation, and these
// are the three places a disagreement with it was observable. Issue #305.

// ---------- the demotion (#305) ----------

// percentEscapedRefDocs is a pair of documents that agree about everything.
// Their shared definition reaches the other definition through a percent-escape
// -- "#/$defs/foo%22bar" names the $defs key foo"bar -- and the target is spelled
// the same way in both.
func percentEscapedRefDocs(title string, target string) string {
	return fmt.Sprintf(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title": %q,
		"type": "object",
		"properties": {"t": {"$ref": "#/$defs/Thing"}},
		"$defs": {
			"Thing": {"type": "object", "properties": {"o": {"$ref": "#/$defs/foo%%22bar"}}},
			"foo\"bar": %s
		}
	}`, title, target)
}

// The reproducer. Nothing separates these two documents, so Thing is one Go
// type -- and it was two.
//
// localDefinitionRef answers which definition a claim's $ref names, and
// shareableNames uses that answer to decide whether the claim may stay shared:
// a name is shareable only when every name it reaches is. The function did not
// percent-decode, so "#/$defs/foo%22bar" came out as the Go name Foo22bar,
// which no claim carries. The transitive lookup missed, Thing was demoted, and
// the package declared ADocThing and BDocThing for one definition. The target
// itself shared correctly all along, which is what makes the split absurd: one
// FooBar, reached by two Things that are the same Thing.
//
// The assertion is the sharing decision and not the derived string, because
// that is what the defect costs. A demotion emits Go that compiles, validates
// and round-trips exactly as the shared type would; the only trace it leaves is
// the duplicate type and the warning that says the definitions "do not describe
// the same type" when they are byte-identical.
func TestSharedTypesFollowsAPercentEscapedReference(t *testing.T) {
	const target = `{"type": "string", "minLength": 4}`
	dir, paths := writeSchemas(t,
		"a.json", percentEscapedRefDocs("ADoc", target),
		"b.json", percentEscapedRefDocs("BDoc", target))

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen", "--shared-types")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "do not describe the same type") {
		t.Errorf("identical definitions must not be split:\n%s", stderr)
	}
	got := strings.Join(declaredTypeNames(t, out), ",")
	if want := "ADoc,BDoc,FooBar,Thing"; got != want {
		t.Errorf("declared types = %s, want %s (one shared Thing)\nstderr:\n%s", got, want, stderr)
	}
}

// The control, and the reason the fix is a decoding order rather than a shortcut
// that makes every escaped ref shareable: when what the escaped pointer reaches
// *does* differ between the documents, both the target and the definition that
// reaches it must still be qualified. The transitive rule has to keep working
// through the escape, in the direction that costs a duplicate type as well as
// the direction that would silently drop a definition.
func TestSharedTypesSplitsOnWhatAPercentEscapedReferenceReaches(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", percentEscapedRefDocs("ADoc", `{"type": "string", "minLength": 4}`),
		"b.json", percentEscapedRefDocs("BDoc", `{"type": "integer", "minimum": 9}`))

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen", "--shared-types")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	got := strings.Join(declaredTypeNames(t, out), ",")
	if want := "ADoc,ADocFooBar,ADocThing,BDoc,BDocFooBar,BDocThing"; got != want {
		t.Errorf("declared types = %s, want %s\nstderr:\n%s", got, want, stderr)
	}
}

// ---------- the discriminator mapping ----------

// discriminatorEscapeDoc has two definitions whose keys are a tilde and a tilde
// followed by a zero, so they derive two distinct Go type names (X and X0) and
// nothing about this test rests on the lossy part of the identifier derivation.
//
// Each oneOf variant is written with the pointer spelling that reaches its own
// definition, and each mapping value with a *different* spelling that reaches
// the same one. That is what forces the matcher off its exact-ref arm and onto
// the derived name:
//
//	"#/$defs/~00"   -- unescapes to the key "~0"  (the X0 definition)
//	"#/$defs/%7E0"  -- decodes to "~0", unescapes to the key "~" (the X definition)
//
// Reading the escapes in the wrong order turns the second into a pointer naming
// "~0", so it derives X0's name -- and then the mapping is crossed: "a" decoded
// as X and "z" as X0, exactly the other way round from what the two pointers
// say. Both crossings are silent; the emitted switch compiles and each arm
// decodes into a real type of the package.
const discriminatorEscapeDoc = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/d.json",
	"title": "D",
	"type": "object",
	"properties": {
		"x": {
			"oneOf": [{"$ref": "#/$defs/%7E0"}, {"$ref": "#/$defs/~00"}],
			"discriminator": {
				"propertyName": "kind",
				"mapping": {"a": "#/$defs/~00", "z": "#/$defs/%7E0"}
			}
		}
	},
	"$defs": {
		"~":  {"type": "object", "properties": {"kind": {"type": "string"}, "tilde": {"type": "string"}}, "required": ["kind", "tilde"]},
		"~0": {"type": "object", "properties": {"kind": {"type": "string"}, "zero": {"type": "integer"}}, "required": ["kind", "zero"]}
	}
}`

// A discriminator mapping value is a $ref like any other, and the variant it
// selects has to be the definition that ref names.
//
// This is the arm the ref-to-name derivation is used as an *identity* rather
// than as a label: everywhere else the name it produces is only what a resolved
// node gets called, and a wrong one is caught by the node registry or numbered
// apart by the collision machinery. Here two refs are compared by the names they
// derive, so a derivation that maps one pointer onto another pointer's target
// dispatches the document into the wrong Go type -- and both definitions decode
// it far enough to make the verdict, rather than the decoder, the thing that is
// wrong.
//
// Run rather than read: the crossing shows up as a valid document refused and an
// invalid one accepted, in both directions.
func TestDiscriminatorMappingDispatchesOnWhatThePointerNames(t *testing.T) {
	_, paths := writeSchemas(t, "d.json", discriminatorEscapeDoc)

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			// "a" is mapped to "#/$defs/~00", the "~0" definition, which is the
			// one with "zero".
			{"D", `{"x":{"kind":"a","zero":3}}`, true, ""},
			// "z" is mapped to "#/$defs/%7E0", the "~" definition, which is the
			// one with "tilde".
			{"D", `{"x":{"kind":"z","tilde":"t"}}`, true, ""},
			// The other document each arm would accept if the two were crossed.
			{"D", `{"x":{"kind":"a","tilde":"t"}}`, false, ""},
			{"D", `{"x":{"kind":"z","zero":3}}`, false, ""},
		})
}

// ---------- the claim diagnostic ----------

// pointerClaimParts is the last place cmd/schemagen answers "which member does
// this pointer name?" in its own words rather than the generator's, and after
// issue #303 collapsed the ref-to-name copies it is the only one. It renders the
// claim a diagnostic names -- "$defs/Inner", "properties/x" -- from the
// reference text, and it carried its own RFC 6901 unescaping that never
// percent-decoded.
//
// This is the guard that replaces TestRefDerivedTypeNameMatchesTheGeneratorsRefNaming.
// That test watched cmd's copy of the derivation for drift from the generator's,
// and the copy is gone: cmd calls generator.TypeNameForRef, so there is no
// second implementation left to drift. What was never covered is this function,
// which is not the derivation but is the same question asked a second time -- and
// a message that names a key the document does not hold sends its reader to look
// for something that is not there.
//
// Both documents declare a property literally called "/", and the two refs reach
// it by the two spellings that mean it: "~1" and "%7E1". Every step has to read
// them as the same key. The claimed name has to be X for both, or the two never
// contest at all and no diagnostic is produced -- which is what the old
// derivation did, deriving X1 for the escaped one -- and the claim each is
// reported under has to be "properties//", the key itself, rather than the
// escape the ref reached it by.
func TestClaimDiagnosticNamesTheKeyTheDocumentHolds(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/a.json", "title": "A", "type": "object",
			"properties": {"/": {"type": "string", "minLength": 5}}}`,
		"b.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/b.json", "title": "B", "type": "object",
			"properties": {"/": {"type": "integer", "minimum": 100}}}`,
		"root.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
			"properties": {
				"pa": {"$ref": "a.json#/properties/~1"},
				"pb": {"$ref": "b.json#/properties/%7E1"}}}`)
	rootPath := paths[2]

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, rootPath, "-o", out, "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"2 documents claim the Go type name X",
		"a.json (reached by $ref) properties// becomes AX",
		"b.json (reached by $ref) properties// becomes BX",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); !strings.Contains(got, "AX") || !strings.Contains(got, "BX") {
		t.Errorf("declared types = %s, want AX and BX", got)
	}

	// And the split has to be real, not just reported: each position keeps the
	// schema its own document wrote at the key both pointers name.
	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{rootPath, "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"pa":"hello","pb":150}`, true, `{"pa":"hello","pb":150}`},
			{"Root", `{"pa":150}`, false, ""},
			{"Root", `{"pb":"hello"}`, false, ""},
		})
}
