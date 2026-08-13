package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// A type minted for a position inside a document is named after the position --
// the parent's type name and the field's -- and two positions of one document can
// derive one name. The re-entrancy guard in generateTypeDef then turned the
// second away and its caller, which cannot hear that, gave the position the first
// one's type: a struct field validated by a schema written somewhere else. Issue
// #271, in the half no caller can pin apart, since these names appear in no
// document. See unclaimedTypeName.
//
// These tests read the IR. The compiled-and-run proof is in
// cmd/schemagen/foldednames_test.go.

// generateDoc generates one document through a plain generator and returns the
// file.
func generateDoc(t *testing.T, doc string) *File {
	t.Helper()
	s := new(schema.Schema)
	if err := json.Unmarshal([]byte(doc), s); err != nil {
		t.Fatalf("document: %v", err)
	}
	s.ComputeBaseURIs(nil, s)
	g := New(Config{Validation: ValidationModeStatic, PackageName: "gen"})
	file, err := g.Generate(s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return file
}

// fieldTypeName returns the Go type the named struct gives the named property,
// with any pointer stripped.
func fieldTypeName(t *testing.T, f *File, typeName, jsonName string) string {
	t.Helper()
	for _, td := range f.TypeDefs {
		sd, ok := td.(*StructDef)
		if !ok || sd.Name != typeName {
			continue
		}
		for _, field := range sd.Fields {
			if field.JSONName != jsonName {
				continue
			}
			gt := field.Type
			if p, isPtr := gt.(*PointerType); isPtr {
				gt = p.Inner
			}
			if nt, isNamed := gt.(*NamedType); isNamed {
				return nt.Name
			}
			return field.Type.GoTypeName()
		}
		t.Fatalf("struct %s has no property %q", typeName, jsonName)
	}
	t.Fatalf("no struct named %s in %v", typeName, typeNamesOf(f))
	return ""
}

// The nested a.b and the flat a_b both derive RootAB. They are different schemas
// -- one holds a string at "c", the other an integer -- so they cannot be one Go
// type, and the flat one used to be given the nested one's.
func TestTwoPositionsDerivingOneNameGetATypeEach(t *testing.T) {
	f := generateDoc(t, `{
		"type": "object",
		"properties": {
			"a": {"type": "object", "properties": {
				"b": {"type": "object", "properties": {"c": {"type": "string"}}}
			}},
			"a_b": {"type": "object", "properties": {"c": {"type": "integer"}}}
		}
	}`)

	nested := fieldTypeName(t, f, "RootA", "b")
	flat := fieldTypeName(t, f, "Root", "a_b")
	if nested == flat {
		t.Fatalf("a.b and a_b share the type %s; each position needs its own", nested)
	}
	if got := fieldTypeName(t, f, nested, "c"); got != "string" {
		t.Errorf("a.b.c is %s, want string", got)
	}
	if got := fieldTypeName(t, f, flat, "c"); got != "int64" {
		t.Errorf("a_b.c is %s, want int64", got)
	}
}

// A $defs key deriving the document's own root type name. The definitions are
// generated first, so it claimed the name and the root type was never declared:
// the package held one Thing, the definition's struct, and the root's property t
// was gone -- {"t":"not-an-object"} decoded and validated clean against a schema
// that requires t to be that object. Issue #268. The CLI resolves this before
// generation and gives the definition a name that says which keyword declared it;
// this is what answers for a caller that resolves nothing.
func TestADefinitionDoesNotTakeTheRootTypeName(t *testing.T) {
	f := generateDoc(t, `{
		"title": "Thing",
		"$defs": {"thing": {"type": "object", "properties": {"x": {"type": "string"}}}},
		"type": "object",
		"properties": {"t": {"$ref": "#/$defs/thing"}}
	}`)

	def := fieldTypeName(t, f, "Thing", "t")
	if def == "Thing" {
		t.Fatal("the root type and its definition are one type; the root's own properties are gone")
	}
	if got := fieldTypeName(t, f, def, "x"); got != "string" {
		t.Errorf("%s.x is %s, want string: the definition keeps its own schema", def, got)
	}
}

// A position that asks for a name and mints nothing does not hold it. Here a.b is
// a string, so the ladder answers with the Go type and declares nothing under
// RootAB -- and the flat a_b, which derives the same name, must have it. Numbering
// around a name that stands on nothing would rename a type for a collision that
// is not there.
func TestAPositionThatDeclaresNothingDoesNotHoldTheName(t *testing.T) {
	f := generateDoc(t, `{
		"type": "object",
		"properties": {
			"a": {"type": "object", "properties": {"b": {"type": "string"}}},
			"a_b": {"type": "object", "properties": {"c": {"type": "integer"}}}
		}
	}`)

	if got := fieldTypeName(t, f, "Root", "a_b"); got != "RootAB" {
		t.Errorf("a_b is %s, want RootAB: nothing else declares that name", got)
	}
}

// Two documents generated through one generator that does not share a package
// are two packages, each written to its own file. The second starts from an empty
// name space -- g.generated is emptied for it -- so a name the first document
// declared is free again, and a position deriving it takes it rather than
// stepping around a type that is not in the file being written.
func TestASecondUnsharedDocumentStartsFromAnEmptyNameSpace(t *testing.T) {
	first := new(schema.Schema)
	if err := json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {"a": {"type": "object", "properties": {
			"b": {"type": "object", "properties": {"c": {"type": "string"}}}
		}}}
	}`), first); err != nil {
		t.Fatal(err)
	}
	second := new(schema.Schema)
	if err := json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {"a_b": {"type": "object", "properties": {"c": {"type": "integer"}}}}
	}`), second); err != nil {
		t.Fatal(err)
	}
	first.ComputeBaseURIs(nil, first)
	second.ComputeBaseURIs(nil, second)

	g := New(Config{Validation: ValidationModeStatic, PackageName: "gen"})
	if _, err := g.Generate(first); err != nil {
		t.Fatalf("first document: %v", err)
	}
	f, err := g.Generate(second)
	if err != nil {
		t.Fatalf("second document: %v", err)
	}
	if got := fieldTypeName(t, f, "Root", "a_b"); got != "RootAB" {
		t.Errorf("a_b is %s, want RootAB: the first document's names are not in this file", got)
	}
}

// The same node reached twice keeps one name. A $ref target used from two
// positions arrives here under the name it already holds, and numbering it again
// would declare the same type a second time -- which is the defect the guard this
// sits in front of exists to prevent.
func TestOnePositionReachedTwiceKeepsOneType(t *testing.T) {
	f := generateDoc(t, `{
		"type": "object",
		"$defs": {"T": {"type": "object", "properties": {"k": {"type": "string"}}}},
		"properties": {"a": {"$ref": "#/$defs/T"}, "b": {"$ref": "#/$defs/T"}}
	}`)

	if a, b := fieldTypeName(t, f, "Root", "a"), fieldTypeName(t, f, "Root", "b"); a != b || a != "T" {
		t.Errorf("a is %s and b is %s, want both T: one definition is one type", a, b)
	}
	names := typeNamesOf(f)
	seen := 0
	for _, n := range names {
		if n == "T" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("T declared %d times in %v, want once", seen, names)
	}
}
