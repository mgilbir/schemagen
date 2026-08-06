package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// keywordPositionFixtures are two keywords that fired at one position and
// vanished at a sibling -- issues #206 and #207 -- together with the controls
// that tell a fix from an amputation.
//
// They are run compiled rather than compared against a golden for the reason the
// `not` and subschema-depth fixtures are: the symptom of each is a Validate that
// accepts, which reads exactly like a schema with nothing more to check. #207's
// generated source even looks correct -- a loop counting elements whose first
// byte is '{' -- and #206's differs from the fix by one character.
//
// Every document below turns on exactly one arm. #206's schemas pair `minimum: 5`
// with `exclusiveMinimum: true` and put 5 to them, a value `minimum` alone
// accepts, so only the exclusive bound can refuse it; 4 is there to show
// `minimum` survived. #207's positions are separate properties of one object, so
// a document naming one exercises that position and no other.
func keywordPositionFixtures() []notFixture {
	return []notFixture{
		{
			// Issue #207 in the shape the issue reports it: `contains` whose
			// sub-schema names an object by pattern. The gate deciding whether
			// the flat per-element checks say everything the sub-schema says was
			// a deny-list of struct fields, and patternProperties was not one of
			// them -- so what survived was `type: object` alone, and every object
			// counted.
			Name:       "contains_object_subschema",
			SchemaPath: "testdata/schemas/regression/contains_object_subschema.json",
			Instances: []notInstance{
				{Name: "matching key with the wrong value type", Doc: `[{"ab":"x"}]`, Valid: false,
					Why: "\"ab\" matches ^a and its value is a string, so no element matches the contains schema; accepting this is issue #207"},
				{Name: "matching key with the right value type", Doc: `[{"ab":1}]`, Valid: true,
					Why: "the element satisfies the sub-schema; a fix that rejects this is an amputation"},
				{Name: "no key the pattern names", Doc: `[{"b":"x"}]`, Valid: true,
					Why: "patternProperties says nothing about a key no pattern matches, so this object matches"},
				{Name: "one matching element among others", Doc: `[1,{"ab":2}]`, Valid: true,
					Why: "contains asks for one element, not all of them"},
				{Name: "no elements", Doc: `[]`, Valid: false,
					Why: "control: contains refuses the empty array whatever its sub-schema says"},
				{Name: "no object at all", Doc: `[1,"s"]`, Valid: false,
					Why: "control: the declared type still narrows which elements can match"},
			},
		},
		{
			// The sibling positions a `contains` is written at. #192 fixed the
			// array-shaped sub-schema at these same positions; this is the
			// object-shaped case it did not reach, and all four were broken.
			Name:       "contains_object_positions",
			SchemaPath: "testdata/schemas/regression/contains_object_positions.json",
			Instances: []notInstance{
				{Name: "property rejects", Doc: `{"atProperty":[{"ab":"x"}]}`, Valid: false,
					Why: "an array property's own contains"},
				{Name: "property accepts", Doc: `{"atProperty":[{"ab":1}]}`, Valid: true, Why: "control for the above"},

				{Name: "element rejects", Doc: `{"atElement":[[{"ab":"x"}]]}`, Valid: false,
					Why: "a contains inside an items sub-schema, the position #192 reached for arrays"},
				{Name: "element accepts", Doc: `{"atElement":[[{"ab":1}]]}`, Valid: true, Why: "control for the above"},

				{Name: "map value rejects", Doc: `{"atMapValue":{"k":[{"ab":"x"}]}}`, Valid: false,
					Why: "a contains inside a schema-valued additionalProperties"},
				{Name: "map value accepts", Doc: `{"atMapValue":{"k":[{"ab":1}]}}`, Valid: true, Why: "control for the above"},

				{Name: "tuple slot rejects", Doc: `{"atTupleSlot":[[{"ab":"x"}]]}`, Valid: false,
					Why: "a contains inside a prefixItems slot"},
				{Name: "tuple slot accepts", Doc: `{"atTupleSlot":[[{"ab":1}]]}`, Valid: true, Why: "control for the above"},

				{Name: "required rejects", Doc: `{"byRequired":[{"b":1}]}`, Valid: false,
					Why: "control: an object shape named by `required` was already delegated and must stay so"},
				{Name: "required accepts", Doc: `{"byRequired":[{"a":1}]}`, Valid: true, Why: "control for the above"},

				{Name: "scalar bound rejects", Doc: `{"byScalarBound":[1]}`, Valid: false,
					Why: "control: a sub-schema the flat checks do say everything about must keep them"},
				{Name: "scalar bound accepts", Doc: `{"byScalarBound":[7]}`, Valid: true, Why: "control for the above"},

				{Name: "annotated scalar bound rejects", Doc: `{"byDescribedBound":[1]}`, Valid: false,
					Why: "control: a description constrains nothing, so the sub-schema is still one the flat checks cover; reading the keyword set naively would route this to a materialized type"},
				{Name: "annotated scalar bound accepts", Doc: `{"byDescribedBound":[7]}`, Valid: true, Why: "control for the above"},

				{Name: "nothing present", Doc: `{}`, Valid: true,
					Why: "every property is optional and none is present"},
			},
		},
		{
			// Issue #206. The boolean spelling of the exclusive bounds is what
			// draft 3 and draft 4 write, and the patternProperties value path
			// read only the numeric one -- so the spelling the dialect does
			// honour landed on neither arm and the bound was dropped.
			//
			// Which dialects honour which spelling is a separate question and a
			// separate issue (#203). Every case here is a draft-4 document using
			// draft 4's own spelling.
			Name:       "exclusive_bound_boolean_draft4",
			SchemaPath: "testdata/schemas/regression/exclusive_bound_boolean_draft4.json",
			Instances: []notInstance{
				{Name: "root pattern rejects the bound itself", Doc: `{"p":5}`, Valid: false,
					Why: "exclusiveMinimum makes 5 the one value minimum 5 excludes; accepting it is issue #206"},
				{Name: "root pattern accepts above the bound", Doc: `{"p":6}`, Valid: true,
					Why: "a fix that rejects this is an amputation"},
				{Name: "root pattern still enforces the minimum", Doc: `{"p":4}`, Valid: false,
					Why: "the `minimum` the boolean modifies must survive"},

				{Name: "property rejects the bound itself", Doc: `{"atProperty":5}`, Valid: false,
					Why: "control: a declared property already read the boolean spelling and must go on doing so"},
				{Name: "property accepts above the bound", Doc: `{"atProperty":6}`, Valid: true, Why: "control for the above"},

				{Name: "nested pattern rejects the bound itself", Doc: `{"nestedMin":{"x":5}}`, Valid: false,
					Why: "the same pattern position one object down"},
				{Name: "nested pattern accepts above the bound", Doc: `{"nestedMin":{"x":6}}`, Valid: true, Why: "control for the above"},
				{Name: "nested pattern still enforces the minimum", Doc: `{"nestedMin":{"x":4}}`, Valid: false, Why: "control for the above"},

				{Name: "exclusiveMaximum rejects the bound itself", Doc: `{"nestedMax":{"y":5}}`, Valid: false,
					Why: "the upper bound has the same two spellings and the same defect"},
				{Name: "exclusiveMaximum accepts below the bound", Doc: `{"nestedMax":{"y":4}}`, Valid: true, Why: "control for the above"},
				{Name: "exclusiveMaximum still enforces the maximum", Doc: `{"nestedMax":{"y":6}}`, Valid: false, Why: "control for the above"},

				{Name: "exclusiveMinimum false leaves the bound inclusive", Doc: `{"notExclusive":{"z":5}}`, Valid: true,
					Why: "control: `false` says the bound is inclusive, so a fix reading the keyword's presence rather than its value would reject this"},
				{Name: "exclusiveMinimum false still enforces the minimum", Doc: `{"notExclusive":{"z":4}}`, Valid: false, Why: "control for the above"},

				{Name: "nothing present", Doc: `{}`, Valid: true, Why: "every property is optional"},
			},
		},
		{
			// The same keyword on draft 3, which spells it the same way. A
			// separate document because the dialect is a property of the
			// document and cannot be varied within one.
			Name:       "exclusive_bound_boolean_draft3",
			SchemaPath: "testdata/schemas/regression/exclusive_bound_boolean_draft3.json",
			Instances: []notInstance{
				{Name: "rejects the bound itself", Doc: `{"x":5}`, Valid: false,
					Why: "draft 3 spells the exclusive bound as a boolean beside `minimum`, exactly as draft 4 does"},
				{Name: "accepts above the bound", Doc: `{"x":6}`, Valid: true, Why: "control for the above"},
				{Name: "still enforces the minimum", Doc: `{"x":4}`, Valid: false, Why: "control for the above"},
			},
		},
		{
			// The other caller of the same extractor: a schema with properties
			// and no declared object type accepts non-object documents, and
			// checks them against the same pp* rules. The bound was dropped here
			// too, and by the same line.
			Name:       "exclusive_bound_boolean_nonobject",
			SchemaPath: "testdata/schemas/regression/exclusive_bound_boolean_nonobject.json",
			Instances: []notInstance{
				{Name: "rejects the bound itself", Doc: `5`, Valid: false,
					Why: "the document is a number, so the schema's own numeric bounds are what judge it"},
				{Name: "accepts above the bound", Doc: `6`, Valid: true, Why: "control for the above"},
				{Name: "still enforces the minimum", Doc: `4`, Valid: false, Why: "control for the above"},
				{Name: "an object is judged as an object", Doc: `{"a":"x"}`, Valid: true,
					Why: "control: the numeric bounds say nothing about the object shape the schema also describes"},
			},
		},
	}
}

// TestKeywordsFireAtEveryPositionTheyAreWrittenAt runs the fixtures above
// compiled, in the harness TestNotIsEnforcedAndDoesNotOverNegate uses.
func TestKeywordsFireAtEveryPositionTheyAreWrittenAt(t *testing.T) {
	for _, fx := range keywordPositionFixtures() {
		t.Run(fx.Name, func(t *testing.T) {
			generated := generateFromSchemaWithConfig(t, fx.SchemaPath, generator.Config{
				PackageName:  "testpkg",
				OmitEmpty:    true,
				RootTypeName: "Root",
			})

			tmpDir := t.TempDir()
			generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
			if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
				t.Fatalf("writing types.go: %v", err)
			}
			writeSharedHelpers(t, tmpDir, generatedMain)

			mainGo, err := notInstanceMain("Root", fx.Instances)
			if err != nil {
				t.Fatalf("building main.go: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
				t.Fatalf("writing main.go: %v", err)
			}
			if err := writeTestGoMod(tmpDir, "keyword_position_test"); err != nil {
				t.Fatalf("writing go.mod: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", ".")
			cmd.Dir = tmpDir
			out, runErr := cmd.CombinedOutput()
			text := programOutput(out)
			if runErr != nil || text != "PASS" {
				t.Fatalf("%s:\n%s", fx.SchemaPath, text)
			}
		})
	}
}
