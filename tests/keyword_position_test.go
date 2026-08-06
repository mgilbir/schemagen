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

// keywordPositionFixtures are the keywords that fired at one position and
// vanished at a sibling, together with the controls that tell a fix from an
// amputation.
//
// They are run compiled rather than compared against a golden for the reason the
// `not` and subschema-depth fixtures are: the symptom of each is a Validate that
// accepts, which reads exactly like a schema with nothing more to check, and
// #206's generated source differs from the fix by one character.
//
// Every document below turns on exactly one arm. #206's schemas pair `minimum: 5`
// with `exclusiveMinimum: true` and put 5 to them, a value `minimum` alone
// accepts, so only the exclusive bound can refuse it; 4 is there to show
// `minimum` survived.
func keywordPositionFixtures() []notFixture {
	return []notFixture{
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
