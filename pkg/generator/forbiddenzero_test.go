package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// forbiddenZeroRoot generates one document and returns its root struct.
func forbiddenZeroRoot(t *testing.T, doc string, cfg Config) *StructDef {
	t.Helper()

	var s schema.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s.Normalize()

	ir, err := New(cfg).Generate(&s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, td := range ir.TypeDefs {
		if d, ok := td.(*StructDef); ok && d.Name == "Root" {
			return d
		}
	}
	t.Fatalf("no Root struct in %#v", ir.TypeDefs)
	return nil
}

// TestForbiddenZeroIsOmittedWithoutOmitEmpty is the IR half of issue #250.
//
// Under --omit-empty=false no optional field carries an omission of any kind, so
// each one is written from whatever the decoder left behind. Where that value is
// one the schema forbids at that position, the document the type produces is not
// an instance of the schema it was generated from -- and, for every nilable Go
// type, not even one the type can decode again.
//
// The table is arranged as pairs wherever a pair exists: the property whose zero
// the schema forbids beside the one it admits, differing only in the keyword
// that decides. A blanket "omit every optional field" satisfies the first column
// and fails the second, and the second column is the whole of what keeps this
// fix out of the zero-value invention the flag does license.
func TestForbiddenZeroIsOmittedWithoutOmitEmpty(t *testing.T) {
	tests := []struct {
		name     string
		schema   string
		jsonName string
		omit     bool
		why      string
	}{
		{
			name:     "a typed array cannot be null",
			schema:   `{"title":"Root","type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}`,
			jsonName: "tags",
			omit:     true,
			why:      "issue #250 as reported: a nil slice marshals to null and the property is typed array",
		},
		{
			name:     "an array whose type list admits null keeps its null",
			schema:   `{"title":"Root","type":"object","properties":{"tags":{"type":["array","null"],"items":{"type":"string"}}}}`,
			jsonName: "tags",
			omit:     false,
			why:      "null is a value this property may hold, and omitting it would lose what the document said",
		},
		{
			name:     "a typed object cannot be null",
			schema:   `{"title":"Root","type":"object","properties":{"m":{"type":"object","additionalProperties":{"type":"string"}}}}`,
			jsonName: "m",
			omit:     true,
			why:      "a nil map marshals to null",
		},
		{
			name:     "a property with no schema at all admits a null",
			schema:   `{"title":"Root","type":"object","properties":{"x":{}}}`,
			jsonName: "x",
			omit:     false,
			why:      "an empty schema forbids nothing, null included",
		},
		{
			name:     "a recursive property cannot be null",
			schema:   `{"title":"Root","type":"object","properties":{"child":{"$ref":"#"}}}`,
			jsonName: "child",
			omit:     true,
			why:      "the pointer a self-reference resolves to is nil for an absent property, and the root is typed object",
		},
		{
			name:     "a const excludes the empty string its named type is zero at",
			schema:   `{"title":"Root","type":"object","properties":{"c":{"const":"fixed"}}}`,
			jsonName: "c",
			omit:     true,
			why:      "the re-Validate half: no null in sight, and \"\" is not \"fixed\"",
		},
		{
			name:     "a const of the empty string is satisfied by it",
			schema:   `{"title":"Root","type":"object","properties":{"c":{"const":""}}}`,
			jsonName: "c",
			omit:     false,
			why:      "the pair to the row above: the zero is the one value this property may hold",
		},
		{
			name:     "an enum that lists no empty string excludes it",
			schema:   `{"title":"Root","type":"object","properties":{"e":{"enum":["a","b"]}}}`,
			jsonName: "e",
			omit:     true,
			why:      "the same as const, spelled as a list",
		},
		{
			name:     "an enum that lists the empty string admits it",
			schema:   `{"title":"Root","type":"object","properties":{"e":{"enum":["","b"]}}}`,
			jsonName: "e",
			omit:     false,
			why:      "the pair to the row above",
		},
		{
			name:     "minLength above zero excludes the empty string",
			schema:   `{"title":"Root","type":"object","properties":{"s":{"type":"string","minLength":2}}}`,
			jsonName: "s",
			omit:     true,
			why:      "the bound places the zero outside the values the property admits",
		},
		{
			name:     "minLength of zero excludes nothing",
			schema:   `{"title":"Root","type":"object","properties":{"s":{"type":"string","minLength":0}}}`,
			jsonName: "s",
			omit:     false,
			why:      "the pair to the row above: a bound that admits the zero is not a reason to drop it",
		},
		{
			name:     "a pattern the empty string does not match excludes it",
			schema:   `{"title":"Root","type":"object","properties":{"p":{"type":"string","pattern":"^[a-z]+$"}}}`,
			jsonName: "p",
			omit:     true,
			why:      "one or more lowercase letters is not none of them",
		},
		{
			name:     "a pattern the empty string matches does not",
			schema:   `{"title":"Root","type":"object","properties":{"p":{"type":"string","pattern":"^[a-z]*$"}}}`,
			jsonName: "p",
			omit:     false,
			why:      "the pair to the row above, one metacharacter apart",
		},
		{
			name:     "a pattern no Go regexp can compile decides nothing",
			schema:   `{"title":"Root","type":"object","properties":{"p":{"type":"string","pattern":"^(?=x)a"}}}`,
			jsonName: "p",
			omit:     false,
			why:      "an ECMA-262 lookahead RE2 refuses: the safe answer is the field as it was",
		},
		{
			name:     "a minimum above zero excludes it",
			schema:   `{"title":"Root","type":"object","properties":{"n":{"type":"integer","minimum":5}}}`,
			jsonName: "n",
			omit:     true,
			why:      "the numeric spelling of the same bound",
		},
		{
			name:     "a minimum of zero admits it",
			schema:   `{"title":"Root","type":"object","properties":{"n":{"type":"integer","minimum":0}}}`,
			jsonName: "n",
			omit:     false,
			why:      "the pair to the row above",
		},
		{
			name:     "an exclusive minimum of zero excludes it",
			schema:   `{"title":"Root","type":"object","properties":{"n":{"type":"integer","exclusiveMinimum":0}}}`,
			jsonName: "n",
			omit:     true,
			why:      "\"greater than zero\" is not \"zero\"",
		},
		{
			name:     "a maximum below zero excludes it",
			schema:   `{"title":"Root","type":"object","properties":{"n":{"type":"integer","maximum":-1}}}`,
			jsonName: "n",
			omit:     true,
			why:      "the bound from the other side",
		},
		{
			name:     "an unconstrained string keeps its zero",
			schema:   `{"title":"Root","type":"object","properties":{"s":{"type":"string"}}}`,
			jsonName: "s",
			omit:     false,
			why:      "zero-value invention is what the flag asks for, and this is the whole of it",
		},
		{
			name:     "an unconstrained integer keeps its zero",
			schema:   `{"title":"Root","type":"object","properties":{"n":{"type":"integer"}}}`,
			jsonName: "n",
			omit:     false,
			why:      "the same, for a number",
		},
		{
			name:     "an unconstrained boolean keeps its zero",
			schema:   `{"title":"Root","type":"object","properties":{"b":{"type":"boolean"}}}`,
			jsonName: "b",
			omit:     false,
			why:      "the same, for false -- the value nobody writes by accident",
		},
		{
			name:     "a nested object keeps the object its zero materializes",
			schema:   `{"title":"Root","type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"string"}}}}}`,
			jsonName: "a",
			omit:     false,
			why:      "the other half of the issue: {\"a\":{}} comes back with every member of a written out, and that is left alone",
		},
		{
			name:     "an allOf branch excluding the zero is read",
			schema:   `{"title":"Root","type":"object","properties":{"s":{"type":"string","allOf":[{"minLength":2}]}}}`,
			jsonName: "s",
			omit:     true,
			why:      "allOf is a conjunction: one branch excluding the zero excludes it",
		},
		{
			name:     "an anyOf excludes the zero only when every branch does",
			schema:   `{"title":"Root","type":"object","properties":{"s":{"type":"string","anyOf":[{"minLength":2},{"maxLength":0}]}}}`,
			jsonName: "s",
			omit:     false,
			why:      "the second branch admits the empty string, so the disjunction does",
		},
		{
			name:     "a $ref is followed to the definition that excludes the zero",
			schema:   `{"title":"Root","type":"object","properties":{"s":{"$ref":"#/$defs/Bounded"}},"$defs":{"Bounded":{"type":"string","minLength":2}}}`,
			jsonName: "s",
			omit:     true,
			why:      "a property behind a definition is the position schemaForbidsNull was once blind at",
		},
		{
			name:     "a required property is written whatever it holds",
			schema:   `{"title":"Root","type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}},"required":["tags"]}`,
			jsonName: "tags",
			omit:     false,
			why:      "omitting a required property is a different invalid document, not a repair",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := forbiddenZeroRoot(t, tc.schema, Config{PackageName: "testpkg", OmitEmpty: false})
			f, ok := fieldByJSONName(root, tc.jsonName)
			if !ok {
				t.Fatalf("no field for property %q in %#v", tc.jsonName, root.Fields)
			}
			if f.OmitZero != tc.omit {
				t.Fatalf("%q (%s): OmitZero = %v, want %v -- %s",
					tc.jsonName, f.Type.GoTypeName(), f.OmitZero, tc.omit, tc.why)
			}
		})
	}
}

// TestForbiddenZeroOmissionIsSpelledOutForAnUntaggableName covers the fields no
// struct tag can reach.
//
// A property name encoding/json's tag grammar cannot carry gets `json:"-"` and
// is written by hand, so ",omitzero" never reaches it and the omission has to be
// emitted as code. Which code depends on what the value has to be recognised by:
// a nil for the nilable shapes, and the bytes its zero marshals to for a named
// scalar or a wrapper struct, neither of which has a nil and only one of which
// is even comparable.
func TestForbiddenZeroOmissionIsSpelledOutForAnUntaggableName(t *testing.T) {
	tests := []struct {
		name     string
		schema   string
		wantOmit string
		wantZero string
	}{
		{
			name:     "a typed array is recognised by its nil",
			schema:   `{"title":"Root","type":"object","properties":{"a,b":{"type":"array","items":{"type":"string"}}}}`,
			wantOmit: "nil",
		},
		{
			name:     "a const is recognised by the bytes its zero writes",
			schema:   `{"title":"Root","type":"object","properties":{"a,b":{"const":"fixed"}}}`,
			wantOmit: "zerojson",
			wantZero: `""`,
		},
		{
			name:     "a constraint-only wrapper is too, having neither nil nor IsZero",
			schema:   `{"title":"Root","type":"object","properties":{"a,b":{"minLength":2}}}`,
			wantOmit: "zerojson",
			wantZero: `""`,
		},
		{
			name:     "a bounded number is recognised by the zero it writes",
			schema:   `{"title":"Root","type":"object","properties":{"a,b":{"type":"integer","minimum":5}}}`,
			wantOmit: "zerojson",
			wantZero: "0",
		},
		{
			name:     "a value the schema admits is still written unconditionally",
			schema:   `{"title":"Root","type":"object","properties":{"a,b":{"type":"string"}}}`,
			wantOmit: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := forbiddenZeroRoot(t, tc.schema, Config{PackageName: "testpkg", OmitEmpty: false})
			f, ok := fieldByJSONName(root, "a,b")
			if !ok {
				t.Fatalf("no field for property \"a,b\" in %#v", root.Fields)
			}
			if !f.ManualJSON {
				t.Fatalf("\"a,b\" is not written by hand, so this test is measuring nothing")
			}
			if f.ManualOmit != tc.wantOmit {
				t.Fatalf("ManualOmit = %q, want %q -- an absent value would be written as one the schema forbids",
					f.ManualOmit, tc.wantOmit)
			}
			if f.ZeroJSON != tc.wantZero {
				t.Fatalf("ZeroJSON = %q, want %q", f.ZeroJSON, tc.wantZero)
			}
		})
	}
}

// TestForbiddenNullIsOmittedWhereTheSchemaSaysSoUnderTheDefaultConfig is the
// half of #250 that has nothing to do with the flag.
//
// A type list naming "null" is the idiomatic spelling of nullable, and the
// omission is suppressed for one so that a nil is written back as the null the
// document had. But a schema may name null in its type list and then not list it
// among its admissible values, and then the null is not a value the property may
// hold at all: the suppression wrote one anyway, and the generated decoder --
// which reads the values rather than the type list -- refused the result.
//
// The pair below is the whole of the distinction: same type list, same
// nullability, and only the enum deciding whether the null survives.
// The rows that admit the null are omitted too, by the ",omitempty" the
// suppression takes away and the record that puts a present null back (issue
// #110) -- so what tells the two apart is which mechanism does it, and a field
// carrying neither is the defect.
func TestForbiddenNullIsOmittedWhereTheSchemaSaysSoUnderTheDefaultConfig(t *testing.T) {
	tests := []struct {
		name         string
		schema       string
		wantOmitZero bool
		why          string
	}{
		{
			name:         "the enum beside the type list does not list null",
			schema:       `{"title":"Root","type":"object","properties":{"e":{"type":["string","null"],"enum":["a",5]}}}`,
			wantOmitZero: true,
			why:          "null is not among the values, so writing one produces a document the decoder refuses",
		},
		{
			name:         "the enum beside the type list does list null",
			schema:       `{"title":"Root","type":"object","properties":{"e":{"type":["string","null"],"enum":["a",null]}}}`,
			wantOmitZero: false,
			why:          "the pair: here the null is a value the property may hold, so the field keeps the omission it had and the record writes the null back",
		},
		{
			name:         "an allOf branch names the values and no null among them",
			schema:       `{"title":"Root","type":"object","properties":{"e":{"type":["string","null"],"allOf":[{"enum":["a","b"]}]}}}`,
			wantOmitZero: true,
			why:          "the same disagreement reached through a conjunction",
		},
		{
			name:         "a plain nullable string keeps its null",
			schema:       `{"title":"Root","type":"object","properties":{"e":{"type":["string","null"]}}}`,
			wantOmitZero: false,
			why:          "the control that must not move: nothing here excludes the null",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := forbiddenZeroRoot(t, tc.schema, Config{PackageName: "testpkg", OmitEmpty: true})
			f, ok := fieldByJSONName(root, "e")
			if !ok {
				t.Fatalf("no field for property \"e\" in %#v", root.Fields)
			}
			if f.OmitZero != tc.wantOmitZero {
				t.Fatalf("%s: OmitZero = %v, want %v -- %s",
					f.Type.GoTypeName(), f.OmitZero, tc.wantOmitZero, tc.why)
			}
			// Whichever mechanism answers for it, no optional field may be
			// written unconditionally: that is what wrote the null.
			if !f.OmitZero && !f.OmitEmpty {
				t.Fatalf("%s carries neither ,omitzero nor ,omitempty, so an absent property is written as %s",
					f.Type.GoTypeName(), "null")
			}
		})
	}
}
