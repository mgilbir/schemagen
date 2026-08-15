package schemagen

import (
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are about the dialect a *schema resource* is read
// under. 2020-12 §8.1.1 lets a subschema that carries an $id declare its own
// $schema, and its dialect is then its own -- so a draft-07 resource embedded in
// a 2020-12 document keeps draft 7's rule that a $ref replaces everything
// written beside it.
//
// It did not. The generator asked that question in two places: a per-node
// refOverridesSiblingsForSchema with seventeen call sites, and a run-level
// refOverridesSiblings reading the *root* document's dialect with three. One of
// the three decided whether a property's sibling assertions bind, so minLength,
// maxLength and pattern written beside a $ref were enforced inside a draft-07
// resource while enum and const -- which go through the per-node spelling --
// were correctly dropped. Issue #309. The run-level spelling is gone.

const embeddedDraft7Resource = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "https://ex.test/root.json",
	"title": "Root",
	"type": "object",
	"properties": {"a": {"$ref": "#/$defs/T"}},
	"$defs": {"T": {
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id": "https://ex.test/t.json",
		"title": "T",
		"type": "object",
		"properties": {"x": {"$ref": "#/definitions/S", "minLength": 10}},
		"definitions": {"S": {"type": "string", "minLength": 3}}}}}`

// The reproducer. Only the target's minLength 3 binds; the minLength 10 beside
// the $ref is not there to be read, so "abcd" is a valid document and was
// refused.
func TestEmbeddedResourceDialectDecidesWhetherRefReplacesItsSiblings(t *testing.T) {
	dir, paths := writeSchemas(t, "fwd.json", embeddedDraft7Resource)

	out := filepath.Join(dir, "gen")
	if stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen"); err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	src := readFileString(t, filepath.Join(out, "fwd.go"))
	if strings.Contains(src, "minimum 10") {
		t.Errorf("draft 7 says the sibling minLength is not read; it was enforced:\n%s", src)
	}
	if !strings.Contains(src, "minimum 3") {
		t.Errorf("the $ref target's own minLength must still bind:\n%s", src)
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"a":{"x":"ab"}}`, false, ""},
			// Valid per the schema, and refused before the fix.
			{"Root", `{"a":{"x":"abcd"}}`, true, `{"a":{"x":"abcd"}}`},
			{"Root", `{"a":{"x":"abcdefghijk"}}`, true, `{"a":{"x":"abcdefghijk"}}`},
		})
}

// The control that says it is the resource's own $schema being read and not the
// embedding: the same document with the nested $schema removed. The resource
// then inherits 2020-12, where $ref is an ordinary applicator, and the sibling
// minLength *does* bind.
func TestEmbeddedResourceWithoutItsOwnSchemaKeepsTheContainingDialect(t *testing.T) {
	dir, paths := writeSchemas(t, "inherit.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json",
		"title": "Root",
		"type": "object",
		"properties": {"a": {"$ref": "#/$defs/T"}},
		"$defs": {"T": {
			"$id": "https://ex.test/t.json",
			"title": "T",
			"type": "object",
			"properties": {"x": {"$ref": "#/$defs/S", "minLength": 10}},
			"$defs": {"S": {"type": "string", "minLength": 3}}}}}`)

	out := filepath.Join(dir, "gen")
	if stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen"); err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if src := readFileString(t, filepath.Join(out, "inherit.go")); !strings.Contains(src, "minimum 10") {
		t.Errorf("under 2020-12 the sibling minLength applies beside the $ref:\n%s", src)
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"a":{"x":"abcd"}}`, false, ""},
			{"Root", `{"a":{"x":"abcdefghijk"}}`, true, `{"a":{"x":"abcdefghijk"}}`},
		})
}

// The second of the three run-level sites: the walk that asks whether a schema
// positively excludes a JSON null at its own position, which decides whether the
// decoder rejects one. The sibling "type" here names only "string" and the $ref
// target admits a null, so under draft 7 -- where the sibling is not there to be
// read -- a null is a value the position holds. Reading the run's 2020-12
// instead made the sibling bind and the null was refused.
//
// The issue filed against this one line said only that the other two sites "are
// the same shape and I have not built a case that reaches either". This is that
// case, and it fires.
func TestEmbeddedResourceDialectDecidesWhetherARefSiblingForbidsNull(t *testing.T) {
	dir, paths := writeSchemas(t, "nul.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
		"properties": {"a": {"$ref": "#/$defs/T"}},
		"$defs": {"T": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"$id": "https://ex.test/t.json", "title": "T", "type": "object",
			"properties": {"x": {"$ref": "#/definitions/S", "type": "string"}},
			"definitions": {"S": {"type": ["string", "null"]}}}}}`)

	out := filepath.Join(dir, "gen")
	if stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen"); err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	if src := readFileString(t, filepath.Join(out, "nul.go")); !strings.Contains(src, "_jsonNulls") {
		t.Errorf("a null the target permits must be kept, not rejected:\n%s", src)
	}

	generateCompileRunRoots(t,
		func(modRoot string) []string {
			return []string{paths[0], "-o", filepath.Join(modRoot, "gen"), "-p", "gen"}
		},
		"example.com/m/gen",
		[]rootInstance{
			{"Root", `{"a":{"x":null}}`, true, `{"a":{"x":null}}`},
			{"Root", `{"a":{"x":"s"}}`, true, `{"a":{"x":"s"}}`},
		})
}

// The third run-level site: the walk that asks whether a schema excludes the
// *zero* of a Go type, which decides whether a property whose value would be
// written as that zero is omitted instead (issue #250). Under draft 7 the
// sibling minLength is not read, so "" is a value the position holds and the
// property must be written; the run-level reading made the sibling bind and
// omitted it.
func TestEmbeddedResourceDialectDecidesWhetherARefSiblingForbidsTheZero(t *testing.T) {
	dir, paths := writeSchemas(t, "zero.json", `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
		"properties": {"a": {"$ref": "#/$defs/T"}},
		"$defs": {"T": {
			"$schema": "http://json-schema.org/draft-07/schema#",
			"$id": "https://ex.test/t.json", "title": "T", "type": "object",
			"properties": {"x": {"$ref": "#/definitions/S", "minLength": 1}},
			"definitions": {"S": {"type": "string"}}}}}`)

	out := filepath.Join(dir, "gen")
	if stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen", "--omit-empty=false"); err != nil {
		t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
	}
	src := readFileString(t, filepath.Join(out, "zero.go"))
	if strings.Contains(src, `json:"x,omitzero"`) {
		t.Errorf("draft 7 does not read the sibling minLength, so \"\" is permitted at x "+
			"and the property must not be omitted:\n%s", src)
	}
	if !strings.Contains(src, "`json:\"x\"`") {
		t.Errorf("x should be written unconditionally:\n%s", src)
	}
}

// enum and const already went through the per-node predicate, which is what made
// the disagreement visible: two keywords beside one $ref, in one resource, read
// under two different dialects. They must still be dropped, and now for the same
// reason as the assertion keywords rather than by a different route.
func TestEmbeddedDraft7ResourceDropsEveryKindOfRefSibling(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sibling string
		absent  string
	}{
		{"minLength", `"minLength": 10`, "minimum 10"},
		{"maxLength", `"maxLength": 2`, "maximum 2"},
		{"pattern", `"pattern": "^zzz"`, "^zzz"},
		{"enum", `"enum": ["only"]`, `"only"`},
		{"const", `"const": "only"`, `"only"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, paths := writeSchemas(t, "s.json", `{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"$id": "https://ex.test/root.json", "title": "Root", "type": "object",
				"properties": {"a": {"$ref": "#/$defs/T"}},
				"$defs": {"T": {
					"$schema": "http://json-schema.org/draft-07/schema#",
					"$id": "https://ex.test/t.json", "title": "T", "type": "object",
					"properties": {"x": {"$ref": "#/definitions/S", `+tc.sibling+`}},
					"definitions": {"S": {"type": "string", "minLength": 3}}}}}`)

			out := filepath.Join(dir, "gen")
			if stderr, err := runGenerateCapturing(t, paths[0], "-o", out, "-p", "gen"); err != nil {
				t.Fatalf("generate: %v\nstderr:\n%s", err, stderr)
			}
			if src := readFileString(t, filepath.Join(out, "s.go")); strings.Contains(src, tc.absent) {
				t.Errorf("draft 7 does not read %s beside a $ref, but %q reached the output:\n%s",
					tc.name, tc.absent, src)
			}
		})
	}
}
