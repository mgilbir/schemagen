package tests

import (
	"fmt"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// A property no value satisfies -- `false`, `{"enum":[]}`, `{"not":{}}` -- is
// refused by the key being there at all, and the check for it is emitted from
// the struct's own Validate.
//
// Under the default configuration every optional property is pointer-wrapped, so
// that check could be written as `field != nil` and was. Under
// --omit-empty=false the same property is a plain `string`, and nothing may be
// compared to nil but a pointer, a slice, a map or an interface: the emitted
// package did not build at all.
//
//	./empty_enum_positions.go:188:17: invalid operation: e.Typed != nil
//	(mismatched types string and untyped nil)
//
// Which is why this is run under both configurations against the same documents
// and the same expectations. The flag decides how a property is held; it decides
// nothing about which documents the schema admits, and a check that changes its
// verdict with it would be a defect of the same family as the one that could not
// compile.
//
// The generator already had the shape of this problem written down twice, in the
// two places a forbidden rule is dropped for a wrapper type -- "the rule is
// emitted as `field != nil` and a wrapper struct is not nilable, so it does not
// compile there". The type-level answer is to stop assuming; see
// ValidationRule.FieldNilable.
const forbiddenPropertySchema = "testdata/schemas/regression/empty_enum_positions.json"

// forbiddenPropertyDocs pairs a document with whether the generated type must
// accept it.
//
// A refusal from the decoder counts the same as one from Validate. Which of the
// two speaks is a property of the schema -- `{"type":"string","enum":[]}`
// excludes null, so UnmarshalJSON refuses `null` before Validate is reached --
// and pinning it here would be pinning an implementation detail in a test about
// a verdict. What must not happen is acceptance.
var forbiddenPropertyDocs = []struct {
	Name     string
	Doc      string
	Accepted bool
	Why      string
}{
	{
		Name: "no forbidden key",
		Doc:  `{}`,
		Why:  "the control: none of the forbidden properties is present, so nothing is violated",

		Accepted: true,
	},
	{
		Name: "a typed forbidden property carrying a value",
		Doc:  `{"typed":"x"}`,
		Why:  "{'type':'string','enum':[]} -- the property that is a plain string under --omit-empty=false, and the one the emitted `!= nil` did not compile against",
	},
	{
		Name: "a typed forbidden property carrying a null",
		Doc:  `{"typed":null}`,
		Why:  "the key is present, which is the whole violation; a nil test cannot see this and reported the document as valid (issue #127)",
	},
	{
		Name: "an untyped forbidden property carrying a value",
		Doc:  `{"inline":1}`,
		Why:  "{'enum':[]} with no type -- held as `any`, so this arm stayed nilable under both configurations and is the control for the one that did not",
	},
	{
		Name: "an untyped forbidden property carrying a null",
		Doc:  `{"inline":null}`,
		Why:  "a null into `any` leaves exactly what an absent property leaves, so only the document's own keys can tell them apart",
	},
	{
		Name: "a forbidden property reached through a $ref",
		Doc:  `{"viaRef":"x"}`,
		Why:  "the $ref materializes a forbidding type of its own, whose Validate is dispatched to instead of a field rule",
	},
	{
		Name: "a permitted enum member",
		Doc:  `{"populated":"a"}`,
		Why:  "the second control, and the one that matters: a fix that refused every enum would satisfy every case above and mean nothing",

		Accepted: true,
	},
	{
		Name: "a value outside a non-empty enum",
		Doc:  `{"populated":"z"}`,
		Why:  "and the enum beside them is still enforced",
	},
}

// TestForbiddenPropertyIsRefusedWithoutOmitEmpty is the configuration the
// generated package did not compile under.
func TestForbiddenPropertyIsRefusedWithoutOmitEmpty(t *testing.T) {
	runGeneratedMainProgramWithConfig(t,
		forbiddenPropertySchema,
		"forbidden_property_noomit_test",
		forbiddenPropertyProgram(),
		generator.Config{PackageName: "testpkg", OmitEmpty: false},
	)
}

// TestForbiddenPropertyIsRefusedUnderDefaultConfig runs the same documents
// through the configuration that pointer-wraps, which is the verdict the one
// above has to agree with.
func TestForbiddenPropertyIsRefusedUnderDefaultConfig(t *testing.T) {
	runGeneratedMainProgramWithConfig(t,
		forbiddenPropertySchema,
		"forbidden_property_default_test",
		forbiddenPropertyProgram(),
		generator.Config{PackageName: "testpkg", OmitEmpty: true},
	)
}

// forbiddenPropertyProgram writes a main() that puts every document through the
// generated type and reports the verdict it got.
func forbiddenPropertyProgram() string {
	body := ""
	for _, d := range forbiddenPropertyDocs {
		body += fmt.Sprintf("\t\t{%q, %s, %t},\n", d.Name, backquote(d.Doc), d.Accepted)
	}
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	cases := []struct {
		name     string
		doc      string
		accepted bool
	}{
%s	}

	for _, c := range cases {
		var v EmptyEnumPositions
		err := json.Unmarshal([]byte(c.doc), &v)
		if err == nil {
			err = v.Validate()
		}
		switch {
		case c.accepted && err != nil:
			fail("%%s: %%s was refused, and the schema admits it: %%v", c.name, c.doc, err)
		case !c.accepted && err == nil:
			fail("%%s: %%s was accepted, and no value satisfies that property", c.name, c.doc)
		}
	}

	fmt.Println("PASS")
}

func fail(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
	os.Exit(1)
}
`, body)
}
