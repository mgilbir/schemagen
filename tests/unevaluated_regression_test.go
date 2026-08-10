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

// unevaluatedFixtures are the two unevaluated keywords put to the same
// documents in the same positions, one pair at a time.
//
// They are written in pairs on purpose. `unevaluatedItems` and
// `unevaluatedProperties` are answered by separate machinery -- the annotation
// path and buildUnevaluatedItemsDef for one, collectRuntimeBranchChecks and
// buildUnevaluatedPropertiesDef for the other -- and every defect in this family
// so far has been one of the two learning something the other did not:
//
//   - #184, a schema-valued sub-schema: `minLength` bound a leftover property
//     and said nothing about a leftover item, and `enum`, `const`, `$ref`,
//     `required` and the composition keywords bound neither.
//   - #189, a `$ref` inside the branch that evaluates the tuple: the annotation
//     path compiles with reference inlining off, so the schema fell back to a
//     static check that does not enforce `unevaluatedItems` at all.
//   - #190, the keyword beside an `anyOf`: the anyOf merge builds its struct
//     without the parent's keyword, so nothing enforced it.
//
// A pair whose two halves disagree is the next instance of that, which is why
// the halves are listed together and never merged into one fixture: a fixture
// covering both keywords at once passes as soon as either arm works.
//
// Each case compiles and runs, because none of these were visible in the
// generated text -- the false accepts emitted a Validate that looks like a
// schema with less in it, which is exactly what a golden records without
// comment.
func unevaluatedFixtures() []notFixture {
	return []notFixture{
		{
			// #189. The tuple slot is a $ref, which is the ordinary way to write
			// it; the inline spelling of the same tuple has always rejected.
			Name:       "items_ref_in_branch",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_ref_in_branch.json",
			Instances: []notInstance{
				{Name: "unevaluated tail", Doc: `["a",1]`, Valid: false,
					Why: "index 1 is unevaluated and unevaluatedItems is false; accepting it is issue #189"},
				{Name: "just the tuple", Doc: `["a"]`, Valid: true,
					Why: "the only position is the one the branch evaluates"},
				{Name: "wrong slot type", Doc: `[1]`, Valid: false,
					Why: "the $ref'd slot schema still binds; a fix that only adds the tail check loses it"},
			},
		},
		{
			// The mirror, which has always worked. It is here so that the pair
			// disagreeing again is a failure rather than a thing nobody looks at.
			Name:       "properties_ref_in_branch",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_ref_in_branch.json",
			Instances: []notInstance{
				{Name: "unevaluated key", Doc: `{"a":"x","b":1}`, Valid: false,
					Why: "b is unevaluated and unevaluatedProperties is false"},
				{Name: "just the declared key", Doc: `{"a":"x"}`, Valid: true,
					Why: "the only key is the one the branch evaluates"},
				{Name: "wrong property type", Doc: `{"a":1}`, Valid: false,
					Why: "the $ref'd property schema still binds"},
			},
		},
		{
			// #190. No sibling keyword is needed: the anyOf merge drops the
			// parent's unevaluatedProperties on the way to the struct.
			Name:       "properties_in_anyof",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_in_anyof.json",
			Instances: []notInstance{
				{Name: "unevaluated key", Doc: `{"a":"x","b":1}`, Valid: false,
					Why: "b is evaluated by no branch; accepting it is issue #190"},
				{Name: "evaluated key", Doc: `{"a":"x"}`, Valid: true,
					Why: "the branch evaluates a, so nothing is left over"},
				{Name: "empty", Doc: `{}`, Valid: true,
					Why: "nothing to be unevaluated"},
			},
		},
		{
			Name:       "items_in_anyof",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_in_anyof.json",
			Instances: []notInstance{
				{Name: "unevaluated tail", Doc: `["x",1]`, Valid: false,
					Why: "index 1 is evaluated by no branch"},
				{Name: "evaluated slot", Doc: `["x"]`, Valid: true,
					Why: "the branch evaluates index 0"},
				{Name: "empty", Doc: `[]`, Valid: true,
					Why: "nothing to be unevaluated"},
			},
		},
		{
			// #184, the half about the string vocabulary. `type` and the numeric
			// bounds fired for a leftover item; minLength and pattern did not,
			// while both bound a leftover property two lines away.
			Name:       "items_string_subschema",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_string_subschema.json",
			Instances: []notInstance{
				{Name: "too short", Doc: `[true,"ab"]`, Valid: false,
					Why: "minLength on the sub-schema; accepting it is issue #184"},
				{Name: "pattern misses", Doc: `[true,"zzz"]`, Valid: false,
					Why: "pattern on the sub-schema, which was dropped with minLength"},
				{Name: "satisfies both", Doc: `[true,"abc"]`, Valid: true,
					Why: "a leftover item the sub-schema admits must still be accepted"},
				{Name: "wrong type", Doc: `[true,1]`, Valid: false,
					Why: "the type arm already worked and must keep working"},
			},
		},
		{
			Name:       "properties_string_subschema",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_string_subschema.json",
			Instances: []notInstance{
				{Name: "too short", Doc: `{"a":true,"b":"ab"}`, Valid: false,
					Why: "minLength on the sub-schema"},
				{Name: "pattern misses", Doc: `{"a":true,"b":"zzz"}`, Valid: false,
					Why: "pattern on the sub-schema"},
				{Name: "satisfies both", Doc: `{"a":true,"b":"abc"}`, Valid: true,
					Why: "a leftover property the sub-schema admits must still be accepted"},
				{Name: "wrong type", Doc: `{"a":true,"b":1}`, Valid: false,
					Why: "the type arm already worked and must keep working"},
			},
		},
		{
			// #184, the half both keywords failed: a sub-schema keyword neither
			// check has an arm for. The schema goes to the runtime evaluator
			// rather than being checked by a subset of what it says.
			Name:       "items_enum_subschema",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_enum_subschema.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `[true,"zzz"]`, Valid: false,
					Why: "enum on the sub-schema; accepting it is issue #184"},
				{Name: "inside the enum", Doc: `[true,"x"]`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "evaluated slot only", Doc: `[true]`, Valid: true,
					Why: "prefixItems evaluates index 0, so the sub-schema judges nothing"},
			},
		},
		{
			Name:       "properties_enum_subschema",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_enum_subschema.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `{"a":true,"b":"zzz"}`, Valid: false,
					Why: "enum on the sub-schema; accepting it is issue #184"},
				{Name: "inside the enum", Doc: `{"a":true,"b":"x"}`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "declared key only", Doc: `{"a":true}`, Valid: true,
					Why: "properties evaluates a, so the sub-schema judges nothing"},
			},
		},
		{
			// The same sub-schema one position in. A schema written inline as a
			// property is never given a name, so the wrapper carrying the check
			// is not built for it unless something asks -- which is why the
			// keyword was enforced at a document root and in a $defs entry and
			// nowhere else. See inlineUnevaluatedWrapper.
			Name:       "items_enum_subschema_in_property",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_enum_subschema_in_property.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `{"p":[true,"zzz"]}`, Valid: false,
					Why: "enum on the sub-schema of an inline array property"},
				{Name: "inside the enum", Doc: `{"p":[true,"x"]}`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "absent", Doc: `{}`, Valid: true,
					Why: "an optional property the document does not carry"},
			},
		},
		{
			Name:       "properties_enum_subschema_in_property",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_enum_subschema_in_property.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `{"p":{"a":true,"b":"zzz"}}`, Valid: false,
					Why: "enum on the sub-schema of an inline object property"},
				{Name: "inside the enum", Doc: `{"p":{"a":true,"b":"x"}}`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "absent", Doc: `{}`, Valid: true,
					Why: "an optional property the document does not carry"},
			},
		},
		{
			// And one position further in again: an array *element*, which is a
			// second inline site with a second predicate to satisfy. It is listed
			// separately from the property because the two are separate arms --
			// resolvePropertyType and resolveArrayItemType -- and a fixture that
			// covered only one left the other unfalsifiable.
			Name:       "items_enum_subschema_in_element",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_enum_subschema_in_element.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `[[true,"zzz"]]`, Valid: false,
					Why: "enum on the sub-schema of an inline array element"},
				{Name: "inside the enum", Doc: `[[true,"x"]]`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "empty outer", Doc: `[]`, Valid: true,
					Why: "no element to judge"},
			},
		},
		{
			Name:       "properties_enum_subschema_in_element",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_enum_subschema_in_element.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `[{"a":true,"b":"zzz"}]`, Valid: false,
					Why: "enum on the sub-schema of an inline object element"},
				{Name: "inside the enum", Doc: `[{"a":true,"b":"x"}]`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "empty outer", Doc: `[]`, Valid: true,
					Why: "no element to judge"},
			},
		},
		{
			// Issue #201: the third inline site, a map value. It is the position
			// #198 left open, because it resolves its type through neither arm
			// that change wired -- an object's own `additionalProperties` is
			// typed by generateStructDef and generatePropertylessObjectDef,
			// which went straight to resolveType and reached none of the
			// wrappers resolvePropertyType and resolveArrayItemType offer. So
			// the map value kept []any and the sub-schema was dropped, while the
			// same sub-schema at a property and at an element rejected.
			//
			// The overflow map of a struct that also declares properties is the
			// same arm's sibling and was broken identically, so it is a fixture
			// of its own rather than a row here: the two are separate functions.
			Name:       "items_enum_subschema_in_map_value",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_enum_subschema_in_map_value.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `{"k":[true,"zzz"]}`, Valid: false,
					Why: "enum on the sub-schema of a map value; accepting it is issue #201"},
				{Name: "inside the enum", Doc: `{"k":[true,"x"]}`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "evaluated slot only", Doc: `{"k":[true]}`, Valid: true,
					Why: "prefixItems evaluates index 0, so the sub-schema judges nothing"},
			},
		},
		{
			// The pair's other half. It already rejected before #201 was fixed --
			// an object-shaped value is materialized as a named type and answers
			// for itself through its own Validate, where the array-shaped one
			// resolved to []any -- so this is the control the row above is
			// measured against, and the halves disagreeing again is a failure
			// rather than something nobody looks at.
			Name:       "properties_enum_subschema_in_map_value",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_enum_subschema_in_map_value.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `{"k":{"a":true,"b":"zzz"}}`, Valid: false,
					Why: "the pair's other half at the same position, which already worked"},
				{Name: "inside the enum", Doc: `{"k":{"a":true,"b":"x"}}`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "declared key only", Doc: `{"k":{"a":true}}`, Valid: true,
					Why: "properties evaluates a, so the sub-schema judges nothing"},
			},
		},
		{
			// The same schema-valued additionalProperties on a struct that also
			// declares a property, which is the other function typing that
			// position. Both were broken; a fix to one is silent about the other.
			Name:       "items_enum_subschema_in_overflow_value",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_enum_subschema_in_overflow_value.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `{"k":[true,"zzz"]}`, Valid: false,
					Why: "the overflow map of a struct with declared properties beside its additionalProperties"},
				{Name: "inside the enum", Doc: `{"k":[true,"x"]}`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "declared property beside it", Doc: `{"decl":"s","k":[true,"x"]}`, Valid: true,
					Why: "control: the declared property is not an additional one and the sub-schema says nothing about it"},
			},
		},
		{
			// Already rejecting for the reason the map-value half above gives.
			Name:       "properties_enum_subschema_in_overflow_value",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_enum_subschema_in_overflow_value.json",
			Instances: []notInstance{
				{Name: "outside the enum", Doc: `{"k":{"a":true,"b":"zzz"}}`, Valid: false,
					Why: "the pair's other half at the same position, which already worked"},
				{Name: "inside the enum", Doc: `{"k":{"a":true,"b":"x"}}`, Valid: true,
					Why: "a listed value must still be accepted"},
				{Name: "declared property beside it", Doc: `{"decl":"s","k":{"a":true}}`, Valid: true,
					Why: "control for the above"},
			},
		},
		{
			// The false rejection in the same surface, and the reason the gate
			// asks for a stated `type` rather than guessing one from a bound.
			// The static check decodes each leftover value into one Go type, so
			// unevaluatedProperties guessed "string" from the minLength and then
			// refused every number and every object -- values the keyword says
			// nothing about.
			Name:       "properties_untyped_subschema",
			SchemaPath: "testdata/schemas/regression/unevaluated_properties_untyped_subschema.json",
			Instances: []notInstance{
				{Name: "not a string", Doc: `{"a":true,"b":1}`, Valid: true,
					Why: "minLength constrains strings and nothing else; rejecting this is the false rejection"},
				{Name: "short string", Doc: `{"a":true,"b":"ab"}`, Valid: false,
					Why: "and it does constrain a string"},
				{Name: "long enough", Doc: `{"a":true,"b":"abc"}`, Valid: true,
					Why: "control"},
			},
		},
		{
			Name:       "items_untyped_subschema",
			SchemaPath: "testdata/schemas/regression/unevaluated_items_untyped_subschema.json",
			Instances: []notInstance{
				{Name: "not a string", Doc: `[true,1]`, Valid: true,
					Why: "minLength constrains strings and nothing else"},
				{Name: "short string", Doc: `[true,"ab"]`, Valid: false,
					Why: "and it does constrain a string"},
				{Name: "long enough", Doc: `[true,"abc"]`, Valid: true,
					Why: "control"},
			},
		},
	}
}

// TestUnevaluatedKeywordsEnforceWhatTheyState runs each fixture's documents
// against the compiled type.
//
// Same harness as TestNotIsEnforcedAndDoesNotOverNegate and for the same
// reason: what changed is which check the generated Validate carries, and no
// comparison of generated text to a golden can say whether a document is
// accepted by it.
func TestUnevaluatedKeywordsEnforceWhatTheyState(t *testing.T) {
	for _, fx := range unevaluatedFixtures() {
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
			if err := writeTestGoMod(tmpDir, "unevaluated_regression_test"); err != nil {
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
