package schemagen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests in this file are about a type declared by one input document and
// used by another. Nothing single-document can see any of it, which is why the
// surface stayed broken: --shared-types scored no deviations over a
// single-document keyword sweep while losing 44 of 48 keywords the moment a
// second document was involved (issue #218), and a bare two-document run
// emitted a package that did not compile while exiting 0 (issue #217).
//
// They generate through the CLI, compile the result, and *run* it against
// documents the schema accepts and rejects. Checking the emitted text is not
// enough: the defect in #218 was a `Validate` that compiled perfectly and
// enforced nothing.

// docInstance is a document and the verdict the schema gives it. remarshal, when
// set, is what the decoded value must marshal back to: whether an absent
// optional property is omitted or fabricated into the output is decided by the
// same lookups, from the same declaration, as whether it is validated.
type docInstance struct {
	doc       string
	valid     bool
	remarshal string
}

// generateCompileRun generates into a fresh module root (argsFor is handed that
// directory and returns the generate arguments), writes a driver that decodes
// each instance into rootType and calls Validate, and runs it. The driver
// reports every instance whose verdict differs from the fixture's.
func generateCompileRun(t *testing.T, argsFor func(modRoot string) []string, importPath, rootType string, instances []docInstance) {
	t.Helper()

	dir := t.TempDir()
	if err := runGenerateArgs(t, argsFor(dir)...); err != nil {
		t.Fatalf("generate: %v", err)
	}

	var cases strings.Builder
	for _, in := range instances {
		fmt.Fprintf(&cases, "\t\t{%q, %t, %q},\n", in.doc, in.valid, in.remarshal)
	}
	driver := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"

	root %q
)

func main() {
	cases := []struct {
		doc       string
		valid     bool
		remarshal string
	}{
%s	}
	failed := 0
	for _, c := range cases {
		var v root.%s
		err := json.Unmarshal([]byte(c.doc), &v)
		if err == nil {
			err = v.Validate()
		}
		if got := err == nil; got != c.valid {
			failed++
			fmt.Printf("FAIL %%s: want valid=%%t, got valid=%%t (err=%%v)\n", c.doc, c.valid, got, err)
			continue
		}
		if err != nil || c.remarshal == "" {
			continue
		}
		back, mErr := json.Marshal(v)
		if mErr != nil {
			failed++
			fmt.Printf("FAIL %%s: marshal: %%v\n", c.doc, mErr)
			continue
		}
		if string(back) != c.remarshal {
			failed++
			fmt.Printf("FAIL %%s: want remarshal %%s, got %%s\n", c.doc, c.remarshal, back)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, importPath, cases.String(), rootType)

	writeFile(t, filepath.Join(dir, "main.go"), driver)
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.23\n")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS") {
		t.Fatalf("generated package did not enforce the cross-document schema: %v\n%s", err, out)
	}
}

// commonDoc is the referenced document: one integer property with a lower bound.
const commonDoc = `{
	"$id": "https://ex.test/c.json",
	"title": "CDoc",
	"type": "object",
	"properties": {"v": {"type": "integer", "minimum": 5}},
	"required": ["v"]
}`

// writeCrossDocSchemas writes commonDoc plus the given extra documents into a
// fresh directory and returns their paths in the order given.
func writeCrossDocSchemas(t *testing.T, docs ...string) (dir string, paths []string) {
	t.Helper()
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "c.json"), commonDoc)
	paths = append(paths, filepath.Join(dir, "c.json"))
	for i, doc := range docs {
		name := fmt.Sprintf("d%d.json", i)
		writeFile(t, filepath.Join(dir, name), doc)
		paths = append(paths, filepath.Join(dir, name))
	}
	return dir, paths
}

// A property whose type comes from another input document must be dispatched to
// by the referencing type's Validate. This is issue #218 in its smallest form:
// CDoc.Validate carried the bound and MDoc.Validate never called it, so every
// constraint behind a cross-document $ref went unenforced.
func TestSharedTypesDelegatesToAReferencedDocument(t *testing.T) {
	_, paths := writeCrossDocSchemas(t, `{
		"$id": "https://ex.test/m.json",
		"title": "MDoc",
		"type": "object",
		"properties": {"c": {"$ref": "https://ex.test/c.json"}},
		"required": ["c"]
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths, "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			{`{"c":{"v":6}}`, true, ""},
			{`{"c":{"v":4}}`, false, ""},
			{`{"c":{}}`, false, ""},
		})
}

// The same across a slice, a map and a scalar position at once. Each is settled
// by a different pass -- ValidatableFields for the direct field and the two
// containers, resolveItemValidations for the element checks -- and every one of
// them read only the file being generated.
func TestSharedTypesDelegatesThroughContainersToAReferencedDocument(t *testing.T) {
	_, paths := writeCrossDocSchemas(t, `{
		"$id": "https://ex.test/m.json",
		"title": "MDoc",
		"type": "object",
		"properties": {
			"one":   {"$ref": "https://ex.test/c.json"},
			"list":  {"type": "array", "items": {"$ref": "https://ex.test/c.json"}},
			"byKey": {"type": "object", "additionalProperties": {"$ref": "https://ex.test/c.json"}}
		},
		"required": ["one", "list", "byKey"]
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths, "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			{`{"one":{"v":6},"list":[{"v":6}],"byKey":{"k":{"v":6}}}`, true, ""},
			{`{"one":{"v":4},"list":[{"v":6}],"byKey":{"k":{"v":6}}}`, false, ""},
			{`{"one":{"v":6},"list":[{"v":4}],"byKey":{"k":{"v":6}}}`, false, ""},
			{`{"one":{"v":6},"list":[{"v":6}],"byKey":{"k":{"v":4}}}`, false, ""},
		})
}

// A referenced document is not always a struct, and the question "what kind of
// declaration is this name" is asked by a different lookup for each kind: an
// enum, an alias over a constrained scalar, an alias over an array, and an alias
// over another document's struct. Every one of those lookups read only the file
// being generated, so each kind lost its check independently.
func TestSharedTypesDelegatesToEveryKindOfReferencedDeclaration(t *testing.T) {
	dir, paths := writeCrossDocSchemas(t,
		`{"$id": "https://ex.test/e.json", "title": "EDoc", "type": "string", "enum": ["red", "green"]}`,
		`{"$id": "https://ex.test/s.json", "title": "SDoc", "type": "string", "minLength": 3}`,
		`{"$id": "https://ex.test/arr.json", "title": "ArrDoc", "type": "array",
		  "items": {"type": "integer", "minimum": 5}, "minItems": 1}`,
		// An alias whose underlying type is another input's struct.
		`{"$id": "https://ex.test/alias.json", "title": "AliasDoc", "$ref": "https://ex.test/c.json"}`,
		`{
			"$id": "https://ex.test/m.json",
			"title": "MDoc",
			"type": "object",
			"properties": {
				"opt":       {"$ref": "https://ex.test/c.json"},
				"colour":    {"$ref": "https://ex.test/e.json"},
				"s":         {"$ref": "https://ex.test/s.json"},
				"arr":       {"$ref": "https://ex.test/arr.json"},
				"al":        {"$ref": "https://ex.test/alias.json"},
				"listOfArr": {"type": "array", "items": {"$ref": "https://ex.test/arr.json"}}
			}
		}`)
	_ = dir

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths, "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			// Every property is optional, so the empty document conforms. It is
			// the control: a delegation that ran against the Go zero of an
			// absent property would reject it.
			{`{}`, true, ""},
			{`{"opt":{"v":6}}`, true, ""},
			{`{"opt":{"v":4}}`, false, ""},
			{`{"colour":"red"}`, true, ""},
			{`{"colour":"blue"}`, false, ""},
			{`{"s":"abc"}`, true, ""},
			{`{"s":"ab"}`, false, ""},
			{`{"arr":[6]}`, true, ""},
			{`{"arr":[4]}`, false, ""},
			{`{"arr":[]}`, false, ""},
			{`{"al":{"v":6}}`, true, ""},
			{`{"al":{"v":4}}`, false, ""},
			{`{"listOfArr":[[6]]}`, true, ""},
			{`{"listOfArr":[[4]]}`, false, ""},
		})
}

// The kinds of declaration whose Go representation is not a plain named type:
// the raw-JSON wrappers a bare "not" and a oneOf-only document produce, an
// object typed as a Go map, and an alias over a primitive carrying a default.
//
// Their round-trip is checked as well as their validation, because the same
// lookups decide both. A wrapper struct is never omitted by omitempty, so a
// referencing file that did not recognize one emitted a field without
// ",omitzero" and *invented* an absent optional property into the output: the
// empty document marshalled back as {"d":null,"mp":{},"n":null}.
func TestSharedTypesDelegatesToWrapperAndContainerDeclarations(t *testing.T) {
	_, paths := writeCrossDocSchemas(t,
		`{"$id": "https://ex.test/not.json", "title": "NotDoc", "not": {"type": "string"}}`,
		`{"$id": "https://ex.test/dyn.json", "title": "DynDoc",
		  "oneOf": [{"type": "integer", "minimum": 10}, {"type": "boolean"}]}`,
		`{"$id": "https://ex.test/mapa.json", "title": "MapDoc", "type": "object",
		  "additionalProperties": {"type": "integer", "minimum": 5}}`,
		`{"$id": "https://ex.test/prim.json", "title": "PrimDoc", "type": "string", "minLength": 2}`,
		// An alias *over* one of those wrappers, in a third document. A defined
		// type inherits none of the wrapper's methods, so unless the alias is
		// seen to be over one it borrows neither -- and the property then
		// refuses every document the schema accepts, in the decoder.
		`{"$id": "https://ex.test/alias2.json", "title": "Alias2Doc", "$ref": "https://ex.test/not.json"}`,
		`{
			"$id": "https://ex.test/m.json",
			"title": "MDoc",
			"type": "object",
			"properties": {
				"nested":      {"type": "array", "items": {"type": "array", "items": {"$ref": "https://ex.test/c.json"}}},
				"n":           {"$ref": "https://ex.test/not.json"},
				"d":           {"$ref": "https://ex.test/dyn.json"},
				"mp":          {"$ref": "https://ex.test/mapa.json"},
				"a2":          {"$ref": "https://ex.test/alias2.json"},
				"withDefault": {"$ref": "https://ex.test/prim.json", "default": "xy"}
			}
		}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths, "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			{`{}`, true, `{}`},
			// Two array levels: the outer is dispatched by the field, the inner
			// only by the per-element checks.
			{`{"nested":[[{"v":6}]]}`, true, ""},
			{`{"nested":[[{"v":4}]]}`, false, ""},
			{`{"n":7}`, true, `{"n":7}`},
			{`{"n":"boom"}`, false, ""},
			{`{"d":11}`, true, `{"d":11}`},
			{`{"d":5}`, false, ""},
			{`{"mp":{"k":6}}`, true, `{"mp":{"k":6}}`},
			{`{"mp":{"k":4}}`, false, ""},
			{`{"a2":9}`, true, `{"a2":9}`},
			{`{"a2":"boom"}`, false, ""},
			{`{"withDefault":"ab"}`, true, ""},
			{`{"withDefault":"a"}`, false, ""},
		})
}

// Whether an optional property needs a pointer to tell "absent" from "present
// and zero", and whether it needs ",omitzero" rather than ",omitempty", are
// answered from the property type's *declaration*. A referencing file that could
// not see the declaration answered from the fallback, and the round-trip broke
// in both directions at once: a present empty string, empty array or empty enum
// was dropped from the output, and an absent wrapper or map was invented into
// it.
func TestSharedTypesRoundTripsOptionalPropertiesTypedByAnotherDocument(t *testing.T) {
	_, paths := writeCrossDocSchemas(t,
		`{"$id": "https://ex.test/plain.json", "title": "PlainDoc", "type": "string"}`,
		// Constraints but no "type": an inferred-alias wrapper struct.
		`{"$id": "https://ex.test/inf.json", "title": "InfDoc", "minimum": 5}`,
		`{"$id": "https://ex.test/arr.json", "title": "ArrDoc", "type": "array", "items": {"type": "integer"}}`,
		`{"$id": "https://ex.test/mp.json", "title": "MpDoc", "type": "object",
		  "additionalProperties": {"type": "integer"}}`,
		`{"$id": "https://ex.test/en.json", "title": "EnDoc", "type": "string", "enum": ["", "red"]}`,
		`{
			"$id": "https://ex.test/m.json",
			"title": "MDoc",
			"type": "object",
			"properties": {
				"p":  {"$ref": "https://ex.test/plain.json"},
				"i":  {"$ref": "https://ex.test/inf.json"},
				"a":  {"$ref": "https://ex.test/arr.json"},
				"mp": {"$ref": "https://ex.test/mp.json"},
				"en": {"$ref": "https://ex.test/en.json"}
			}
		}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths, "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			// Nothing was set, so nothing may be emitted.
			{`{}`, true, `{}`},
			// Each of these is present and at its type's zero, and must survive.
			{`{"p":""}`, true, `{"p":""}`},
			{`{"a":[]}`, true, `{"a":[]}`},
			{`{"mp":{}}`, true, `{"mp":{}}`},
			{`{"en":""}`, true, `{"en":""}`},
			// And the constraints behind the same refs still apply.
			{`{"i":6}`, true, `{"i":6}`},
			{`{"i":4}`, false, ""},
			{`{"en":"nope"}`, false, ""},
		})
}

// The same blindness in the other direction: asked whether a name carries a
// Validate, the element-position lookup answers "yes" for a name it cannot find,
// because within one file every generated name does. A name another document
// declared was not found -- and a document that says nothing is `type AnyDoc
// any`, which has no methods at all. --shared-types therefore emitted
// `_typed.Validate()` against it and the package did not compile, with
// generation reporting success.
func TestSharedTypesDoesNotCallValidateOnAnUncheckableTypeFromAnotherDocument(t *testing.T) {
	_, paths := writeCrossDocSchemas(t,
		// A document that constrains nothing: its Go type is `any`.
		`{"$id": "https://ex.test/any.json", "title": "AnyDoc"}`,
		`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://ex.test/m.json",
			"title": "MDoc",
			"type": "object",
			"properties": {
				"l":    {"type": "array", "items": {"$ref": "https://ex.test/any.json"}},
				"t":    {"type": "array", "prefixItems": [{"$ref": "https://ex.test/any.json"}]},
				"keep": {"$ref": "https://ex.test/c.json"}
			}
		}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths, "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			// An unconstrained element admits anything...
			{`{"l":[1,"x"]}`, true, ""},
			{`{"t":[1]}`, true, ""},
			// ...while the document that does constrain still enforces, so the
			// fix is not "stop dispatching".
			{`{"keep":{"v":6}}`, true, ""},
			{`{"keep":{"v":4}}`, false, ""},
		})
}

// Three documents, so the delegation has to survive a hop it did not itself
// generate: ADoc refs BDoc refs CDoc, and only CDoc states the bound.
func TestSharedTypesDelegatesAcrossAChainOfDocuments(t *testing.T) {
	_, paths := writeCrossDocSchemas(t,
		`{
			"$id": "https://ex.test/b.json",
			"title": "BDoc",
			"type": "object",
			"properties": {"c": {"$ref": "https://ex.test/c.json"}},
			"required": ["c"]
		}`,
		`{
			"$id": "https://ex.test/a.json",
			"title": "ADoc",
			"type": "object",
			"properties": {"b": {"$ref": "https://ex.test/b.json"}},
			"required": ["b"]
		}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths, "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen", "ADoc",
		[]docInstance{
			{`{"b":{"c":{"v":6}}}`, true, ""},
			{`{"b":{"c":{"v":4}}}`, false, ""},
		})
}

// Two documents reaching the same third one. The second of them is generated
// after CDoc's types were materialized *and* after ADoc referenced them, so it
// is the arm that never sees a local declaration of the type at all.
func TestSharedTypesDelegatesThroughADiamond(t *testing.T) {
	_, paths := writeCrossDocSchemas(t,
		`{
			"$id": "https://ex.test/a.json",
			"title": "ADoc",
			"type": "object",
			"properties": {"c": {"$ref": "https://ex.test/c.json"}},
			"required": ["c"]
		}`,
		`{
			"$id": "https://ex.test/b.json",
			"title": "BDoc",
			"type": "object",
			"properties": {"c": {"$ref": "https://ex.test/c.json"}},
			"required": ["c"]
		}`,
		`{
			"$id": "https://ex.test/m.json",
			"title": "MDoc",
			"type": "object",
			"properties": {
				"a": {"$ref": "https://ex.test/a.json"},
				"b": {"$ref": "https://ex.test/b.json"}
			},
			"required": ["a", "b"]
		}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths, "-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types")
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			{`{"a":{"c":{"v":6}},"b":{"c":{"v":6}}}`, true, ""},
			{`{"a":{"c":{"v":4}},"b":{"c":{"v":6}}}`, false, ""},
			{`{"a":{"c":{"v":6}},"b":{"c":{"v":4}}}`, false, ""},
		})
}

// A $ref spelled as a relative path, in documents that declare no $id at all.
// That route reaches the referenced document through the file resolver rather
// than through the run's $id index, so it is a second way for the two documents
// to meet and has to reach the same delegation.
func TestSharedTypesDelegatesForARelativePathRef(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "c.json"),
		`{"title":"CDoc","type":"object","properties":{"v":{"type":"integer","minimum":5}},"required":["v"]}`)
	writeFile(t, filepath.Join(dir, "m.json"),
		`{"title":"MDoc","type":"object","properties":{"c":{"$ref":"c.json"}},"required":["c"]}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return []string{
				filepath.Join(dir, "c.json"), filepath.Join(dir, "m.json"),
				"-o", filepath.Join(modRoot, "gen"), "-p", "gen", "--shared-types",
			}
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			{`{"c":{"v":6}}`, true, ""},
			{`{"c":{"v":4}}`, false, ""},
		})
}

// --schema-package is the mode that was reported correct, and it is: a $ref
// into a *different* package emits the import and the delegating call. But two
// documents assigned to the *same* import path are files of one Go package
// generated by one generator, exactly as under --shared-types, and lost the
// delegation for the same reason. The reported sweep put one document in each
// package, so it never showed.
func TestSchemaPackageDelegatesBetweenTwoDocumentsOfOnePackage(t *testing.T) {
	_, paths := writeCrossDocSchemas(t, `{
		"$id": "https://ex.test/m.json",
		"title": "MDoc",
		"type": "object",
		"properties": {"c": {"$ref": "https://ex.test/c.json"}},
		"required": ["c"]
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths,
				"-o", modRoot,
				"--schema-package", "https://ex.test/c.json=example.com/m/gen",
				"--schema-package", "https://ex.test/m.json=example.com/m/gen",
			)
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			{`{"c":{"v":6}}`, true, ""},
			{`{"c":{"v":4}}`, false, ""},
		})
}

// And the mode that was already right stays right: a $ref across a package
// boundary keeps importing the owning package and calling its Validate.
func TestSchemaPackageStillDelegatesAcrossPackages(t *testing.T) {
	_, paths := writeCrossDocSchemas(t, `{
		"$id": "https://ex.test/m.json",
		"title": "MDoc",
		"type": "object",
		"properties": {"c": {"$ref": "https://ex.test/c.json"}},
		"required": ["c"]
	}`)

	generateCompileRun(t,
		func(modRoot string) []string {
			return append(paths,
				"-o", modRoot,
				"--schema-package", "https://ex.test/c.json=example.com/m/cpkg",
				"--schema-package", "https://ex.test/m.json=example.com/m/gen",
			)
		},
		"example.com/m/gen", "MDoc",
		[]docInstance{
			{`{"c":{"v":6}}`, true, ""},
			{`{"c":{"v":4}}`, false, ""},
		})
}

// Issue #217: without a mode that shares types, each input materializes the
// document its $ref reaches, and the two files declare the same type in one
// package. Nothing about either file is wrong on its own, so the run used to
// exit 0 and leave a package the Go compiler refuses.
func TestTwoDocumentsRedeclaringATypeInOnePackageIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.json"),
		`{"title":"Common","type":"object","properties":{"v":{"type":"integer","minimum":5}},"required":["v"]}`)
	writeFile(t, filepath.Join(dir, "main.json"),
		`{"title":"Main","type":"object","properties":{"c":{"$ref":"common.json"}},"required":["c"]}`)

	out := t.TempDir()
	err := runGenerateArgs(t,
		filepath.Join(dir, "common.json"), filepath.Join(dir, "main.json"),
		"-o", out, "-p", "main")
	if err == nil {
		t.Fatal("two inputs declaring the same type in one package must not be reported as a successful run")
	}
	msg := err.Error()
	for _, want := range []string{"common.json", "main.json", "Common", "--shared-types", "--schema-package"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should name %q, got: %v", want, msg)
		}
	}

	// And it must not leave the package it refused behind.
	if _, statErr := os.Stat(filepath.Join(out, "main.go")); statErr == nil {
		t.Error("the file that would have redeclared the type was written anyway")
	}
}

// The same refusal for two inputs that share no $ref at all but happen to name
// a type the same way: the collision is between the files of a package, not
// between the schemas.
func TestTwoUnrelatedDocumentsClaimingOneTypeNameIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one.json"),
		`{"title":"Same","type":"object","properties":{"v":{"type":"integer"}}}`)
	writeFile(t, filepath.Join(dir, "two.json"),
		`{"title":"Same","type":"object","properties":{"w":{"type":"string"}}}`)

	if err := runGenerateArgs(t,
		filepath.Join(dir, "one.json"), filepath.Join(dir, "two.json"),
		"-o", t.TempDir(), "-p", "gen"); err == nil {
		t.Fatal("two inputs both declaring Same in one package must be refused")
	}
}

// Two inputs that share nothing keep generating into one package, which is the
// case the refusal above must not reach.
func TestTwoIndependentDocumentsStillGenerateIntoOnePackage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one.json"),
		`{"title":"One","type":"object","properties":{"v":{"type":"integer","minimum":5}},"required":["v"]}`)
	writeFile(t, filepath.Join(dir, "two.json"),
		`{"title":"Two","type":"object","properties":{"w":{"type":"string","minLength":3}},"required":["w"]}`)

	out := t.TempDir()
	if err := runGenerateArgs(t,
		filepath.Join(dir, "one.json"), filepath.Join(dir, "two.json"),
		"-o", out, "-p", "gen"); err != nil {
		t.Fatalf("independent documents should generate into one package: %v", err)
	}
	if buildOut, err := buildGenerated(t, out, "example.com/m"); err != nil {
		t.Fatalf("generated package does not compile: %v\n%s", err, buildOut)
	}
}
