package generator

import (
	"encoding/json"
	"go/token"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

func generateForNamesTest(t *testing.T, src string, cfg Config) (*Generator, *File) {
	t.Helper()
	var s schema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()
	if cfg.PackageName == "" {
		cfg.PackageName = "testpkg"
	}
	g := New(cfg)
	f, err := g.Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return g, f
}

func structHasProperty(f *File, structName, jsonName string) bool {
	for _, td := range f.TypeDefs {
		if sd, ok := td.(*StructDef); ok && sd.Name == structName {
			for _, fd := range sd.Fields {
				if fd.JSONName == jsonName {
					return true
				}
			}
		}
	}
	return false
}

// Two references whose last pointer token is the same, into nodes nothing
// named first. The name each derives is read off the reference text -- "1" for
// both -- and a library caller, which pins nothing, used to get one type for
// the two nodes: b was typed by a's schema. The CLI numbered them apart, by
// collecting claims before generating; the library did not. The registry
// answers both the same way now.
func TestRefsWithOneLastPointerTokenGetTypesOfTheirOwn(t *testing.T) {
	_, f := generateForNamesTest(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"a": {"$ref": "#/x-alpha/1"},
			"b": {"$ref": "#/x-beta/1"}
		},
		"x-alpha": [{}, {"type": "object", "properties": {"s": {"type": "string"}}, "required": ["s"]}],
		"x-beta": [{}, {"type": "object", "properties": {"i": {"type": "integer"}}, "required": ["i"]}]
	}`, Config{RootTypeName: "Root"})

	a, b := fieldTypeName(t, f, "Root", "a"), fieldTypeName(t, f, "Root", "b")
	if a == b {
		t.Fatalf("a and b are both typed %s: two nodes, one type", a)
	}
	if !structHasProperty(f, a, "s") || structHasProperty(f, a, "i") {
		t.Errorf("a's type %s is not x-alpha/1's", a)
	}
	if !structHasProperty(f, b, "i") || structHasProperty(f, b, "s") {
		t.Errorf("b's type %s is not x-beta/1's", b)
	}
}

// A cross-package import alias is claimed in the package's name space, so it
// steps around every name generated code already spells: the import names the
// generated files use themselves, the helpers, Go's predeclared identifiers.
// reservedImportNames reserved "flags" where the files spell "ecmaflags", and
// named neither sort nor strconv nor errors among the helper file's imports.
func TestCrossPackageImportAliasStepsAroundGeneratedNames(t *testing.T) {
	var taken []string
	for _, imp := range generatedImports {
		taken = append(taken, imp.Name)
	}
	taken = append(taken, "jsonInteger", "len", "string", "any")
	for _, name := range taken {
		g := New(Config{PackageName: "p"})
		g.crossImports = map[string]string{}
		alias := g.importAlias("example.com/m/" + name)
		if alias == name {
			t.Errorf("a sibling package named %s is imported as %s, which generated code already spells", name, alias)
		}
		if !token.IsIdentifier(alias) {
			t.Errorf("the alias %q for a package named %s is not an identifier", alias, name)
		}
	}
	// And one that collides with nothing keeps its own name.
	g := New(Config{PackageName: "p"})
	g.crossImports = map[string]string{}
	if alias := g.importAlias("example.com/m/widgets"); alias != "widgets" {
		t.Errorf("an uncontested package name was aliased %s", alias)
	}
}

// The numbering the policy states: want2, want3 ... and, for a name that ends
// in a digit, want_2, so the added number cannot run into the one there.
func TestNumberedNameSpellsTheNumberApart(t *testing.T) {
	for _, tc := range []struct {
		want string
		n    int
		got  string
	}{
		{"Foo", 2, "Foo2"},
		{"Foo", 10, "Foo10"},
		{"String2", 2, "String2_2"},
		{"X1", 3, "X1_3"},
	} {
		if got := NumberedName(tc.want, tc.n); got != tc.got {
			t.Errorf("NumberedName(%q, %d) = %q, want %q", tc.want, tc.n, got, tc.got)
		}
	}
}

// Every definition claims its name before any type is generated, in the order
// definitionClaimOrder gives -- a key spelled as its Go name first -- so which
// definition keeps a folded name does not depend on what the document happens
// to generate first, and a position never displaces one.
func TestDefinitionsClaimAheadOfPositions(t *testing.T) {
	g, f := generateForNamesTest(t, `{
		"type": "object",
		"properties": {"a": {"type": "array", "items": {"type": "object", "properties": {"z": {"type": "string"}}}}},
		"$defs": {
			"root_a_item": {"type": "object", "properties": {"u": {"type": "boolean"}}},
			"RootAItem": {"type": "integer"}
		}
	}`, Config{RootTypeName: "Root"})

	declared := map[string]TypeDef{}
	for _, td := range f.TypeDefs {
		declared[td.TypeName()] = td
	}
	if _, ok := declared["RootAItem"].(*AliasDef); !ok {
		t.Errorf("RootAItem is %T, want the $defs integer: a key already spelled as its Go name keeps it", declared["RootAItem"])
	}
	if !structHasProperty(f, "RootAItem2", "u") {
		t.Errorf("RootAItem2 should be $defs/root_a_item, numbered off RootAItem")
	}
	if !structHasProperty(f, "RootAItem3", "z") {
		t.Errorf("the element type should be numbered after both definitions, as RootAItem3")
	}
	for _, m := range g.NameMoves() {
		if _, ok := declared[m.Got]; !ok {
			t.Errorf("NameMoves names %s, which is not declared", m.Got)
		}
	}
}

// A move is reported only once its outcome is declared. A name a position asks
// for is claimed where a declaration is made, and given back if the arm then
// declines -- so a move can be recorded for a spelling nothing ends up declaring,
// and a report naming it would describe a type the package does not have.
func TestNameMovesNeverNameAnUndeclaredType(t *testing.T) {
	g := New(Config{PackageName: "p"})
	held := &schema.Schema{}
	position := &schema.Schema{}
	g.names.claimExactly("Thing", typeHolder(held, "$defs/Thing"))
	g.names.declared["Thing"] = true

	// A position derives Thing, is numbered, claims Thing2 -- and declines.
	name := g.unclaimedTypeName("Thing", position)
	if name != "Thing2" {
		t.Fatalf("the position was given %s, want Thing2", name)
	}
	g.names.claimExactly(name, g.holderFor(position, ""))
	g.releaseTypeName(name, position)
	if moves := g.NameMoves(); len(moves) != 0 {
		t.Fatalf("NameMoves reports %+v for a name nothing declared", moves)
	}

	// Declared, the same move is reported.
	g.names.claimExactly(name, g.holderFor(position, ""))
	g.declare(name)
	moves := g.NameMoves()
	if len(moves) != 1 || moves[0].Wanted != "Thing" || moves[0].Got != "Thing2" {
		t.Fatalf("NameMoves = %+v, want the one move Thing -> Thing2", moves)
	}
}

// Among definitions folding onto one name, the key already spelled as the name
// keeps it -- $defs/X keeps X ahead of $defs/!, whose key sorts first and has no
// letters at all -- which is the order the CLI's splitFoldedClaims gives the
// same claims, so a library caller and the CLI name one document alike.
func TestAKeySpelledAsItsNameKeepsIt(t *testing.T) {
	_, f := generateForNamesTest(t, `{
		"type": "object",
		"properties": {"bang": {"$ref": "#/$defs/!"}, "x": {"$ref": "#/$defs/X"}},
		"$defs": {"!": {"type": "integer"}, "X": {"type": "string"}}
	}`, Config{RootTypeName: "Root"})
	if got := fieldTypeName(t, f, "Root", "x"); got != "X" {
		t.Errorf("$defs/X is typed %s, want X", got)
	}
	if got := fieldTypeName(t, f, "Root", "bang"); got != "X2" {
		t.Errorf("$defs/! is typed %s, want X2", got)
	}
}

// A definition generated early can mint a position name that a definition
// generated later is keyed as: $defs/A's property b is the position AB, and
// $defs/a_b, which derives AB too, comes after A. The definitions claim their
// names before anything is generated, so it is the position that is numbered,
// not the definition whose key derives the name.
func TestAPositionNeverDisplacesALaterDefinition(t *testing.T) {
	_, f := generateForNamesTest(t, `{
		"type": "object",
		"properties": {"a": {"$ref": "#/$defs/A"}, "ab": {"$ref": "#/$defs/a_b"}},
		"$defs": {
			"A": {"type": "object", "properties": {"b": {"type": "object", "properties": {"inner": {"type": "string"}}}}},
			"a_b": {"type": "object", "properties": {"outer": {"type": "integer"}}}
		}
	}`, Config{RootTypeName: "Root"})
	if got := fieldTypeName(t, f, "Root", "ab"); got != "AB" || !structHasProperty(f, "AB", "outer") {
		t.Errorf("$defs/AB is typed %s, want AB with its own property", got)
	}
	if got := fieldTypeName(t, f, "A", "b"); got == "AB" || !structHasProperty(f, got, "inner") {
		t.Errorf("A.b is typed %s; the position must step around $defs/AB and keep its own schema", got)
	}
}

// A definition of another document is named from the key it is written under
// in its own resource -- whichever reference reaches it, an anchor or a pointer
// -- and claimed like any other name, so the root's definition of the same key
// keeps its own type and the other document's is numbered off it.
func TestAnotherResourcesDefinitionIsNamedFromItsOwnKey(t *testing.T) {
	var other schema.Schema
	if err := json.Unmarshal([]byte(`{"$defs":{"Thing":{"$anchor":"anch","type":"object","properties":{"s":{"type":"string"}}}}}`), &other); err != nil {
		t.Fatal(err)
	}
	other.Normalize()
	_, f := generateForNamesTest(t, `{
		"$id": "https://ex.test/root.json",
		"type": "object",
		"properties": {
			"byAnchor": {"$ref": "other.json#anch"},
			"byPointer": {"$ref": "other.json#/$defs/Thing"},
			"mine": {"$ref": "#/$defs/Thing"}
		},
		"$defs": {"Thing": {"type": "object", "properties": {"n": {"type": "integer"}}}}
	}`, Config{RootTypeName: "Root", Resolver: schema.NewMappingResolver(map[string]*schema.Schema{"https://ex.test/other.json": &other})})
	for prop, want := range map[string]string{"byAnchor": "Thing2", "byPointer": "Thing2", "mine": "Thing"} {
		if got := fieldTypeName(t, f, "Root", prop); got != want {
			t.Errorf("%s is typed %s, want %s", prop, got, want)
		}
	}
	if !structHasProperty(f, "Thing2", "s") || !structHasProperty(f, "Thing", "n") {
		t.Errorf("Thing2 should be other.json's definition and Thing the root's")
	}
}
