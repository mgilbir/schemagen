package schemagen

import (
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about one document contesting a Go type name with
// itself: "$defs/X" and "definitions/X" are two JSON Pointers naming two schema
// locations, and a document may write different schemas at them. Both nodes
// reached the generator, which named each after its key alone and let the first
// claim X, so a property carried a type its own document never described --
// issue #249's harm, inside one document, where #249's document-name qualifier
// has nothing to tell the two apart. Issue #260.

// ---------- issue #260: $defs/X and definitions/X ----------

const twoKeywordsDoc = `{
	"title": "D", "type": "object",
	"properties": {"x": {"$ref": "#/$defs/X"}, "y": {"$ref": "#/definitions/X"}},
	"$defs": {"X": {"type": "string"}},
	"definitions": {"X": {"type": "object",
		"properties": {"k": {"type": "integer"}}, "required": ["k"]}}
}`

// The reproducer. "#/$defs/X" and "#/definitions/X" are two JSON Pointers naming
// two schema locations, and Schema.normalizeNode mirrors the keywords only when
// one of them is empty -- so both nodes reach the generator, which named each
// after its key alone and let the first claim X. The document's own "y" then
// carried a string, and {"y":{"k":1}} -- which its schema accepts -- would not
// decode at all.
//
// Both properties are exercised, because the defect generated cleanly: it is
// visible only in what the types accept.
func TestOneDocumentSpellingADefinitionTwoWaysKeepsBoth(t *testing.T) {
	_, paths := writeSchemas(t, "d.json", twoKeywordsDoc)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "D",
		[]docInstance{
			{`{"x":"s","y":{"k":1}}`, true, `{"x":"s","y":{"k":1}}`},
			// Each position must refuse the other's shape.
			{`{"x":{"k":1}}`, false, ""},
			{`{"y":"s"}`, false, ""},
			// definitions/X requires "k"; $defs/X says nothing of the sort.
			{`{"y":{}}`, false, ""},
		})

	if names := generatedTypeNames(t, paths); names != "D,DefinitionsX,DefsX" {
		t.Errorf("declared types = %s, want D,DefinitionsX,DefsX", names)
	}
}

// The same document under --shared-types. The name a definition gets must not
// depend on the mode: a caller who adds the flag to a working single-document
// run has not asked for every type to be renamed.
func TestOneDocumentSpellingADefinitionTwoWaysIsTheSameUnderSharedTypes(t *testing.T) {
	dir, paths := writeSchemas(t, "d.json", twoKeywordsDoc)
	out := filepath.Join(dir, "gen")
	if _, err := runGenerateCapturing(t, append(append([]string{}, paths...),
		"-o", out, "-p", "gen", "--shared-types")...); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "D,DefinitionsX,DefsX" {
		t.Errorf("declared types = %s, want D,DefinitionsX,DefsX", got)
	}
}

// The common case, and the one a fix must not touch: a document that supports
// two drafts writes its definitions under both spellings and means the same
// thing by them. One type, no rename, and -- since a warning about a document
// that is doing nothing wrong is its own defect -- nothing on stderr.
func TestOneDocumentSpellingADefinitionTwoWaysIdenticallyIsUntouched(t *testing.T) {
	dir, paths := writeSchemas(t, "d.json", `{
		"title": "D", "type": "object",
		"properties": {"x": {"$ref": "#/$defs/X"}, "y": {"$ref": "#/definitions/X"}},
		"$defs": {"X": {"type": "string", "minLength": 2}},
		"definitions": {"X": {"minLength": 2, "type": "string"}}
	}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen")...)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: the two spellings describe one definition", stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "D,X" {
		t.Errorf("declared types = %s, want D,X", got)
	}
}

// Agreement is judged on the normalized documents, exactly as it is across
// documents: key order, whitespace and a keyword the dialect does not define are
// not a difference. Draft-07 defines neither "$defs" nor "unevaluatedProperties",
// so the two below say the same thing.
func TestTwoSpellingsThatDifferOnlyOutsideTheDialectStillShare(t *testing.T) {
	dir, paths := writeSchemas(t, "d.json", `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title": "D", "type": "object",
		"properties": {"y": {"$ref": "#/definitions/X"}},
		"$defs": {"X": {"type": "string", "unevaluatedProperties": false}},
		"definitions": {"X": {"type": "string"}}
	}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen")...)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "D,X" {
		t.Errorf("declared types = %s, want D,X", got)
	}
}

// The diagnostic, verbatim. A caller whose two spellings were about to be one
// type has to be told they are not, and told it in terms of the document rather
// than of an input set they do not have.
func TestTwoSpellingsReportWhatWasSplit(t *testing.T) {
	dir, paths := writeSchemas(t, "d.json", twoKeywordsDoc)

	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", filepath.Join(dir, "gen"), "-p", "gen")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	want := "warning: " + paths[0] + " declares the Go type name X in 2 places, and those declarations do not describe the same type, so they cannot be one:\n" +
		"  " + paths[0] + " $defs/X becomes DefsX\n" +
		"  " + paths[0] + " definitions/X becomes DefinitionsX\n" +
		"one Go package holds one type per name, so declaring them all as X would have given every $ref whichever was generated first and discarded the rest -- a position typed by a schema the document never wrote there. " +
		"Each definition is qualified instead with the keyword that declared it, which is the only thing in the document that tells them apart. " +
		"$defs and definitions name the same container in every draft that defines both, so if these were meant to be one definition make them identical or delete one; otherwise rename one of them in the schema to choose the Go names yourself.\n"
	if stderr != want {
		t.Errorf("stderr =\n%q\nwant\n%q", stderr, want)
	}
}

// A definition whose key derives the document's own root type name. It is the
// same collision with the same discriminator -- the keyword that declared it --
// and it was the more damaging one: the definitions are generated first, so the
// definition claimed the name and the *root type the caller asked for was never
// declared at all*. A document titled "X" produced a package with no X struct in
// it, and said nothing.
func TestDefinitionNamedAfterItsOwnRootTypeKeepsBoth(t *testing.T) {
	_, paths := writeSchemas(t, "x.json", `{
		"title": "X", "type": "object",
		"properties": {"p": {"$ref": "#/$defs/X"}},
		"$defs": {"X": {"type": "string"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "X",
		[]docInstance{
			{`{"p":"s"}`, true, `{"p":"s"}`},
			{`{"p":1}`, false, ""},
		})

	if names := generatedTypeNames(t, paths); names != "DefsX,X" {
		t.Errorf("declared types = %s, want DefsX,X", names)
	}
}

// The remedy in that message is a different one, and naming a remedy that does
// not apply is worse than naming none: --root-name moves a root type, and does
// nothing at all to a definition spelled twice.
func TestDefinitionNamedAfterItsOwnRootTypeSaysWhatToChange(t *testing.T) {
	dir, paths := writeSchemas(t, "x.json", `{
		"title": "X", "type": "object",
		"properties": {"p": {"$ref": "#/$defs/X"}},
		"$defs": {"X": {"type": "string"}}
	}`)

	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", filepath.Join(dir, "gen"), "-p", "gen")...)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := "warning: " + paths[0] + " declares the Go type name X in 2 places, and those declarations do not describe the same type, so they cannot be one:\n" +
		"  " + paths[0] + " $defs/X becomes DefsX\n" +
		"  " + paths[0] + " root type keeps X\n" +
		"one Go package holds one type per name, so declaring them all as X would have given every $ref whichever was generated first and discarded the rest -- a position typed by a schema the document never wrote there. " +
		"Each definition is qualified instead with the keyword that declared it, which is the only thing in the document that tells them apart. " +
		"The document's root type keeps the name -- it is the one the caller asked for, by the document's title or by --root-name. " +
		"Rename the definition in the schema, or give the document another root name with --root-name, to choose the Go names yourself.\n"
	if stderr != want {
		t.Errorf("stderr =\n%q\nwant\n%q", stderr, want)
	}
}

// The boundary, recorded so it is not mistaken for something this change
// answers. Two $defs keys of one document that fold onto one Go name are
// contested by neither qualifier -- same document, same keyword -- so they still
// merge, exactly as before. That collision needs a discriminator the document
// does not contain, which is a different question with a different answer; what
// matters here is that nothing invents one, and in particular that two nodes are
// never pinned to one name, which the generator would refuse outright.
func TestTwoDefsKeysFoldingOntoOneNameAreUnchanged(t *testing.T) {
	dir, paths := writeSchemas(t, "f.json", `{
		"title": "F", "type": "object",
		"properties": {"a": {"$ref": "#/$defs/foo-bar"}, "b": {"$ref": "#/$defs/foo_bar"}},
		"$defs": {"foo-bar": {"type": "string"}, "foo_bar": {"type": "integer"}}
	}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen")...)
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: this collision is not one this change answers", stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); got != "F,FooBar" {
		t.Errorf("declared types = %s, want F,FooBar", got)
	}
}
