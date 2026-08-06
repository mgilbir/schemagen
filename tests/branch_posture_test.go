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

// branchPostureFixtures are issue #205: `format` and the content vocabulary lose
// their assertion at every branch position, on exactly the dialects that assert
// them.
//
// Two things have to be true of every group here, and the second is why they are
// written in pairs.
//
//   - On a dialect that asserts, the document the keyword forbids must be
//     refused. `format` asserts on drafts 3, 4, 6 and 7 and on v1; the content
//     vocabulary asserts on draft 7 alone.
//   - On a dialect that annotates, the same document must go on being accepted.
//     2019-09 and 2020-12 declare the format-annotation vocabulary and make the
//     content keywords annotation-only, so refusing there would be a new false
//     reject -- and those cells were already correct before this fix, which is
//     precisely why a sweep run only against 2020-12 could not see the defect.
//
// So a fixture is judged against its own dialect's posture rather than against
// 2020-12's, and the annotating twins are not padding: they are the half of the
// property that a fix is most likely to break.
//
// They are run compiled rather than compared against a golden, for the reason
// the `not` and subschema-depth groups are: the symptom is a Validate that
// accepts, which reads exactly like a schema with nothing to check. Several of
// these positions emitted no check at all and no warning either, so there was
// nothing in the source to compare.
func branchPostureFixtures() []notFixture {
	const (
		badEmail  = `"2962"`
		goodEmail = `"a@b.com"`
		// Not base64 at all: '%' is outside RFC 4648's alphabet. This is the
		// suite's own string for the case, and the one issue #205 quotes.
		badB64  = `"eyJmb28iOi%iYmFyIn0K"`
		goodB64 = `"eyJmb28iOiJiYXIifQo="`
	)
	return []notFixture{
		{
			Name:       "format_then_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_then_draft7.json",
			Instances: []notInstance{
				{Name: "branch taken, bad format", Doc: `{"t":1,"p":` + badEmail + `}`, Valid: false,
					Why: "the `then` branch applies and demands an e-mail address; accepting this is issue #205's first repro"},
				{Name: "branch taken, good format", Doc: `{"t":1,"p":` + goodEmail + `}`, Valid: true,
					Why: "the branch is satisfied; a fix that rejects this is an amputation"},
				{Name: "branch not taken", Doc: `{"t":2,"p":` + badEmail + `}`, Valid: true,
					Why: "the condition does not hold, so `then` says nothing about this document"},
				{Name: "branch taken, property absent", Doc: `{"t":1}`, Valid: false,
					Why: "control: the `required` the branch always enforced must survive the rerouting"},
			},
		},
		{
			Name:       "format_then_2020_annotates",
			SchemaPath: "testdata/schemas/regression/branch_format_then_2020.json",
			Instances: []notInstance{
				{Name: "branch taken, non-conforming string", Doc: `{"t":1,"p":` + badEmail + `}`, Valid: true,
					Why: "2020-12 declares format-annotation, so the keyword asserts nothing and this document is valid; refusing it is a false reject"},
				{Name: "branch taken, conforming string", Doc: `{"t":1,"p":` + goodEmail + `}`, Valid: true,
					Why: "control for the above"},
				{Name: "branch taken, property absent", Doc: `{"t":1}`, Valid: false,
					Why: "the posture governs `format` and nothing else: `required` still binds"},
			},
		},
		{
			Name:       "format_else_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_else_draft7.json",
			Instances: []notInstance{
				{Name: "else taken, bad format", Doc: `{"t":2,"p":` + badEmail + `}`, Valid: false,
					Why: "the condition fails, so `else` applies and demands an e-mail address"},
				{Name: "else taken, good format", Doc: `{"t":2,"p":` + goodEmail + `}`, Valid: true,
					Why: "control for the above"},
				{Name: "else not taken", Doc: `{"t":1}`, Valid: true,
					Why: "the condition holds, and there is no `then`, so nothing applies"},
			},
		},
		{
			Name:       "format_anyof_draft6_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_anyof_draft6.json",
			Instances: []notInstance{
				{Name: "bad format", Doc: `{"p":` + badEmail + `}`, Valid: false,
					Why: "the one branch demands an e-mail address, and a one-branch anyOf is satisfied only by satisfying it"},
				{Name: "good format", Doc: `{"p":` + goodEmail + `}`, Valid: true, Why: "control for the above"},
				{Name: "non-string", Doc: `{"p":5}`, Valid: false,
					Why: "control: the branch's own `type` must not be lost along with the format"},
			},
		},
		{
			Name:       "format_anyof_2020_annotates",
			SchemaPath: "testdata/schemas/regression/branch_format_anyof_2020.json",
			Instances: []notInstance{
				{Name: "non-conforming string", Doc: `{"p":` + badEmail + `}`, Valid: true,
					Why: "the format annotates here, so every string satisfies the branch"},
				{Name: "non-string", Doc: `{"p":5}`, Valid: false,
					Why: "the branch's `type` binds whatever the format's posture is"},
			},
		},
		{
			Name:       "format_contains_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_contains_draft7.json",
			Instances: []notInstance{
				{Name: "no element matches", Doc: `[` + badEmail + `]`, Valid: false,
					Why: "the sub-schema states nothing but a format, and `contains` needs an element that satisfies it"},
				{Name: "an element matches", Doc: `[` + goodEmail + `]`, Valid: true, Why: "control for the above"},
				{Name: "empty array", Doc: `[]`, Valid: false,
					Why: "control: `contains` demands a match, so the empty array fails whatever the sub-schema says. A fix that dropped the keyword instead of reading it would accept this"},
				{Name: "a non-string element", Doc: `[5]`, Valid: true,
					Why: "`format` says nothing about a value that is not a string, so the number satisfies the sub-schema and the array contains a match"},
			},
		},
		{
			Name:       "content_contains_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_content_contains_draft7.json",
			Instances: []notInstance{
				{Name: "no element decodes", Doc: `[` + badB64 + `]`, Valid: false,
					Why: "issue #205's third repro: the string is not base64, so no element matches"},
				{Name: "an element decodes", Doc: `[` + goodB64 + `]`, Valid: true, Why: "control for the above"},
				{Name: "empty array", Doc: `[]`, Valid: false, Why: "control, as above"},
				{Name: "a non-string element", Doc: `[5]`, Valid: true,
					Why: "the content keywords speak about strings alone, so a number satisfies the sub-schema"},
			},
		},
		{
			Name:       "content_contains_2020_annotates",
			SchemaPath: "testdata/schemas/regression/branch_content_contains_2020.json",
			Instances: []notInstance{
				{Name: "a string that is not base64", Doc: `[` + badB64 + `]`, Valid: true,
					Why: "from 2019-09 the content vocabulary is annotation-only, so every string matches; the suite marks this document valid"},
				{Name: "empty array", Doc: `[]`, Valid: false,
					Why: "`contains` still demands an element, and an annotating sub-schema is satisfied by any of them"},
			},
		},
		{
			Name:       "content_propertynames_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_content_propertynames_draft7.json",
			Instances: []notInstance{
				{Name: "a key that is not base64", Doc: `{` + badB64 + `:1}`, Valid: false,
					Why: "every property name must satisfy the sub-schema, and this one does not decode"},
				{Name: "a key that is base64", Doc: `{` + goodB64 + `:1}`, Valid: true, Why: "control for the above"},
				{Name: "no keys", Doc: `{}`, Valid: true,
					Why: "propertyNames says nothing about an object with no keys"},
			},
		},
		{
			Name:       "format_dependentschemas_v1_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_dependentschemas_v1.json",
			Instances: []notInstance{
				{Name: "trigger present, bad format", Doc: `{"t":1,"p":` + badEmail + `}`, Valid: false,
					Why: "issue #205's second repro: v1 asserts `format`, so the dependent branch demands an address"},
				{Name: "trigger present, good format", Doc: `{"t":1,"p":` + goodEmail + `}`, Valid: true, Why: "control for the above"},
				{Name: "no trigger", Doc: `{"p":` + badEmail + `}`, Valid: true,
					Why: "a dependent branch applies only when its trigger key is present"},
				{Name: "trigger present, property absent", Doc: `{"t":1}`, Valid: false,
					Why: "control: the branch's `required` must survive"},
			},
		},
		{
			Name:       "format_dependentschemas_2019_annotates",
			SchemaPath: "testdata/schemas/regression/branch_format_dependentschemas_2019.json",
			Instances: []notInstance{
				{Name: "trigger present, non-conforming string", Doc: `{"t":1,"p":` + badEmail + `}`, Valid: true,
					Why: "2019-09 annotates, and this is the cell whose v1 twin refuses the same document"},
				{Name: "trigger present, property absent", Doc: `{"t":1}`, Valid: false,
					Why: "`required` binds on either posture"},
			},
		},
		{
			Name:       "format_dependencies_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_dependencies_draft7.json",
			Instances: []notInstance{
				{Name: "trigger present, bad format", Doc: `{"t":1,"p":` + badEmail + `}`, Valid: false,
					Why: "the schema form of `dependencies` is the same branch under an older spelling"},
				{Name: "trigger present, good format", Doc: `{"t":1,"p":` + goodEmail + `}`, Valid: true, Why: "control for the above"},
				{Name: "no trigger", Doc: `{}`, Valid: true, Why: "no trigger, no branch"},
			},
		},
		{
			Name:       "format_unevaluatedprops_v1_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_unevaluatedprops_v1.json",
			Instances: []notInstance{
				{Name: "unevaluated key, bad format", Doc: `{"b":` + badEmail + `}`, Valid: false,
					Why: "\"b\" is evaluated by nothing else, so unevaluatedProperties judges it and demands an address"},
				{Name: "unevaluated key, good format", Doc: `{"b":` + goodEmail + `}`, Valid: true, Why: "control for the above"},
				{Name: "an evaluated key", Doc: `{"a":1}`, Valid: true,
					Why: "\"a\" is evaluated by `properties`, so the unevaluated sub-schema never sees it"},
			},
		},
		{
			Name:       "format_unevaluateditems_v1_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_unevaluateditems_v1.json",
			Instances: []notInstance{
				{Name: "unevaluated item, bad format", Doc: `[1,` + badEmail + `]`, Valid: false,
					Why: "position 1 is past the prefix, so unevaluatedItems judges it"},
				{Name: "unevaluated item, good format", Doc: `[1,` + goodEmail + `]`, Valid: true, Why: "control for the above"},
				{Name: "prefix only", Doc: `[1]`, Valid: true,
					Why: "every position is evaluated by the prefix, so there is nothing left over"},
			},
		},
		{
			// A condition every object satisfies, so `then` is unconditional.
			// Nothing flattened it, so it was enforced by neither route.
			Name:       "format_vacuous_if_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_vacuous_if_draft7.json",
			Instances: []notInstance{
				{Name: "bad format", Doc: `{"p":` + badEmail + `}`, Valid: false,
					Why: "`if: {}` matches every object, so `then` applies to this one"},
				{Name: "good format", Doc: `{"p":` + goodEmail + `}`, Valid: true,
					Why: "control for the above"},
				{Name: "property absent", Doc: `{}`, Valid: false,
					Why: "the branch requires \"p\" of every document, and the empty object is the shortest thing it refuses"},
			},
		},
		{
			// A format the dialect asserts and this generator cannot judge. Every
			// draft says to ignore an unrecognised format, so the sub-schema
			// admits everything -- and the empty array is what tells that apart
			// from the keyword being dropped, which admits everything too.
			Name:       "unjudgeable_format_contains_draft7",
			SchemaPath: "testdata/schemas/regression/branch_unjudgeable_format_contains_draft7.json",
			Instances: []notInstance{
				{Name: "empty array", Doc: `[]`, Valid: false,
					Why: "`contains` demands a matching element and there is none; accepting this is the keyword dropped rather than read"},
				{Name: "a string element", Doc: `["x"]`, Valid: true,
					Why: "an unrecognised format constrains nothing, so the element matches"},
				{Name: "a non-string element", Doc: `[1]`, Valid: true,
					Why: "control for the above, one JSON type over"},
			},
		},
		{
			// The condition, rather than the consequence: an `if` the static
			// reading cannot express drops the whole group, so `then` asserts
			// nothing. Without this case the arm of conditionalReadWhole that
			// answers for an unreadable condition is never taken, and could be
			// deleted with every other test still passing.
			Name:       "format_inexpressible_if_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_inexpressible_if_draft7.json",
			Instances: []notInstance{
				{Name: "condition holds, bad format", Doc: `{"a":1,"p":` + badEmail + `}`, Valid: false,
					Why: "two properties satisfy the `minProperties` condition, so `then` applies and demands an address"},
				{Name: "condition holds, good format", Doc: `{"a":1,"p":` + goodEmail + `}`, Valid: true,
					Why: "control for the above"},
				{Name: "condition fails", Doc: `{"p":` + badEmail + `}`, Valid: true,
					Why: "one property, so the condition does not hold and `then` says nothing -- the control that separates a fix from enforcing `then` unconditionally"},
				{Name: "condition holds, property absent", Doc: `{"a":1,"b":2}`, Valid: false,
					Why: "control: the branch's `required` binds once the condition holds"},
			},
		},
		{
			Name:       "format_anyof_in_then_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_format_anyof_in_then_draft7.json",
			Instances: []notInstance{
				{Name: "nested, bad format", Doc: `{"t":1,"p":` + badEmail + `}`, Valid: false,
					Why: "a format inside an anyOf inside a then: the branch reading modelled no applicator, so the whole anyOf went with it"},
				{Name: "nested, good format", Doc: `{"t":1,"p":` + goodEmail + `}`, Valid: true, Why: "control for the above"},
				{Name: "branch not taken", Doc: `{"t":2,"p":` + badEmail + `}`, Valid: true,
					Why: "the condition does not hold"},
			},
		},
		{
			Name:       "content_propertynames_in_then_draft7_asserts",
			SchemaPath: "testdata/schemas/regression/branch_content_propertynames_in_then_draft7.json",
			Instances: []notInstance{
				{Name: "nested, key not base64", Doc: `{"t":1,"o":{` + badB64 + `:1}}`, Valid: false,
					Why: "a content assertion under a propertyNames under a then, which needs both readings to be whole"},
				{Name: "nested, key is base64", Doc: `{"t":1,"o":{` + goodB64 + `:1}}`, Valid: true, Why: "control for the above"},
				{Name: "branch not taken", Doc: `{"t":2,"o":{` + badB64 + `:1}}`, Valid: true,
					Why: "the condition does not hold, so the nested demand never applies"},
			},
		},
	}
}

// TestBranchPositionsKeepTheDialectsPosture compiles each fixture and puts every
// document to the generated type.
//
// Reading the generated source is not enough and is how this survived a sweep:
// at `then`, `dependentSchemas` and `unevaluated*` the emitted check looked
// complete and simply had one conjunct missing, and at `anyOf` and `contains`
// there was no check and no warning at all -- generation printed nothing, the
// type had no Validate, and `UnenforcedSchemas()` was empty, because the arm
// that records an unenforced schema only runs for a schema being turned into an
// alias.
func TestBranchPositionsKeepTheDialectsPosture(t *testing.T) {
	for _, fx := range branchPostureFixtures() {
		t.Run(fx.Name, func(t *testing.T) {
			generated := generateFromSchemaWithConfig(t, fx.SchemaPath, generator.Config{
				PackageName:  "testpkg",
				OmitEmpty:    true,
				RootTypeName: "Root",
			})
			const rootType = "Root"

			tmpDir := t.TempDir()
			generatedMain := strings.Replace(string(generated), "package testpkg", "package main", 1)
			if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(generatedMain), 0o644); err != nil {
				t.Fatalf("writing types.go: %v", err)
			}
			writeSharedHelpers(t, tmpDir, generatedMain)

			mainGo, err := notInstanceMain(rootType, fx.Instances)
			if err != nil {
				t.Fatalf("building main.go: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
				t.Fatalf("writing main.go: %v", err)
			}
			if err := writeTestGoMod(tmpDir, "branch_posture_test"); err != nil {
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
