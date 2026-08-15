package schemagen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about schema resources embedded inside a single
// input document: a subschema carrying its own $id, which 2020-12 §8.1.1 makes a
// resource in its own right with its own definition namespace. The generator
// materializes their definitions the way it materializes another document's --
// only where a $ref reaches one -- but the guard that keeps two same-named
// definitions apart could not see them at all, because it was keyed on
// documents-as-files. Two of them declaring "$defs/X" silently became one Go
// type and the other was discarded: exactly the outcome #249 rejected and #297
// fixed one spelling of. Issue #308.
//
// The behavioural ones generate through the CLI, compile the result and run
// documents through it, for the reason the #249 and #297 tests give: the defect
// emitted a package that compiled perfectly and typed one position with another
// resource's schema.

// The reproducer from the issue. One input file, two embedded resources, each
// declaring $defs/X -- a string with minLength 3 and an integer with minimum 10.
// The whole output used to be `type X string` with both properties pointing at
// it, so every document setting b was refused at decode whatever it held.
const embeddedTwoResources = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/root.json",
	"title": "Root",
	"type": "object",
	"properties": {
		"a": {"$ref": "https://ex.test/a.json#/$defs/X"},
		"b": {"$ref": "https://ex.test/b.json#/$defs/X"}
	},
	"$defs": {
		"A": {"$id": "https://ex.test/a.json", "$defs": {"X": {"type": "string", "minLength": 3}}},
		"B": {"$id": "https://ex.test/b.json", "$defs": {"X": {"type": "integer", "minimum": 10}}}
	}
}`

func TestEmbeddedResourcesClaimingOneDefinitionNameKeepTheirOwnSchema(t *testing.T) {
	dir, paths := writeSchemas(t, "single.json", embeddedTwoResources)

	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	// The same diagnostic the other two spellings produce, naming each resource
	// by the $id that makes it one -- its file is its container's and would say
	// nothing about which of the resources in it declared the name.
	for _, want := range []string{
		"2 documents claim the Go type name X",
		"https://ex.test/a.json (reached by $ref) $defs/X becomes AX",
		"https://ex.test/b.json (reached by $ref) $defs/X becomes BX",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			// Valid per the schema, and refused at decode before the fix.
			{"Root", `{"a":"abc","b":10}`, true, `{"a":"abc","b":10}`},
			// Each resource's own constraint has to survive, not just its Go type.
			{"Root", `{"a":"ab","b":10}`, false, ""},
			{"Root", `{"a":"abc","b":5}`, false, ""},
			// Each position holds the other's shape.
			{"Root", `{"a":10,"b":"abc"}`, false, ""},
			{"Root", `{}`, true, `{}`},
		})
}

// The same collapse reached by $anchor rather than by JSON Pointer. The two
// definitions do not even share a $defs key here -- only the anchor name -- so
// the contested name is the one the *reference* derives, Tee, and a claim filed
// under the $defs key would have been filed under a name nothing declares.
func TestEmbeddedResourcesClaimingOneAnchorNameKeepTheirOwnSchema(t *testing.T) {
	dir, paths := writeSchemas(t, "anchor.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json",
		"title": "Root",
		"type": "object",
		"properties": {
			"a": {"$ref": "https://ex.test/a.json#tee"},
			"b": {"$ref": "https://ex.test/b.json#tee"}
		},
		"$defs": {
			"A": {"$id": "https://ex.test/a.json",
				"$defs": {"P": {"$anchor": "tee", "type": "string", "minLength": 3}}},
			"B": {"$id": "https://ex.test/b.json",
				"$defs": {"Q": {"$anchor": "tee", "type": "integer", "minimum": 10}}}
		}}`)

	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"2 documents claim the Go type name Tee",
		// The claim is named Tee, the way generation names it, and *described*
		// by the key its author wrote it under.
		"https://ex.test/a.json (reached by $ref) $defs/P becomes ATee",
		"https://ex.test/b.json (reached by $ref) $defs/Q becomes BTee",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"a":"abc","b":10}`, true, `{"a":"abc","b":10}`},
			{"Root", `{"a":"ab","b":10}`, false, ""},
			{"Root", `{"a":"abc","b":5}`, false, ""},
			{"Root", `{"a":10,"b":"abc"}`, false, ""},
		})
}

// The anchor spelling across *documents*, which the #297 walk was watching and
// still missed for the same reason: it claimed the $defs key while generation
// named the node after the reference. Neither a.json nor b.json is an input.
func TestReferencedDocumentsClaimingOneAnchorNameKeepTheirOwnSchema(t *testing.T) {
	dir, paths := writeSchemas(t,
		"a.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema",
			"$id":"https://ex.test/a.json",
			"$defs":{"P":{"$anchor":"tee","type":"string","minLength":3}}}`,
		"b.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema",
			"$id":"https://ex.test/b.json",
			"$defs":{"Q":{"$anchor":"tee","type":"integer","minimum":10}}}`,
		"root.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema",
			"$id":"https://ex.test/root.json","title":"Root","type":"object",
			"properties":{"a":{"$ref":"a.json#tee"},"b":{"$ref":"b.json#tee"}}}`)

	stderr, err := runGenerateCapturing(t, paths[2], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"2 documents claim the Go type name Tee",
		"a.json (reached by $ref) $defs/P becomes ATee",
		"b.json (reached by $ref) $defs/Q becomes BTee",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[2], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"a":"abc","b":10}`, true, `{"a":"abc","b":10}`},
			{"Root", `{"a":"ab","b":10}`, false, ""},
			{"Root", `{"a":"abc","b":5}`, false, ""},
		})
}

// The same two resources reached by a JSON Pointer written against the
// *container* rather than by each resource's $id. The reference never names a
// document, so the pre-#308 walk read it as "stays inside its own document" and
// stopped -- but the node it lands on belongs to the resource "$defs/A"
// establishes, and it is that resource's namespace X is claimed out of.
func TestPointerThroughAContainerIntoAnEmbeddedResourceIsAResourceClaim(t *testing.T) {
	dir, paths := writeSchemas(t, "thru.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json",
		"title": "Root",
		"type": "object",
		"properties": {
			"a": {"$ref": "#/$defs/A/$defs/X"},
			"b": {"$ref": "#/$defs/B/$defs/X"}
		},
		"$defs": {
			"A": {"$id": "https://ex.test/a.json", "$defs": {"X": {"type": "string", "minLength": 3}}},
			"B": {"$id": "https://ex.test/b.json", "$defs": {"X": {"type": "integer", "minimum": 10}}}
		}}`)

	stderr, err := runGenerateCapturing(t, paths[0], "-o", filepath.Join(dir, "gen"), "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "2 documents claim the Go type name X") {
		t.Errorf("the split should be reported, got:\n%s", stderr)
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"a":"abc","b":10}`, true, `{"a":"abc","b":10}`},
			{"Root", `{"a":"abc","b":5}`, false, ""},
		})
}

// A document nobody listed is as free to embed resources as an input is, and
// the two questions compose: the claim belongs to the resource, not to the file
// the resolver read it out of. Recording it against the file put two definition
// namespaces under one label, and which of them was named by its $id and which
// by the file then depended on the order the walk reached them in -- the second
// reference into a file finds the resources the first one indexed.
//
// So this asserts the labels as well as the types: both resources are named by
// their own $id, and the answer does not move when the two references swap
// places.
func TestEmbeddedResourcesOfAReferencedDocumentAreNamedByTheirOwnID(t *testing.T) {
	const container = `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/c.json",
		"$defs": {
			"A": {"$id": "https://ex.test/c-a.json", "$defs": {"X": {"type": "string", "minLength": 3}}},
			"B": {"$id": "https://ex.test/c-b.json", "$defs": {"X": {"type": "integer", "minimum": 10}}}}}`

	for _, tc := range []struct{ name, props string }{
		{"as written", `"a": {"$ref": "c.json#/$defs/A/$defs/X"}, "b": {"$ref": "c.json#/$defs/B/$defs/X"}`},
		{"reversed", `"a": {"$ref": "c.json#/$defs/B/$defs/X"}, "b": {"$ref": "c.json#/$defs/A/$defs/X"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, paths := writeSchemas(t, "c.json", container, "root.json", `{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
				"properties": {`+tc.props+`}}`)

			out := filepath.Join(dir, "gen")
			stderr, err := runGenerateCapturing(t, paths[1], "-o", out, "-p", "gen")
			if err != nil {
				t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
			}
			for _, want := range []string{
				"https://ex.test/c-a.json (reached by $ref) $defs/X becomes CAX",
				"https://ex.test/c-b.json (reached by $ref) $defs/X becomes CBX",
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("missing %q in:\n%s", want, stderr)
				}
			}
			got := strings.Join(declaredTypeNames(t, out), ",")
			if !strings.Contains(got, "CAX") || !strings.Contains(got, "CBX") {
				t.Errorf("declared types = %s, want both CAX and CBX", got)
			}
		})
	}
}

// Sharing is the answer that cannot be taken back, and it is still the answer
// when the resources agree. Two embedded resources declaring the same definition
// keep one Go type and nothing is reported.
func TestEmbeddedResourcesThatAgreeStayOneType(t *testing.T) {
	const body = `{"type":"object","properties":{"k":{"type":"string"}},"required":["k"]}`
	dir, paths := writeSchemas(t, "agree.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
		"properties": {
			"a": {"$ref": "https://ex.test/a.json#/$defs/X"},
			"b": {"$ref": "https://ex.test/b.json#/$defs/X"}},
		"$defs": {
			"A": {"$id": "https://ex.test/a.json", "$defs": {"X": `+body+`}},
			"B": {"$id": "https://ex.test/b.json", "$defs": {"X": `+body+`}}}}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "claim the Go type name") {
		t.Errorf("agreeing definitions should not be split:\n%s", stderr)
	}
	got := strings.Join(declaredTypeNames(t, out), ",")
	if !strings.Contains(got, "X") || strings.Contains(got, "AX") {
		t.Errorf("declared types = %s, want one shared X", got)
	}
}

// An embedded resource contributes only what a $ref reaches, for the same reason
// a referenced document does: the generator declares an unreferenced definition
// of neither, so claiming one would move a type of the containing document to
// make room for one that is never declared.
func TestUnreachedDefinitionsOfAnEmbeddedResourceClaimNothing(t *testing.T) {
	dir, paths := writeSchemas(t, "unreached.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
		"properties": {
			"a": {"$ref": "https://ex.test/a.json#/$defs/Inner"},
			"o": {"$ref": "#/$defs/Other"}},
		"$defs": {
			"Other": {"type": "integer"},
			"A": {"$id": "https://ex.test/a.json",
				"$defs": {"Inner": {"type": "string"}, "Other": {"type": "boolean"}}}}}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "Other") {
		t.Errorf("the embedded resource's unreferenced $defs/Other claims nothing:\n%s", stderr)
	}
	if src := readFileString(t, filepath.Join(out, "unreached.go")); !strings.Contains(src, "type Other int64") {
		t.Errorf("the containing document's own Other should keep its name and type:\n%s", src)
	}
}

// A reference from inside an embedded resource into that same resource reaches
// nothing new, and must not be read as a claim against it. The two resources
// here each hold a $defs/X they refer to locally; nothing is contested and
// nothing may be renamed.
func TestReferenceInsideAnEmbeddedResourceClaimsNothing(t *testing.T) {
	dir, paths := writeSchemas(t, "local.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
		"properties": {"a": {"$ref": "https://ex.test/a.json"}},
		"$defs": {
			"A": {"$id": "https://ex.test/a.json", "type": "object",
				"properties": {"x": {"$ref": "#/$defs/X"}},
				"$defs": {"X": {"type": "string", "minLength": 3}}}}}`)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "claim the Go type name") {
		t.Errorf("a reference inside one resource contests nothing:\n%s", stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); strings.Contains(got, "AX") {
		t.Errorf("declared types = %s, want an unqualified X", got)
	}
}

// --root-name reaches an embedded resource by the "id:" key, exactly as it
// reaches a document nobody listed, and sets the name its definitions are
// qualified with.
func TestRootNameChoosesAnEmbeddedResourcesQualifier(t *testing.T) {
	dir, paths := writeSchemas(t, "single.json", embeddedTwoResources)

	out := filepath.Join(dir, "gen")
	stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen",
		"--root-name", "id:https://ex.test/a.json=Alpha")
	if err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "becomes AlphaX") {
		t.Errorf("--root-name should set the qualifier, got:\n%s", stderr)
	}
	if got := strings.Join(declaredTypeNames(t, out), ","); !strings.Contains(got, "AlphaX") {
		t.Errorf("declared types = %s, want AlphaX", got)
	}
}

// The names must not depend on which order Go happened to walk a map in. The
// whole answer is a set of type names in a generated file, so the file itself is
// the check.
func TestEmbeddedResourceSplitIsDeterministic(t *testing.T) {
	dir, paths := writeSchemas(t, "single.json", embeddedTwoResources)

	var firstSrc, firstErr string
	for i := 0; i < 8; i++ {
		out := filepath.Join(dir, "gen")
		if err := os.RemoveAll(out); err != nil {
			t.Fatal(err)
		}
		stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen")
		if err != nil {
			t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
		}
		src := readFileString(t, filepath.Join(out, "single.go"))
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
