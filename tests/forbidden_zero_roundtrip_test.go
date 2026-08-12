package tests

import (
	"fmt"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// Issue #250: under --omit-empty=false the generated type wrote documents it
// could not read back.
//
// Every optional field is written unconditionally there, so what goes into the
// document is whatever the decoder left behind -- and for a nil pointer, slice,
// map or interface that is a null, which a typed property forbids. {"n":1} came
// back as {"n":1,"tags":null} and the type's own UnmarshalJSON then refused it.
// The same break with no null in it is a zero the schema excludes by value: a
// const property wrote "" where only "fixed" is admissible, and Validate refused
// the result a step later.
//
// The property proved here is round-trip closure, which is what caught the
// defect: marshalling a decoded value must produce a document that decodes
// again, validates, and marshals to the same bytes. Nothing weaker would have
// seen it -- the output was well-formed JSON, it just was not an instance of the
// schema it came from.
//
// The two halves of the issue are separable and only one of them is fixed, so
// the expectations below are exact strings rather than a closure check alone.
// The zeros this schema *permits* are still written: "" for a string, 0 for a
// number, false for a boolean, a materialized object for a nested one, and null
// for a property whose type list admits it. That is zero-value invention, it is
// what --omit-empty=false asks for, and a fix reaching it would show up here as
// a shorter document.
const forbiddenZeroSchema = "testdata/schemas/regression/forbidden_zero_roundtrip.json"

// forbiddenZeroCases pairs a document with what marshalling its decoded form
// must produce. json.Marshal writes an object's keys in sorted order, and the
// generated MarshalJSON hands its overflow map to json.Marshal, so the wanted
// text is exact rather than compared semantically.
type forbiddenZeroCase struct {
	Name string
	Doc  string
	Want string
}

// forbiddenZeroDocs are the documents both configurations are run over. The
// document text is shared; only the expected output differs between them.
var forbiddenZeroDocs = []struct {
	Name        string
	Doc         string
	WantOmit    string // --omit-empty (the default)
	WantNoOmit  string // --omit-empty=false
	WhyItIsHere string
}{
	{
		Name: "the empty document",
		Doc:  `{}`,
		// Nothing is present, so nothing is written.
		WantOmit: `{}`,
		// Every property whose zero this schema admits is written; every
		// property whose zero it forbids is not. Neither "tags":null (an array
		// that cannot be null) nor "constant":"" (a const of "fixed") is here,
		// and both were before.
		WantNoOmit:  `{"anything":null,"b":false,"i":0,"nested":{"deeper":{"leaf":""}},"nullableComposition":null,"nullableList":null,"nullableString":null,"s":""}`,
		WhyItIsHere: "the reported shape: a document that carries none of the optional properties",
	},
	{
		Name: "present but empty collections",
		Doc:  `{"tags":[],"labels":{},"nullableList":[]}`,
		// A present [] and {} are not absences, and omitting them would be the
		// same round-trip break in the other direction.
		WantOmit:    `{"labels":{},"nullableList":[],"tags":[]}`,
		WantNoOmit:  `{"anything":null,"b":false,"i":0,"labels":{},"nested":{"deeper":{"leaf":""}},"nullableComposition":null,"nullableList":[],"nullableString":null,"s":"","tags":[]}`,
		WhyItIsHere: "the control for the omission: what is skipped is the nil, not the empty",
	},
	{
		Name: "explicit nulls where the schema admits one",
		Doc:  `{"nullableString":null,"nullableList":null,"nullableComposition":null,"anything":null}`,
		// A nullable property must still be able to hold and emit null. The
		// decoded value has no state for it -- a null and an absent property
		// leave the same nil -- so it is written back from the record
		// UnmarshalJSON keeps.
		WantOmit:    `{"anything":null,"nullableComposition":null,"nullableList":null,"nullableString":null}`,
		WantNoOmit:  `{"anything":null,"b":false,"i":0,"nested":{"deeper":{"leaf":""}},"nullableComposition":null,"nullableList":null,"nullableString":null,"s":""}`,
		WhyItIsHere: "a null the schema permits is a value, and omitting it would lose what the document said",
	},
	{
		Name: "every property carrying a legal value",
		Doc:  `{"tags":["x"],"constant":"fixed","choice":"a","counted":1,"atLeastTwo":"ok","lowercase":"abc","positive":7,"a,b":["q"],"c,d":"fixed","boundedTags":["t"],"boundedLabels":{"k":"v"},"labels":{"k":"v"},"entries":[{"k":"v"}],"refList":["a","b"],"slot":["s",1],"child":{},"nullableString":"x","s":"v","i":3,"b":true,"anything":5,"nested":{"deeper":{"leaf":"l"}}}`,
		// The acceptance control. An omission that fired on a value the document
		// carried would be an amputation rather than a fix, and every property
		// this schema declares is here to catch one.
		WantOmit:    `{"a,b":["q"],"anything":5,"atLeastTwo":"ok","b":true,"boundedLabels":{"k":"v"},"boundedTags":["t"],"c,d":"fixed","child":{},"choice":"a","constant":"fixed","counted":1,"entries":[{"k":"v"}],"i":3,"labels":{"k":"v"},"lowercase":"abc","nested":{"deeper":{"leaf":"l"}},"nullableString":"x","positive":7,"refList":["a","b"],"s":"v","slot":["s",1],"tags":["x"]}`,
		WantNoOmit:  `{"a,b":["q"],"anything":5,"atLeastTwo":"ok","b":true,"boundedLabels":{"k":"v"},"boundedTags":["t"],"c,d":"fixed","child":{"anything":null,"b":false,"i":0,"nested":{"deeper":{"leaf":""}},"nullableComposition":null,"nullableList":null,"nullableString":null,"s":""},"choice":"a","constant":"fixed","counted":1,"entries":[{"k":"v"}],"i":3,"labels":{"k":"v"},"lowercase":"abc","nested":{"deeper":{"leaf":"l"}},"nullableComposition":null,"nullableList":null,"nullableString":"x","positive":7,"refList":["a","b"],"s":"v","slot":["s",1],"tags":["x"]}`,
		WhyItIsHere: "the acceptance control: nothing the document carried may be dropped",
	},
}

// TestForbiddenZeroRoundTripsWithoutOmitEmpty is issue #250 in the
// configuration it was reported in.
func TestForbiddenZeroRoundTripsWithoutOmitEmpty(t *testing.T) {
	cases := make([]forbiddenZeroCase, 0, len(forbiddenZeroDocs))
	for _, d := range forbiddenZeroDocs {
		cases = append(cases, forbiddenZeroCase{Name: d.Name, Doc: d.Doc, Want: d.WantNoOmit})
	}
	runGeneratedMainProgramWithConfig(t,
		forbiddenZeroSchema,
		"forbidden_zero_noomit_test",
		forbiddenZeroProgram(cases),
		generator.Config{PackageName: "testpkg", OmitEmpty: false},
	)
}

// TestForbiddenZeroRoundTripsUnderDefaultConfig runs the same documents through
// the configuration that omits empties.
//
// It is not a formality. Two properties of this schema state a type list that
// admits a null and a set of values that does not -- {"type":["string","null"],
// "enum":["a",5]}, and the same disagreement spelled through an allOf branch --
// and for those the omission is suppressed under every configuration, on the
// grounds that the schema is nullable. It is not: null is not among the values
// either of them admits, so the nil that was written back as a null produced a
// document the decoder refused, with omitempty on and no flag involved. The
// empty document below did not round-trip under the default configuration
// either.
func TestForbiddenZeroRoundTripsUnderDefaultConfig(t *testing.T) {
	cases := make([]forbiddenZeroCase, 0, len(forbiddenZeroDocs))
	for _, d := range forbiddenZeroDocs {
		cases = append(cases, forbiddenZeroCase{Name: d.Name, Doc: d.Doc, Want: d.WantOmit})
	}
	runGeneratedMainProgramWithConfig(t,
		forbiddenZeroSchema,
		"forbidden_zero_default_test",
		forbiddenZeroProgram(cases),
		generator.Config{PackageName: "testpkg", OmitEmpty: true},
	)
}

// forbiddenZeroProgram writes a main() that closes the loop on every case: the
// document is decoded, marshalled, compared against the wanted text, then
// decoded again, validated, and marshalled once more.
//
// The second decode is the half the issue was reported as -- the type could not
// read back what it wrote -- and Validate is the half that caught the const
// property, whose output decoded perfectly well and was not an instance of the
// schema. The second marshal pins the two spellings together: a fix that made
// the first pass and the second differ would leave marshalling non-idempotent.
func forbiddenZeroProgram(cases []forbiddenZeroCase) string {
	body := ""
	for _, c := range cases {
		body += fmt.Sprintf("\t\t{%q, %s, %s},\n", c.Name, backquote(c.Doc), backquote(c.Want))
	}
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	cases := []struct{ name, doc, want string }{
%s	}

	for _, c := range cases {
		var first ForbiddenZeroRoundTrip
		if err := json.Unmarshal([]byte(c.doc), &first); err != nil {
			fail("%%s: the document did not decode: %%v", c.name, err)
		}
		out, err := json.Marshal(first)
		if err != nil {
			fail("%%s: marshal: %%v", c.name, err)
		}
		if string(out) != c.want {
			fail("%%s: marshalled\n  %%s\nwant\n  %%s", c.name, string(out), c.want)
		}

		// The type must be able to read back what it just wrote.
		var second ForbiddenZeroRoundTrip
		if err := json.Unmarshal(out, &second); err != nil {
			fail("%%s: the output does not decode: %%s\n  %%v", c.name, string(out), err)
		}
		if err := second.Validate(); err != nil {
			fail("%%s: the output is not an instance of the schema: %%s\n  %%v", c.name, string(out), err)
		}
		again, err := json.Marshal(second)
		if err != nil {
			fail("%%s: re-marshal: %%v", c.name, err)
		}
		if string(again) != string(out) {
			fail("%%s: marshalling is not idempotent:\n  %%s\n  %%s", c.name, string(out), string(again))
		}
	}

	fmt.Println("PASS")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
`, body)
}

// backquote renders a JSON document as a Go raw string literal. No document in
// this file contains a backquote, and one arriving later must not be pasted into
// source that would no longer compile.
func backquote(s string) string {
	for _, r := range s {
		if r == '`' {
			panic("forbidden-zero case contains a backquote: " + s)
		}
	}
	return "`" + s + "`"
}
