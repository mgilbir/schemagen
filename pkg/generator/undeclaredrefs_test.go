package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func mustParse(t *testing.T, doc string) *schema.Schema {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	return &s
}

// The IR looks like a tree and is not one. A conditional the static checks
// cannot spell is compiled into a RuntimeBranchCheck, which keeps the schema
// node it came from, and a schema node keeps DocumentRoot -- a back-pointer to
// the document that contains it. So the walk that decides which degraded refs
// left an undeclared name behind is walking a graph with a loop in it, and
// without the visited set it does not walk it, it falls off the stack.
//
// This fixture is the smallest thing that has both halves: the conditional that
// closes the loop, and the array element that makes the walk run at all.
func TestUndeclaredRefTypesSurvivesTheBackPointersInTheIR(t *testing.T) {
	s := mustParse(t, `{
		"title": "Doc", "type": "object",
		"properties": {
			"kind": {"type": "string"},
			"xs": {"type": "array", "items": {"$ref": "gone.json"}}
		},
		"if": {"properties": {"kind": {"const": "tool"}}, "required": ["kind"]},
		"then": {"properties": {"tool": {"type": "array", "items": {"type": "object"}}}, "required": ["tool"]}
	}`)

	f, err := New(Config{PackageName: "p", LenientRefs: true}).Generate(s)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The fixture is only worth what it exercises: if the conditional ever
	// stops being compiled to a runtime check there is no back-pointer left and
	// this test is guarding nothing.
	var runtimeChecks int
	for _, td := range f.TypeDefs {
		if sd, ok := td.(*StructDef); ok {
			runtimeChecks += len(sd.RuntimeBranchChecks)
		}
	}
	if runtimeChecks == 0 {
		t.Fatal("fixture no longer produces a runtime branch check, so it no longer holds a schema node -- pick a shape that does")
	}

	if len(f.UndeclaredRefTypes) != 1 ||
		f.UndeclaredRefTypes[0].Ref != "gone.json" ||
		f.UndeclaredRefTypes[0].TypeName != "GoneJSON" {
		t.Fatalf("UndeclaredRefTypes = %+v, want the array element's gone.json -> GoneJSON", f.UndeclaredRefTypes)
	}
}

// Without LenientRefs an unresolvable ref is an error and there is no file, so
// nothing here should ever be populated. Stated as a test because the cost of
// the walk and the risk of a wrong answer are both confined by that, and the
// call site is a single line that a later edit could move.
func TestUndeclaredRefTypesIsEmptyWithoutLenientRefs(t *testing.T) {
	s := mustParse(t, `{
		"title": "Doc", "type": "object",
		"properties": {"xs": {"type": "array", "items": {"$ref": "gone.json"}}}
	}`)

	if _, err := New(Config{PackageName: "p"}).Generate(s); err == nil {
		t.Fatal("without --lenient-refs an unresolvable $ref must fail generation")
	}

	// And a run where everything resolves reports nothing either way.
	ok := mustParse(t, `{
		"title": "Doc", "type": "object",
		"properties": {"xs": {"type": "array", "items": {"$ref": "#/$defs/D"}}},
		"$defs": {"D": {"type": "object", "properties": {"n": {"type": "string"}}}}
	}`)
	for _, lenient := range []bool{false, true} {
		f, err := New(Config{PackageName: "p", LenientRefs: lenient}).Generate(ok)
		if err != nil {
			t.Fatalf("Generate (lenient=%v): %v", lenient, err)
		}
		if len(f.UnresolvedRefs) != 0 || len(f.UndeclaredRefTypes) != 0 {
			t.Errorf("lenient=%v: nothing failed to resolve, got UnresolvedRefs=%v UndeclaredRefTypes=%+v",
				lenient, f.UnresolvedRefs, f.UndeclaredRefTypes)
		}
	}
}

// A NamedType is the common way a reference reaches the emitted source, and the
// `...TypeName` strings are the other. Both are asserted directly here, because
// end-to-end the two are indistinguishable -- a walk that found only the first
// would still answer correctly for most shapes.
func TestReferencedTypeNamesReadsBothSpellingsOfAReference(t *testing.T) {
	f := &File{
		PackageName: "p",
		TypeDefs: []TypeDef{
			&StructDef{
				Name: "Root",
				Fields: []FieldDef{
					{Name: "A", Type: &ArrayType{ItemType: &NamedType{Name: "ThroughNamedType"}}},
					{Name: "B", Type: &NamedType{Name: "Foreign", PkgAlias: "other"}},
				},
				ItemValidations: []ItemValidationDef{
					{FieldName: "A", Levels: []ItemLevel{{ElemTypeName: "ThroughTypeNameString"}}},
				},
			},
		},
	}

	got := referencedTypeNames(f)
	for _, want := range []string{"ThroughNamedType", "ThroughTypeNameString"} {
		if !got[want] {
			t.Errorf("%q is spelled by the IR and was not collected: %v", want, got)
		}
	}
	if got["Foreign"] {
		t.Errorf("a type qualified by a package alias is declared in that package, not this one: %v", got)
	}
	if got["Root"] {
		t.Errorf("a declaration's own name is not a reference to one: %v", got)
	}
}
