package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// In shared-types mode one generator runs several schemas into one Go package,
// and g.output is replaced on every call. Everything a later schema asks about a
// type an earlier one materialized -- does it carry a Validate, is it an enum,
// what is its zero -- therefore answered "no such type", and the file that
// referenced it silently dropped the check (issue #218). typeDefsInScope is what
// keeps the earlier declarations answerable; these tests read the IR, so they
// reach the shapes whose generated code would need an external module to
// compile. The compiled-and-run proof is in cmd/schemagen/crossdocument_test.go.

// sharedTypesRun generates each schema through one shared-types generator and
// returns the last call's file.
func sharedTypesRun(t *testing.T, docs ...string) *File {
	t.Helper()
	// Every document is loaded once and indexed by $id, so a cross-document
	// $ref comes back as the instance being generated rather than a second
	// copy. That is what the CLI does, and the generated-type registry keys on
	// node identity.
	loaded := make([]*schema.Schema, 0, len(docs))
	byID := make(map[string]*schema.Schema, len(docs))
	for i, doc := range docs {
		s := new(schema.Schema)
		if err := json.Unmarshal([]byte(doc), s); err != nil {
			t.Fatalf("document %d: %v", i, err)
		}
		s.ComputeBaseURIs(nil, s)
		loaded = append(loaded, s)
		byID[s.ID] = s
	}

	g := New(Config{
		Validation:  ValidationModeStatic,
		SharedTypes: true,
		PackageName: "gen",
		Resolver:    schema.NewMappingResolver(byID),
	})
	var last *File
	for i, s := range loaded {
		file, err := g.Generate(s)
		if err != nil {
			t.Fatalf("document %d: %v", i, err)
		}
		last = file
	}
	return last
}

func typeNamesOf(f *File) []string {
	names := make([]string, 0, len(f.TypeDefs))
	for _, td := range f.TypeDefs {
		names = append(names, td.TypeName())
	}
	return names
}

// A patternProperties bucket whose sub-schema is a $ref into another document
// keeps the type that carries the checks. resolvePatternPropertyTypes withdraws
// a bucket type it cannot see a Validate on, and it could not see one declared
// by an earlier schema of the package -- so the bucket was silently reduced to
// "any key matching this pattern is fine".
func TestSharedTypesKeepsAPatternBucketTypedByAnotherDocument(t *testing.T) {
	f := sharedTypesRun(t,
		`{"$id":"https://ex.test/c.json","title":"CDoc","type":"object",
		  "properties":{"v":{"type":"integer","minimum":5}},"required":["v"]}`,
		`{"$id":"https://ex.test/m.json","title":"MDoc","type":"object",
		  "patternProperties":{"^p_":{"$ref":"https://ex.test/c.json"}}}`)

	sd := structNamed(t, f, "MDoc")
	if len(sd.PatternProperties) != 1 {
		t.Fatalf("MDoc should carry one patternProperties bucket, got %d", len(sd.PatternProperties))
	}
	if got := sd.PatternProperties[0].TypeName; got != "CDoc" {
		t.Errorf("the ^p_ bucket should validate through CDoc, got TypeName %q "+
			"(empty means the bucket accepts any matching key unchecked)", got)
	}
}

// The same question for a property: a field whose type another document declared
// must be listed as validatable, so the struct's Validate dispatches to it.
func TestSharedTypesMarksAFieldTypedByAnotherDocumentValidatable(t *testing.T) {
	f := sharedTypesRun(t,
		`{"$id":"https://ex.test/c.json","title":"CDoc","type":"object",
		  "properties":{"v":{"type":"integer","minimum":5}},"required":["v"]}`,
		`{"$id":"https://ex.test/m.json","title":"MDoc","type":"object",
		  "properties":{"c":{"$ref":"https://ex.test/c.json"}},"required":["c"]}`)

	sd := structNamed(t, f, "MDoc")
	if len(sd.ValidatableFields) != 1 || sd.ValidatableFields[0].FieldName != "C" {
		t.Fatalf("MDoc.C should be dispatched to, got ValidatableFields %+v", sd.ValidatableFields)
	}
}

// And a type an earlier schema declared must not be re-emitted into the later
// schema's file: that is the duplicate declaration of issue #217, which
// shared-types exists to avoid.
func TestSharedTypesDoesNotReEmitAnEarlierDocumentsType(t *testing.T) {
	f := sharedTypesRun(t,
		`{"$id":"https://ex.test/c.json","title":"CDoc","type":"object",
		  "properties":{"v":{"type":"integer","minimum":5}},"required":["v"]}`,
		`{"$id":"https://ex.test/m.json","title":"MDoc","type":"object",
		  "properties":{"c":{"$ref":"https://ex.test/c.json"}},"required":["c"]}`)

	for _, name := range typeNamesOf(f) {
		if name == "CDoc" {
			t.Errorf("CDoc belongs to the first schema's file; re-emitting it here declares it twice in package gen")
		}
	}
}
