package schemagen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about the documents a generated file declares types
// for that nobody listed as an input: they arrive by $ref off disk, write no
// file of their own, and were invisible to both of the guards issue #249 left
// behind. Two of them declaring one definition name silently became one Go type
// and the other was discarded. Issue #297.
//
// The behavioural ones generate through the CLI, compile the result and run a
// document through it, for the reason the #249 tests give: the defect emitted a
// package that compiled perfectly and typed one position with another
// document's schema.

const externalInnerA = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/a.json",
	"title": "A",
	"type": "object",
	"$defs": {"Inner": {"type": "string", "minLength": 5}}
}`

const externalInnerB = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/b.json",
	"title": "B",
	"type": "object",
	"$defs": {"Inner": {"type": "integer", "minimum": 100}}
}`

const externalInnerRoot = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/root.json",
	"title": "Root",
	"type": "object",
	"properties": {
		"pa": {"$ref": "a.json#/$defs/Inner"},
		"pb": {"$ref": "b.json#/$defs/Inner"}
	}
}`

// The reproducer. Only root.json is an input; a.json and b.json are read from
// disk by the file resolver, which is the documented relative-path route. Both
// properties came out *Inner -- a.json's string -- so every verdict for pb was
// wrong in both directions: 150 was rejected and "hello" accepted.
func TestReferencedDocumentsClaimingOneDefinitionNameKeepTheirOwnSchema(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", externalInnerA, "b.json", externalInnerB, "root.json", externalInnerRoot)
	rootPath := paths[2]

	stderr, err := runGenerateCapturing(t, rootPath, "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"2 documents claim the Go type name Inner",
		"a.json (reached by $ref) $defs/Inner becomes AInner",
		"b.json (reached by $ref) $defs/Inner becomes BInner",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{rootPath, "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"pa":"hello","pb":150}`, true, `{"pa":"hello","pb":150}`},
			// Each position holds the other's shape; both must be refused.
			{"Root", `{"pa":150}`, false, ""},
			{"Root", `{"pb":"hello"}`, false, ""},
			// b.json's own constraint has to survive, not just its Go type.
			{"Root", `{"pb":3}`, false, ""},
			{"Root", `{"pa":"hi"}`, false, ""},
		})
}

// The same collision with no $defs at all: two documents in different
// directories both titled Common, each referenced whole. Nothing outside the
// title tells the two apart -- neither is an input, so neither has a root name
// the caller chose -- which is #271's answer, numbering, one level up.
func TestReferencedDocumentRootsClaimingOneNameKeepTheirOwnSchema(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, "one", "common.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/one/common.json",
		"title": "Common", "type": "string", "minLength": 5}`)
	writeFile(t, filepath.Join(dir, "two", "common.json"), `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/two/common.json",
		"title": "Common", "type": "integer", "minimum": 100}`)
	rootPath := filepath.Join(dir, "root.json")
	writeFile(t, rootPath, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json",
		"title": "Root", "type": "object",
		"properties": {
			"pa": {"$ref": "one/common.json"},
			"pb": {"$ref": "two/common.json"}}}`)

	stderr, err := runGenerateCapturing(t, rootPath, "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "2 documents claim the Go type name Common") {
		t.Errorf("the split should be reported, got:\n%s", stderr)
	}

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

// Sharing is still the answer where the definitions agree, and that is the
// common case: a definition copied between related documents. Splitting it
// anyway would emit two identical types and a warning about a disagreement that
// is not there.
func TestReferencedDocumentsThatAgreeStayOneType(t *testing.T) {
	const body = `{"type": "object", "properties": {"k": {"type": "string"}}, "required": ["k"]}`
	dir, paths := writeSchemas(t,
		"a.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/a.json","title":"A","$defs":{"Inner":`+body+`}}`,
		"b.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://ex.test/b.json","title":"B","$defs":{"Inner":`+body+`}}`,
		"root.json", externalInnerRoot)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[2], "-o", out, "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "claim the Go type name") {
		t.Errorf("agreeing definitions should not be split:\n%s", stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); !strings.Contains(got, "Inner") || strings.Contains(got, "AInner") {
		t.Errorf("declared types = %s, want one shared Inner", got)
	}
}

// A referenced document contributes only what a reference reaches. The
// generator declares a definition of another document when something refers to
// it and not otherwise, so claiming the rest would move a type of the referring
// document to make room for one that is never declared -- a defect where there
// is none today.
func TestUnreachedDefinitionsOfAReferencedDocumentClaimNothing(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/a.json", "title": "A",
			"$defs": {"Inner": {"type": "string"}, "Other": {"type": "boolean"}}}`,
		"root.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
			"properties": {
				"pa": {"$ref": "a.json#/$defs/Inner"},
				"po": {"$ref": "#/$defs/Other"}},
			"$defs": {"Other": {"type": "integer"}}}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[1], "-o", out, "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "Other") {
		t.Errorf("a.json's unreferenced $defs/Other claims nothing:\n%s", stderr)
	}
	src := readFileString(t, filepath.Join(out, "root.go"))
	if !strings.Contains(src, "type Other int64") {
		t.Errorf("root.json's own Other should keep its name and type:\n%s", src)
	}
}

// The names must not depend on which order Go happened to walk a map in. The
// whole answer is a set of type names in a generated file, so the file itself is
// the check.
func TestReferencedDocumentSplitIsDeterministic(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", externalInnerA, "b.json", externalInnerB, "root.json", externalInnerRoot)

	var firstSrc, firstErr string
	for i := 0; i < 8; i++ {
		out := filepath.Join(dir, "gen")
		if err := os.RemoveAll(out); err != nil {
			t.Fatal(err)
		}
		stderr, err := runGenerateCapturing(t, paths[2], "-o", out, "-p", "gen")
		if err != nil {
			t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
		}
		src := readFileString(t, filepath.Join(out, "root.go"))
		if i == 0 {
			firstSrc, firstErr = src, stderr
			continue
		}
		if src != firstSrc {
			t.Fatalf("run %d generated different source", i)
		}
		if stderr != firstErr {
			t.Fatalf("run %d reported differently:\n%s\nfirst:\n%s", i, stderr, firstErr)
		}
	}
}

// A reference into a referenced document's interior -- not its root, not one of
// its $defs -- is the one shape whose claimed name is derived from the reference
// text rather than read off the document, and it is the arm that calls
// generator.TypeNameForRef. Both documents' "properties/x" derive X, so the two
// contest it and are qualified, and the type the caller ends up holding has to
// be the one its own document described.
//
// This file used to carry TestRefDerivedTypeNameMatchesTheGeneratorsRefNaming,
// a copy of the generator's own TestRefToGoName table run against a copy of the
// generator's derivation, because that derivation was unexported and this
// package could not reach it. Both copies are gone -- the derivation is
// generator.TypeNameForRef and the table is TestRefToGoName, which the two
// cross-document cases moved to -- so nothing here compares two implementations
// any more. What that test never covered, and this one does, is that the name
// so derived is the name generation actually declares.
func TestReferencedDocumentInteriorNodesClaimingOneNameKeepTheirOwnSchema(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/a.json", "title": "A", "type": "object",
			"properties": {"x": {"type": "string", "minLength": 5}}}`,
		"b.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/b.json", "title": "B", "type": "object",
			"properties": {"x": {"type": "integer", "minimum": 100}}}`,
		"root.json", `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
			"properties": {
				"pa": {"$ref": "a.json#/properties/x"},
				"pb": {"$ref": "b.json#/properties/x"}}}`)
	rootPath := paths[2]

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, rootPath, "-o", out, "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"2 documents claim the Go type name X",
		"a.json (reached by $ref) properties/x becomes AX",
		"b.json (reached by $ref) properties/x becomes BX",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
	// The claim is only worth reporting if it names a type generation declares.
	if got := strings.Join(declaredTypeNames(t, out), ","); !strings.Contains(got, "AX") || !strings.Contains(got, "BX") {
		t.Errorf("declared types = %s, want AX and BX", got)
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{rootPath, "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"pa":"hello","pb":150}`, true, `{"pa":"hello","pb":150}`},
			{"Root", `{"pa":150}`, false, ""},
			{"Root", `{"pb":"hello"}`, false, ""},
			{"Root", `{"pb":3}`, false, ""},
			{"Root", `{"pa":"hi"}`, false, ""},
		})
}

// --root-name reaches a document nobody listed, by its $id or its path, and sets
// the name its definitions are qualified with. The diagnostic says so, so it has
// to be true.
func TestRootNameChoosesAReferencedDocumentsQualifier(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", externalInnerA, "b.json", externalInnerB, "root.json", externalInnerRoot)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[2], "-o", out, "-p", "gen",
		"--root-name", "id:https://ex.test/a.json=Alpha")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); !strings.Contains(got, "AlphaInner") {
		t.Errorf("declared types = %s, want AlphaInner", got)
	}
	// The key matched a document of the run, so it is not an unused key.
	if strings.Contains(stderr, "matched no input schema") {
		t.Errorf("a --root-name key that named a referenced document is not unused:\n%s", stderr)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}
