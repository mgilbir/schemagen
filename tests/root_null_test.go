package tests

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// rootNullFixtures are issue #267: three root shapes that accepted a JSON null
// although the schema's own type excludes it.
//
// A plain object root has refused one since #103, so each of these is a
// specialised root path that lost the guard on the way past -- the same
// sibling-position pattern that has recurred throughout this generator. The
// three lost it in three different ways and the fix is one: generateTypeDef asks
// schemaForbidsNull once, of the schema as written, and hands the answer to
// whichever def the arm produced.
//
// They compile and run rather than compare text. Two of the three are invisible
// in the emitted source unless you already know what is missing: (a) is an
// UnmarshalJSON that looks complete and is one guard short, and (b) is a type
// with no UnmarshalJSON at all, which reads exactly like every other const enum.
// Only a document put to the compiled type says which.
//
// Every fixture carries its own acceptance control, because over-correcting here
// costs a document the schema permits and that is the more expensive direction:
// a false rejection is visible to the caller and a false acceptance is not.
func rootNullFixtures() []notFixture {
	return []notFixture{
		{
			// (a) The merged root. Reported symptom: `null` accepted.
			Name:       "root_null_allof_merge",
			SchemaPath: "testdata/schemas/regression/root_null_allof_merge.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: false,
					Why: "issue #267(a): the merged root's UnmarshalJSON had no null guard, so the decode was a no-op and Validate judged a zero struct"},
				{Name: "an object", Doc: `{"a":"x"}`, Valid: true,
					Why: "control: the merge still accepts what it describes"},
				{Name: "the empty object", Doc: `{}`, Valid: true,
					Why: "control: the branch declares no required property"},
				{Name: "null at the property the branch declares", Doc: `{"a":null}`, Valid: false,
					Why: "control: the property position has refused one all along, and must go on doing so"},
				{Name: "not an object at all", Doc: `"x"`, Valid: false,
					Why: "control: \"type\":\"object\" still binds, and a guard that replaced the decode rather than preceding it would lose this"},
			},
		},
		{
			// The acceptance control for (a).
			Name:       "root_null_allof_merge_nullable",
			SchemaPath: "testdata/schemas/regression/root_null_allof_merge_nullable.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: true,
					Why: "the type list admits null; refusing it would be the same defect in the direction that costs a permitted document"},
				{Name: "an object", Doc: `{"a":"x"}`, Valid: true, Why: "control"},
			},
		},
		{
			// (b) The const enum whose zero is a member.
			Name:       "root_null_enum_empty_member",
			SchemaPath: "testdata/schemas/regression/root_null_enum_empty_member.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: false,
					Why: "issue #267(b): `type Root string` carried no UnmarshalJSON, so a null left \"\" behind and the enum admits \"\""},
				{Name: "the empty string", Doc: `""`, Valid: true,
					Why: "the sharpest control: \"\" is a member and must stay one. A guard that refused the zero rather than the null would pass every other case here"},
				{Name: "a listed member", Doc: `"a"`, Valid: true, Why: "control"},
				{Name: "a value the enum does not list", Doc: `"z"`, Valid: false,
					Why: "control: the membership check is what it always was"},
				{Name: "a number", Doc: `1`, Valid: false,
					Why: "control: \"type\":\"string\" still binds"},
			},
		},
		{
			// The acceptance control for (b).
			Name:       "root_null_enum_admits_null",
			SchemaPath: "testdata/schemas/regression/root_null_enum_admits_null.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: true,
					Why: "null is a member; the guard must not reach an enum that lists it"},
				{Name: "a listed member", Doc: `"a"`, Valid: true, Why: "control"},
				{Name: "a value the enum does not list", Doc: `"z"`, Valid: false, Why: "control"},
			},
		},
		{
			// (c) The root union with a scalar branch. The schema already had a
			// golden of its own; what it never had was a document put to it.
			Name:       "oneof_root_scalar_branch",
			SchemaPath: "testdata/schemas/regression/oneof_root_scalar_branch.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: false,
					Why: "issue #267(c): the branch loop steps over a null, which at the top level leaves the union unset and Validate with nothing to judge"},
				{Name: "the scalar branch", Doc: `"x"`, Valid: true,
					Why: "the control the guard's placement turns on: the union must still skip the opening object decode, or every string is refused before a branch is tried"},
				{Name: "the object branch", Doc: `{"k":"v"}`, Valid: true, Why: "control"},
				{Name: "an object matching neither branch", Doc: `{}`, Valid: false,
					Why: "control: the object branch requires k and the string branch is not an object"},
				{Name: "a number", Doc: `1`, Valid: false, Why: "control: no branch describes a number"},
			},
		},
		{
			// The acceptance control for (c).
			Name:       "root_null_oneof_null_branch",
			SchemaPath: "testdata/schemas/regression/root_null_oneof_null_branch.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: true,
					Why: "a branch describes null, so stepping over one is right here and the guard must not fire"},
				{Name: "the scalar branch", Doc: `"x"`, Valid: true, Why: "control"},
				{Name: "the object branch", Doc: `{"k":"v"}`, Valid: true, Why: "control"},
			},
		},
		{
			// The fourth def kind: the wrapper a root with no "type" of its own
			// gets, where the branches are what exclude the null. Not one of the
			// three reported shapes -- it is the same predicate reaching one arm
			// further, and it is here because that arm has no other case that
			// exercises it.
			Name:       "root_null_inferred_wrapper",
			SchemaPath: "testdata/schemas/regression/root_null_inferred_wrapper.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: false,
					Why: "no branch of the anyOf admits a null; the wrapper stored it raw and left it unjudged"},
				{Name: "a string the bound admits", Doc: `"ab"`, Valid: true, Why: "control"},
				{Name: "a string the bound refuses", Doc: `"a"`, Valid: false,
					Why: "control: minLength still binds, and a guard that swallowed the type would lose it"},
				{Name: "an integer", Doc: `5`, Valid: true,
					Why: "control: the second branch admits it, and minLength does not apply to a number"},
			},
		},
		{
			// The acceptance control for the wrapper.
			Name:       "root_null_inferred_wrapper_nullable",
			SchemaPath: "testdata/schemas/regression/root_null_inferred_wrapper_nullable.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: true,
					Why: "a branch describes null, so the wrapper must go on storing it raw"},
				{Name: "a string the bound admits", Doc: `"ab"`, Valid: true, Why: "control"},
				{Name: "a string the bound refuses", Doc: `"a"`, Valid: false,
					Why: "control: this is what makes the fixture able to fail at all"},
			},
		},
		{
			// The sibling positions the issue says already refuse a null. They
			// are here to stay unchanged: a fix that reached them would be
			// reaching further than the defect.
			Name:       "root_null_sibling_positions",
			SchemaPath: "testdata/schemas/regression/root_null_sibling_positions.json",
			Instances: []notInstance{
				{Name: "merged at a property", Doc: `{"mergedAtProperty":null}`, Valid: false, Why: "control: refused before the fix and after it"},
				{Name: "enum at a property", Doc: `{"enumAtProperty":null}`, Valid: false, Why: "control"},
				{Name: "union at a property", Doc: `{"unionAtProperty":null}`, Valid: false, Why: "control"},

				{Name: "merged at an element", Doc: `{"mergedAtElement":[null]}`, Valid: false, Why: "control"},
				{Name: "enum at an element", Doc: `{"enumAtElement":[null]}`, Valid: false, Why: "control"},
				{Name: "union at an element", Doc: `{"unionAtElement":[null]}`, Valid: false, Why: "control"},

				{Name: "merged at a map value", Doc: `{"mergedAtMapValue":{"k":null}}`, Valid: false, Why: "control"},
				{Name: "enum at a map value", Doc: `{"enumAtMapValue":{"k":null}}`, Valid: false, Why: "control"},
				{Name: "union at a map value", Doc: `{"unionAtMapValue":{"k":null}}`, Valid: false, Why: "control"},

				{Name: "the values those positions admit", Doc: `{"mergedAtProperty":{"a":"x"},"enumAtProperty":"","unionAtProperty":"x","mergedAtElement":[{"a":"x"}],"enumAtElement":["a"],"unionAtElement":[{"k":"v"}],"mergedAtMapValue":{"k":{"a":"x"}},"enumAtMapValue":{"k":""},"unionAtMapValue":{"k":"x"}}`, Valid: true,
					Why: "the acceptance control for all nine: every position still takes what its sub-schema describes, including the \"\" the enum admits"},

				{Name: "a permitted null at a property", Doc: `{"nullableEnumAtProperty":null}`, Valid: true,
					Why: "control: the sub-schema lists null, so no position may refuse it"},
				{Name: "a permitted null at an element", Doc: `{"nullableMergeAtElement":[null]}`, Valid: true, Why: "control for the above"},

				{Name: "nothing present", Doc: `{}`, Valid: true, Why: "control: every property is optional"},
			},
		},
	}
}

// TestRootNullIsRefusedWhereTheSchemaExcludesIt runs each fixture compiled.
func TestRootNullIsRefusedWhereTheSchemaExcludesIt(t *testing.T) {
	runInstanceFixtures(t, "root_null_test", rootNullFixtures())
}

// rootNullBigIntFixtures are the same defect in the one type kind that needs a
// flag to exist. See root_null_bigint_merged_type.json.
//
// The wrapper is the only def here that holds a permitted null as a *state*
// rather than merely admitting one, so it is also the only one where the fix
// has two halves: refuse the value, and stop carrying the state for it. The
// second fixture is what keeps the second half from reaching a schema that
// really does admit a null.
func rootNullBigIntFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "root_null_bigint_merged_type",
			SchemaPath: "testdata/schemas/regression/root_null_bigint_merged_type.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: false,
					Why: "the parent's \"type\":\"integer\" excludes null; the wrapper read the merge, which a branch had widened"},
				{Name: "an integer", Doc: `1`, Valid: true,
					Why: "control: both the parent and the branch admit one"},
				{Name: "a string", Doc: `"1"`, Valid: false,
					Why: "control: the wrapper has always refused a JSON string for a number"},
			},
		},
		{
			Name:       "root_null_bigint_nullable",
			SchemaPath: "testdata/schemas/regression/root_null_bigint_nullable.json",
			Instances: []notInstance{
				{Name: "null at the root", Doc: `null`, Valid: true,
					Why: "issue #85's null state must survive: the type list admits one"},
				{Name: "an integer", Doc: `1`, Valid: true, Why: "control"},
			},
		},
	}
}

// TestRootNullIsRefusedInTheBigIntWrapper runs the big-integer half.
func TestRootNullIsRefusedInTheBigIntWrapper(t *testing.T) {
	runInstanceFixturesWithConfig(t, "root_null_bigint_test", rootNullBigIntFixtures(), generator.Config{
		PackageName:   "testpkg",
		OmitEmpty:     true,
		RootTypeName:  "Root",
		BigIntSupport: true,
	})
}
