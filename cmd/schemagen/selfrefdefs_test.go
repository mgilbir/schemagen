package schemagen

import (
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about a definition that leads back to its own
// document's root. Nothing multi-document can see any of it -- the defect
// reproduces with a single input and no flags -- which is why the cross-document
// answer in sharedefs.go could not reach it. Issue #259.

// ---------- issue #259: a self-referential definition ----------

// The reproducer. $defs/Thing is {"$ref":"#"}, the root's own property points at
// $defs/Thing, and the file declared Thing twice: once as `type Thing any`,
// emitted while the root's struct was being built and the reference back into
// the in-flight definition re-entered generateTypeDef, and once as the alias the
// definition's own frame appended when it returned. Go source that does not
// compile, caught only by packageDecls.
//
// Thing is the root, so it has to be the root's type -- the run below rejects
// {"thing":42}, which the `any` the defect emitted accepted.
func TestSelfReferentialDefinitionIsDeclaredOnce(t *testing.T) {
	_, paths := writeSchemas(t, "a.json", `{
		"title": "A", "type": "object",
		"properties": {"thing": {"$ref": "#/$defs/Thing"}},
		"$defs": {"Thing": {"$ref": "#"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "A",
		[]docInstance{
			{`{}`, true, `{}`},
			{`{"thing":{}}`, true, `{"thing":{}}`},
			{`{"thing":{"thing":{}}}`, true, `{"thing":{"thing":{}}}`},
			// The root is an object, so the definition that is the root is one
			// too. `type Thing any` took this.
			{`{"thing":42}`, false, ""},
		})

	names := generatedTypeNames(t, paths)
	if want := "A,Thing"; names != want {
		t.Errorf("declared types = %s, want %s", names, want)
	}
}

// The same definition with nothing referencing it. The double declaration needed
// the reference back, but the second half of the defect did not: the definitions
// are generated before the root, so "#" was named from the reference text --
// refToGoName("#") is the literal "Root" -- and the document's root was
// materialized a second time under that name, beside the type the title asks
// for. Two structs for one schema, in a file that compiled and said nothing.
func TestSelfReferentialDefinitionDoesNotDuplicateTheRoot(t *testing.T) {
	_, paths := writeSchemas(t, "a.json", `{
		"title": "A", "type": "object",
		"properties": {"n": {"type": "integer"}},
		"$defs": {"Thing": {"$ref": "#"}}
	}`)

	names := generatedTypeNames(t, paths)
	// jsonInteger is the shared integer helper, not a schema type.
	if want := "A,Thing"; names != want {
		t.Errorf("declared types = %s, want %s -- a second root type named from the $ref text is the defect", names, want)
	}
}

// A self-reference written as the document's own $id rather than as "#". Same
// defect, and the name the reference derived was "V7JSON" rather than "Root",
// which is why the fix reads the resolved node and not the reference text.
func TestSelfReferenceByDocumentIDIsDeclaredOnce(t *testing.T) {
	_, paths := writeSchemas(t, "a.json", `{
		"$id": "https://ex.test/a.json", "title": "A", "type": "object",
		"properties": {"thing": {"$ref": "#/$defs/Thing"}},
		"$defs": {"Thing": {"$ref": "https://ex.test/a.json"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "A",
		[]docInstance{
			{`{"thing":{}}`, true, `{"thing":{}}`},
			{`{"thing":42}`, false, ""},
		})

	if names := generatedTypeNames(t, paths); names != "A,Thing" {
		t.Errorf("declared types = %s, want A,Thing", names)
	}
}

// The cycle with a definition in the middle: a property reaches $defs/Outer,
// which reaches $defs/Inner, which is the root. Inner is generated first (the
// definitions are visited in key order), so the root is built inside Inner's
// frame and Outer is reached from the root's property with Inner still in
// flight.
//
// Outer is the case the fix does not reach: the reference that closes the cycle
// is Outer's own, not the property's, so what it names is decided by
// refCycleAliasDef -- an alias to `any`. Named here so that the boundary is
// recorded rather than discovered: the type is weaker than the schema, but it
// compiles and it does not duplicate the root, which is what this run checks.
func TestSelfReferenceThroughASecondDefinitionDoesNotDuplicateTheRoot(t *testing.T) {
	_, paths := writeSchemas(t, "a.json", `{
		"title": "A", "type": "object",
		"properties": {"o": {"$ref": "#/$defs/Outer"}},
		"$defs": {"Outer": {"$ref": "#/$defs/Inner"}, "Inner": {"$ref": "#"}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "A",
		[]docInstance{{`{"o":{}}`, true, `{"o":{}}`}})

	if names := generatedTypeNames(t, paths); names != "A,Inner,Outer" {
		t.Errorf("declared types = %s, want A,Inner,Outer", names)
	}
}

// A cycle among the definitions that never reaches the root. It was correct
// before and has to stay correct: the fix reads the node in flight, and a rule
// that answered this one too would replace a working answer with a pointer.
func TestDefinitionCycleThatAvoidsTheRootIsUnchanged(t *testing.T) {
	_, paths := writeSchemas(t, "a.json", `{
		"title": "Doc", "type": "object",
		"properties": {"a": {"$ref": "#/$defs/A"}},
		"$defs": {"A": {"$ref": "#/$defs/B"}, "B": {"$ref": "#/$defs/A"}}
	}`)

	if names := generatedTypeNames(t, paths); names != "A,B,Doc" {
		t.Errorf("declared types = %s, want A,B,Doc", names)
	}
}

// A required property whose schema is the definition it sits in. The reference
// arrives while that definition's own frame is open, and the field took the type
// by value -- `type Node struct { Next Node }`, which Go rejects outright with
// "invalid recursive type". The whole package failed to build, for a shape as
// ordinary as a tree node.
//
// The pointer is not decoration: it is the only thing that makes the declaration
// legal, which is why the guard the fix reuses hands a *field* a pointer.
func TestRequiredSelfReferentialPropertyCompiles(t *testing.T) {
	_, paths := writeSchemas(t, "a.json", `{
		"title": "A", "type": "object",
		"properties": {"node": {"$ref": "#/$defs/Node"}},
		"$defs": {"Node": {"type": "object",
			"properties": {"next": {"$ref": "#/$defs/Node"}}, "required": ["next"]}}
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(append([]string{}, paths...), "-o", filepath.Join(modRoot, "gen"), "-p", "gen")
		},
		"example.com/m/gen", "A",
		[]docInstance{
			{`{"node":{"next":{"next":{}}}}`, false, ""},
			{`{}`, true, `{}`},
		})
}

// ---------- issue #259: the collision message ----------

// The guard packageDecls is: one generated file declaring one name twice. No
// schema can ask for that -- the generator declares each name once per file --
// so it is schemagen's own defect, and the message the guard had was written for
// two inputs meeting in a package: "a.json and a.json both declare [Thing]",
// naming the same document twice and offering --shared-types, which has no
// second document to move.
//
// Asserted on the guard directly rather than through a document, because the
// route that reached it is the one this branch fixes. A message nobody has
// watched arrive is not a message.
func TestOneFileDeclaringANameTwiceReportsSchemagensOwnDefect(t *testing.T) {
	decls := newPackageDecls("gen")
	err := decls.add("a.json", []byte("package gen\n\ntype Thing any\n\ntype Thing int\n"))
	if err == nil {
		t.Fatal("a file declaring one name twice must be refused")
	}
	want := `the file generated for a.json declares [Thing] twice in package "gen", so it would not compile; ` +
		`one file declares each type once, whatever the document says, so this is a defect in schemagen rather than in the schema ` +
		`-- --shared-types and --schema-package will not change it. Please report it, with the schema that produced it`
	if err.Error() != want {
		t.Errorf("error =\n%q\nwant\n%q", err.Error(), want)
	}
}

// The two-document message is the other half of the same branch and must not
// have moved: it is the one issue #217 is about, and it is the one that names a
// remedy the caller can act on.
func TestTwoFilesDeclaringANameKeepTheirOwnMessage(t *testing.T) {
	decls := newPackageDecls("gen")
	if err := decls.add("a.json", []byte("package gen\n\ntype Thing any\n")); err != nil {
		t.Fatalf("first file: %v", err)
	}
	err := decls.add("b.json", []byte("package gen\n\ntype Thing int\n"))
	if err == nil {
		t.Fatal("two files declaring one name must be refused")
	}
	if !strings.HasPrefix(err.Error(), `a.json and b.json both declare [Thing] in package "gen"; `) ||
		!strings.Contains(err.Error(), "--shared-types") {
		t.Errorf("unexpected error: %v", err)
	}
}

// generatedTypeNames generates the given inputs into a throwaway package and
// returns its declared type names, comma-joined.
func generatedTypeNames(t *testing.T, paths []string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "gen")
	if _, err := runGenerateCapturing(t, append(append([]string{}, paths...), "-o", out, "-p", "gen")...); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return strings.Join(declaredTypeNames(t, out), ",")
}
