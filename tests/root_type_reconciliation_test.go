package tests

import (
	"testing"
)

// rootTypeReconciliationFixtures are issue #270: a struct's Go shape is chosen
// from `properties` and the `type` keyword beside it was never reconciled with
// the result.
//
// The two directions are one defect and both are here, on purpose. A fix that
// only refuses more turns the false accepts into correct behaviour and leaves
// the false rejects exactly where they were -- and a false rejection costs a
// document the schema permits, which is the direction the caller sees. So every
// fixture below states both: the values the type forbids, and every value it
// permits.
//
// They compile and run rather than compare text. The whole defect is what a
// document does when it is put to the generated type: the emitted source for
// {"type":"null","properties":{...}} reads exactly like the source for
// {"properties":{...}}, and only a document says which one refuses an object.
func rootTypeReconciliationFixtures() []notFixture {
	return []notFixture{
		{
			// The narrowing direction, in the issue's own words. The corpus has
			// carried this schema as a fuzz seed since the adversarial set was
			// written; what it never had was a document put to it.
			Name:       "null-with-properties",
			SchemaPath: "testdata/schemas/adversarial/contra/null-with-properties.json",
			Instances: []notInstance{
				{Name: "the empty object", Doc: `{}`, Valid: false,
					Why: "issue #270: `type` names null, and an object took the struct decode where nothing asks what the type said"},
				{Name: "an object the properties describe", Doc: `{"a":"x"}`, Valid: false,
					Why: "the same; matching the declared shape is not a reason to be admitted when no object is admitted"},
				{Name: "an object the properties do not describe", Doc: `{"k":"v"}`, Valid: false,
					Why: "the same, through the overflow map"},
				{Name: "null", Doc: `null`, Valid: true,
					Why: "the acceptance control, and the whole of what this schema admits: a fix that refused the object by refusing everything would pass every case above and fail here"},
				{Name: "an array", Doc: `["x"]`, Valid: false,
					Why: "control: this one was already refused, by the type rule the non-object path carries -- the type was half-read, and the half that was read must stay read"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control"},
				{Name: "a number", Doc: `1`, Valid: false, Why: "control"},
				{Name: "a boolean", Doc: `true`, Valid: false, Why: "control"},
			},
		},
		{
			// The widening direction, the mirror of the above: the type names
			// object *and* something else, and the struct refused the something
			// else before any constraint was consulted.
			Name:       "type-string-object-props",
			SchemaPath: "testdata/schemas/adversarial/contra/type-string-object-props.json",
			Instances: []notInstance{
				{Name: "a string", Doc: `"x"`, Valid: true,
					Why: "issue #270's false rejection: the schema's own type names string, and the opening struct decode refused it"},
				{Name: "the empty string", Doc: `""`, Valid: true, Why: "the same; no keyword bounds the string half"},
				{Name: "the empty object", Doc: `{}`, Valid: true, Why: "control: the object half still decodes"},
				{Name: "an object the properties describe", Doc: `{"a":"x"}`, Valid: true, Why: "control"},
				{Name: "an object with a property of the wrong type", Doc: `{"a":1}`, Valid: false,
					Why: "the sharpest control: widening must not cost the object half its checks"},
				{Name: "an object with an undeclared key", Doc: `{"b":"x"}`, Valid: true,
					Why: "control: additionalProperties is absent, so an extra key is permitted"},
				{Name: "null", Doc: `null`, Valid: false, Why: "control: the type names neither null"},
				{Name: "a number", Doc: `1`, Valid: false, Why: "control"},
				{Name: "an array", Doc: `["x"]`, Valid: false, Why: "control"},
			},
		},
		{
			Name:       "root_type_object_null",
			SchemaPath: "testdata/schemas/regression/root_type_object_null.json",
			Instances: []notInstance{
				{Name: "null", Doc: `null`, Valid: true, Why: "the type list names null"},
				{Name: "an object", Doc: `{"a":"x"}`, Valid: true, Why: "and object"},
				{Name: "the empty object", Doc: `{}`, Valid: true, Why: "no property is required"},
				{Name: "a null at the declared property", Doc: `{"a":null}`, Valid: false,
					Why: "control: the property's own type forbids one, and a nullable root must not make its members nullable"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control: the type list names neither"},
				{Name: "an array", Doc: `[]`, Valid: false, Why: "control"},
				{Name: "a number", Doc: `4`, Valid: false, Why: "control"},
				{Name: "a boolean", Doc: `true`, Valid: false, Why: "control"},
			},
		},
		{
			Name:       "root_type_array_object",
			SchemaPath: "testdata/schemas/regression/root_type_array_object.json",
			Instances: []notInstance{
				{Name: "the empty array", Doc: `[]`, Valid: true, Why: "issue #270: the type names array and the struct decode refused one"},
				{Name: "an array with elements", Doc: `["x"]`, Valid: true,
					Why: "the same, and nothing here describes the positions, so every element is admitted"},
				{Name: "an object", Doc: `{"a":"x"}`, Valid: true, Why: "control: the object half is unchanged"},
				{Name: "an object with a property of the wrong type", Doc: `{"a":1}`, Valid: false, Why: "control"},
				{Name: "null", Doc: `null`, Valid: false, Why: "control: not named"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control"},
				{Name: "a number", Doc: `4`, Valid: false, Why: "control"},
			},
		},
		{
			Name:       "root_type_number_object",
			SchemaPath: "testdata/schemas/regression/root_type_number_object.json",
			Instances: []notInstance{
				{Name: "a number at the bound", Doc: `5`, Valid: true, Why: "issue #270: the type names number and the struct decode refused one"},
				{Name: "a number above the bound", Doc: `7`, Valid: true, Why: "the same"},
				{Name: "a number below the bound", Doc: `4`, Valid: false,
					Why: "the sharpest control for the widening: admitting the number half must not cost it the keyword that bounds it"},
				{Name: "an object", Doc: `{"a":"x"}`, Valid: true,
					Why: "control: `minimum` says nothing about an object, and judging the object half against it would be the same defect turned around"},
				{Name: "null", Doc: `null`, Valid: false,
					Why: "control: the type list names neither -- and this is also where the bound used to be applied to a null as 0"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control"},
				{Name: "an array", Doc: `[]`, Valid: false, Why: "control"},
			},
		},
		{
			// Issue #270's third shape: a bound at a typeless root, applied to a
			// null as the zero encoding/json left behind. The draft-4 boolean
			// spelling of the bound, because that is the corpus schema the issue
			// names and the exclusive form is the one that fires on the zero.
			Name:       "exclusive_bound_boolean_nonobject",
			SchemaPath: "testdata/schemas/regression/exclusive_bound_boolean_nonobject.json",
			Instances: []notInstance{
				{Name: "null", Doc: `null`, Valid: true,
					Why: "issue #270: a numeric keyword does not apply to a value that is not a number, and the decode into float64 turned the null into 0"},
				{Name: "a number above the bound", Doc: `6`, Valid: true, Why: "control"},
				{Name: "the bound itself", Doc: `5`, Valid: false,
					Why: "control: exclusiveMinimum:true beside minimum:5 excludes 5, and the null guard must not reach a number"},
				{Name: "a number below the bound", Doc: `4`, Valid: false, Why: "control"},
				{Name: "a string", Doc: `"x"`, Valid: true, Why: "control: the bound says nothing about a string either"},
				{Name: "an object", Doc: `{"a":"x"}`, Valid: true, Why: "control: the properties still describe it"},
			},
		},
		{
			// Issue #270's fourth shape: the merged-struct path, where the hatch
			// was closed by a hard `false` rather than by anything the schema
			// said.
			Name:       "anyof_required_branches",
			SchemaPath: "testdata/schemas/regression/anyof_required_branches.json",
			Instances: []notInstance{
				{Name: "null", Doc: `null`, Valid: true,
					Why: "issue #270: `required` is vacuous on a value that is not an object, so both branches match"},
				{Name: "a string", Doc: `"x"`, Valid: true, Why: "the same"},
				{Name: "a number", Doc: `1`, Valid: true, Why: "the same"},
				{Name: "an array", Doc: `[]`, Valid: true, Why: "the same"},
				{Name: "an object matching the first branch", Doc: `{"a":"x"}`, Valid: true, Why: "control"},
				{Name: "an object matching the second", Doc: `{"b":"x"}`, Valid: true, Why: "control"},
				{Name: "an object matching neither", Doc: `{}`, Valid: false,
					Why: "the control the whole widening turns on: an object is judged by the branches, and opening the escape hatch must not let one past them"},
			},
		},
		{
			Name:       "root_type_allof_merge",
			SchemaPath: "testdata/schemas/regression/root_type_allof_merge.json",
			Instances: []notInstance{
				{Name: "null", Doc: `null`, Valid: true,
					Why: "issue #270 on the allOf merge: no schema here states a type, and `required` is vacuous on a non-object"},
				{Name: "a string", Doc: `"x"`, Valid: true, Why: "the same"},
				{Name: "an object satisfying the branch", Doc: `{"a":"x"}`, Valid: true, Why: "control"},
				{Name: "an object missing the required key", Doc: `{}`, Valid: false,
					Why: "control: the branch still binds on an object, and the hatch must not carry one past it"},
				{Name: "an object with the wrong type at the key", Doc: `{"a":1}`, Valid: false, Why: "control"},
			},
		},
		{
			// Both directions on one schema: the parent's type excludes an
			// object, the branch describes one, and the merge carries the
			// branch's properties without the parent's type.
			Name:       "root_type_null_allof_merge",
			SchemaPath: "testdata/schemas/regression/root_type_null_allof_merge.json",
			Instances: []notInstance{
				{Name: "null", Doc: `null`, Valid: true, Why: "the only value the parent's type admits"},
				{Name: "an object the branch describes", Doc: `{"a":"x"}`, Valid: false,
					Why: "issue #270: the parent's type excludes it, and the merge the struct was built from never carried that type"},
				{Name: "the empty object", Doc: `{}`, Valid: false, Why: "the same"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control"},
				{Name: "an array", Doc: `[]`, Valid: false, Why: "control"},
			},
		},
		{
			// The narrowing direction where the root states no type at all: what
			// excludes the non-objects is a branch, and the arm that reads the
			// root's own `type` list saw nothing and opened the hatch.
			Name:       "root_type_branch_names_object",
			SchemaPath: "testdata/schemas/regression/root_type_branch_names_object.json",
			Instances: []notInstance{
				{Name: "a string", Doc: `"x"`, Valid: false,
					Why: "issue #270 in the narrowing direction: every anyOf branch names object, so nothing else is admitted"},
				{Name: "a number", Doc: `4`, Valid: false, Why: "the same"},
				{Name: "an array", Doc: `[]`, Valid: false, Why: "the same"},
				{Name: "null", Doc: `null`, Valid: false, Why: "the same"},
				{Name: "an object", Doc: `{"a":"x"}`, Valid: true, Why: "control: the object half is the whole of what this admits"},
				{Name: "the empty object", Doc: `{}`, Valid: true, Why: "control: nothing is required"},
				{Name: "an object with a property of the wrong type", Doc: `{"a":1}`, Valid: false, Why: "control"},
			},
		},
		{
			// The baseline, and the reason the reconciliation cannot simply
			// refuse more: a bare `properties` admits every JSON value.
			Name:       "root_type_untyped_properties",
			SchemaPath: "testdata/schemas/regression/root_type_untyped_properties.json",
			Instances: []notInstance{
				{Name: "null", Doc: `null`, Valid: true, Why: "no type is stated, so every kind is admitted and `required` is vacuous on this one"},
				{Name: "a string", Doc: `"x"`, Valid: true, Why: "the same"},
				{Name: "a number", Doc: `4`, Valid: true, Why: "the same"},
				{Name: "an array", Doc: `[]`, Valid: true, Why: "the same"},
				{Name: "a boolean", Doc: `true`, Valid: true, Why: "the same"},
				{Name: "an object satisfying required", Doc: `{"a":"x"}`, Valid: true, Why: "control"},
				{Name: "an object missing the required key", Doc: `{}`, Valid: false,
					Why: "control: `required` does bind on an object, and this is what a reconciliation that widened too far would lose"},
			},
		},
		{
			// The control that matters most: the shape nearly every corpus schema
			// has. Its emitted source is pinned byte for byte by the golden
			// beside it; this says the verdicts did not move either.
			Name:       "root_type_object_only",
			SchemaPath: "testdata/schemas/regression/root_type_object_only.json",
			Instances: []notInstance{
				{Name: "an object satisfying required", Doc: `{"a":"x"}`, Valid: true, Why: "control"},
				{Name: "an object missing the required key", Doc: `{}`, Valid: false, Why: "control"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control: `type` names object and nothing else"},
				{Name: "a number", Doc: `4`, Valid: false, Why: "control"},
				{Name: "an array", Doc: `[]`, Valid: false, Why: "control"},
				{Name: "null", Doc: `null`, Valid: false, Why: "control: #103's refusal, which must survive"},
				{Name: "a boolean", Doc: `true`, Valid: false, Why: "control"},
			},
		},
		{
			// The third shape at the position it shares its rendering with. A
			// pattern-value bucket judges raw bytes by decoding them into a Go
			// type, and every family read a null as the zero it left behind.
			Name:       "pattern_value_null_bounds",
			SchemaPath: "testdata/schemas/regression/pattern_value_null_bounds.json",
			Instances: []notInstance{
				{Name: "a null under a numeric bound", Doc: `{"num1":null}`, Valid: true,
					Why: "issue #270: `minimum` does not apply to a null, and the decode into float64 measured it as 0"},
				{Name: "a number below that bound", Doc: `{"num1":4}`, Valid: false, Why: "control"},
				{Name: "a number above it", Doc: `{"num1":6}`, Valid: true, Why: "control"},

				{Name: "a null under a length bound", Doc: `{"str1":null}`, Valid: true,
					Why: "the same for minLength, which measured the null as the empty string"},
				{Name: "a string below that bound", Doc: `{"str1":"ab"}`, Valid: false, Why: "control"},
				{Name: "a string above it", Doc: `{"str1":"abc"}`, Valid: true, Why: "control"},

				{Name: "a null under a pattern", Doc: `{"pat1":null}`, Valid: true,
					Why: "the same for pattern, which matched the null as the empty string"},
				{Name: "a string not matching", Doc: `{"pat1":"a"}`, Valid: false, Why: "control"},
				{Name: "a string matching", Doc: `{"pat1":"za"}`, Valid: true, Why: "control"},

				{Name: "a null under an item count", Doc: `{"arr1":null}`, Valid: true,
					Why: "the same for minItems, which counted the null as an empty array"},
				{Name: "an array below that count", Doc: `{"arr1":[1]}`, Valid: false, Why: "control"},
				{Name: "an array at it", Doc: `{"arr1":[1,2]}`, Valid: true, Why: "control"},
			},
		},
		{
			// The third shape in the other three keyword families. The numeric
			// one is above; these share a rendering with it and lost the same
			// null the same way.
			Name:       "root_type_nonobject_bounds",
			SchemaPath: "testdata/schemas/regression/root_type_nonobject_bounds.json",
			Instances: []notInstance{
				{Name: "null", Doc: `null`, Valid: true,
					Why: "issue #270: none of minLength, pattern or minItems applies to a null, and each measured the zero encoding/json left behind -- \"\" for the two string ones and an empty slice for the array one"},
				{Name: "a string satisfying both string bounds", Doc: `"zab"`, Valid: true, Why: "control"},
				{Name: "a string too short", Doc: `"za"`, Valid: false, Why: "control: minLength still binds on a string"},
				{Name: "a string not matching", Doc: `"abc"`, Valid: false, Why: "control: pattern still binds on a string"},
				{Name: "an array too short", Doc: `[1]`, Valid: false, Why: "control: minItems still binds on an array"},
				{Name: "an array long enough", Doc: `[1,2]`, Valid: true, Why: "control"},
				{Name: "an object", Doc: `{"a":"x"}`, Valid: true, Why: "control: none of the three says anything about an object"},
				{Name: "a number", Doc: `5`, Valid: true, Why: "control: nor about a number"},
				{Name: "a boolean", Doc: `true`, Valid: true, Why: "control"},
			},
		},
		{
			// The kind lattice: "integer" names a subset of "number", so a type
			// list that says integer excludes neither, and the escape hatch has
			// to stay open for the integers.
			Name:       "root_type_integer_properties",
			SchemaPath: "testdata/schemas/regression/root_type_integer_properties.json",
			Instances: []notInstance{
				{Name: "an integer", Doc: `5`, Valid: true,
					Why: "the whole of what this schema admits; a kind comparison made on the two names alone would find `integer` and `number` different and close the hatch on it"},
				{Name: "an object", Doc: `{}`, Valid: false, Why: "control: the type excludes one"},
				{Name: "an object the properties describe", Doc: `{"a":"x"}`, Valid: false, Why: "control"},
				{Name: "a fractional number", Doc: `1.5`, Valid: false, Why: "control: integer is still integer"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control"},
				{Name: "null", Doc: `null`, Valid: false, Why: "control"},
			},
		},
		{
			// Draft 3's schema-valued type alternatives, which the names in the
			// type array do not describe.
			Name:       "root_type_draft3_type_schemas",
			SchemaPath: "testdata/schemas/regression/root_type_draft3_type_schemas.json",
			Instances: []notInstance{
				{Name: "an object", Doc: `{}`, Valid: true,
					Why: "the second type entry is a schema describing an object; reading the array as the names \"string\" and nothing else would exclude every object"},
				{Name: "an object the alternative describes", Doc: `{"a":"x"}`, Valid: true, Why: "the same"},
				{Name: "a string", Doc: `"x"`, Valid: true, Why: "control: the named alternative"},
				{Name: "a number", Doc: `5`, Valid: false, Why: "control: neither alternative admits one"},
				{Name: "an array", Doc: `[]`, Valid: false, Why: "control"},
			},
		},
		{
			// A metaschema that withholds the validation vocabulary makes `type`
			// an annotation, and an annotation excludes nothing.
			Name:       "root_type_no_vocabulary",
			SchemaPath: "testdata/schemas/regression/root_type_no_vocabulary.json",
			Instances: []notInstance{
				{Name: "an object", Doc: `{}`, Valid: true,
					Why: "`type` asserts nothing here, so it cannot make an object inadmissible -- reading it would refuse a document the schema permits"},
				{Name: "an object the properties describe", Doc: `{"a":"x"}`, Valid: true, Why: "the same"},
				{Name: "null", Doc: `null`, Valid: true, Why: "the same, from the other side"},
				{Name: "a string", Doc: `"x"`, Valid: true, Why: "the same"},
			},
		},
		{
			// The parent's type and a branch's naming no kind in common, which
			// is what says where the conjunction has to happen: before the arm
			// that reads an empty conjunction, not after it.
			Name:       "allof_parent_type_contradicts_branch",
			SchemaPath: "testdata/schemas/regression/allof_parent_type_contradicts_branch.json",
			Instances: []notInstance{
				{Name: "an integer", Doc: `5`, Valid: false,
					Why: "the branch admits one and the parent's \"type\":\"string\" does not, so the conjunction admits nothing -- conjoining after the empty-conjunction check leaves the merge an ordinary integer that accepts this"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control: the other side of the same contradiction"},
				{Name: "null", Doc: `null`, Valid: false, Why: "control"},
				{Name: "an object", Doc: `{}`, Valid: false, Why: "control: nothing at all is admitted"},
			},
		},
		{
			// The parent's type meeting a guess rather than an assertion.
			Name:       "allof_parent_type_over_array_guess",
			SchemaPath: "testdata/schemas/regression/allof_parent_type_over_array_guess.json",
			Instances: []notInstance{
				{Name: "an object", Doc: `{}`, Valid: true,
					Why: "the branch's `items` is vacuous on an object and the parent says object; intersecting the parent's assertion with the merge's own \"array\" guess would leave nothing and refuse every instance"},
				{Name: "an object with keys", Doc: `{"a":"x"}`, Valid: true, Why: "the same; nothing here constrains a key"},
				{Name: "an array", Doc: `[]`, Valid: false, Why: "control: the parent's type excludes one"},
				{Name: "a string", Doc: `"x"`, Valid: false, Why: "control"},
			},
		},
		{
			// The merge widening a declared type, which #276 recorded in this
			// schema's own $comment as a defect of its own and left for a fix in
			// the merge. It is the same defect as the rest of this group -- a
			// shape derived from one keyword with `type` never reconciled with it
			// -- so it is fixed here, in conjoinParentDeclaredType, and this is
			// the default-configuration verdict that says so. The --big-int half
			// stays in TestRootNullIsRefusedInTheBigIntWrapper.
			Name:       "root_null_bigint_merged_type",
			SchemaPath: "testdata/schemas/regression/root_null_bigint_merged_type.json",
			Instances: []notInstance{
				{Name: "null", Doc: `null`, Valid: false,
					Why: "the parent's \"type\":\"integer\" excludes null; the branch widened the merge to integer-or-null, which resolveType answered with `*int64` -- a pointer, which carries no methods and so had nothing to refuse it with"},
				{Name: "an integer", Doc: `1`, Valid: true, Why: "control: both the parent and the branch admit one"},
				{Name: "a string", Doc: `"1"`, Valid: false, Why: "control"},
				{Name: "a fractional number", Doc: `1.5`, Valid: false, Why: "control: neither admits one"},
			},
		},
	}
}

// TestRootTypeIsReconciledWithItsPropertiesSibling runs each fixture compiled.
func TestRootTypeIsReconciledWithItsPropertiesSibling(t *testing.T) {
	runInstanceFixtures(t, "root_type_reconciliation_test", rootTypeReconciliationFixtures())
}
