package tests

import "testing"

// conditionalSubschemaFixtures are the `then` and `else` of an object-level
// if/then/else read whole, which is issue #209 and the last of the major
// applicators to get there.
//
// objectConditionalDef reduced a group to a list of required keys and a handful
// of per-property scalar checks, and where it could not say what a branch said it
// dropped the branch -- with nothing behind it. A `$ref` is the spelling that
// costs the most, because factoring a sub-schema into `$defs` is the ordinary way
// to write a schema: objectConditionalBranchLenient refuses a ref-carrying branch
// outright, so
//
//	{"if":{"required":["kind"]},"then":{"$ref":"#/$defs/other","required":["a"]}}
//
// accepted {"kind":"x"}. From 2019-09 the reference and the `required` beside it
// both bind and both were lost; before it the reference replaces its siblings,
// and the target it replaces them with was lost too. Written inline the same
// branch rejects, so where the sub-schema was written decided whether the
// consequence bound at all.
//
// They are run compiled rather than compared against a golden for the reason the
// `not` and the propertyNames fixtures are: the symptom is a Validate that
// accepts, which reads exactly like a schema with nothing to check.
func conditionalSubschemaFixtures() []notFixture {
	return []notFixture{
		{
			// Issue #209 in the shape the issue reports it, with a target that
			// says something so that both halves of the branch are visible.
			Name:       "conditional_branch_via_ref",
			SchemaPath: "testdata/schemas/regression/conditional_branch_via_ref.json",
			Instances: []notInstance{
				{Name: "sibling required binds", Doc: `{"kind":"x"}`, Valid: false,
					Why: "from 2019-09 the $ref applies alongside its siblings, so the `required` demands \"a\"; accepting this is issue #209"},
				{Name: "target binds", Doc: `{"kind":"x","a":"y","p":1,"q":2}`, Valid: false,
					Why: "the referenced target caps the object at three properties, and the branch says that as much as it says the required"},
				{Name: "both satisfied", Doc: `{"kind":"x","a":"y"}`, Valid: true,
					Why: "control: a fix that rejects this is an amputation"},
				{Name: "condition fails", Doc: `{"a":"y","p":1,"q":2,"r":3}`, Valid: true,
					Why: "control: the consequence applies only where the condition holds, and four properties is past the target's cap"},
			},
		},
		{
			// The `else` mirror. Mirrors are where this codebase leaves things
			// half-done, so the branch is written on the other side of the same
			// condition and asked the same two questions.
			Name:       "conditional_else_branch_via_ref",
			SchemaPath: "testdata/schemas/regression/conditional_else_branch_via_ref.json",
			Instances: []notInstance{
				{Name: "else target binds", Doc: `{"b":1}`, Valid: false,
					Why: "the condition fails, so the else applies and its $ref demands \"a\""},
				{Name: "else sibling binds", Doc: `{"a":"y"}`, Valid: false,
					Why: "the `required` written beside the reference demands \"b\", and it binds from 2019-09"},
				{Name: "else satisfied", Doc: `{"a":"y","b":1}`, Valid: true, Why: "control for the two above"},
				{Name: "then side", Doc: `{"kind":"x"}`, Valid: true,
					Why: "control: the condition holds, so the empty `then` applies and the else says nothing"},
			},
		},
		{
			// The neighbouring spellings, one group per allOf branch so that a
			// document naming one trigger exercises that group and no other.
			//
			// Every case here is branch-level, and that was once for want of a
			// per-property case that could fail: a branch's `properties` were
			// merged into the struct as fields, and a field enforced its own type
			// and rules whatever the condition said, so a document constrained by
			// a `then` property was judged the same before and after #209. That
			// unconditional binding was a false rejection of its own -- the schema
			// permits the value where the condition fails -- and issue #213 is it.
			// Since then a `then` property's rules are held back where this group
			// is applied in full, so the per-property half does discriminate;
			// conditional_only_property_positions.json is where it is asked, one
			// row per position, and this fixture stays what it was.
			Name:       "conditional_branch_subschema_shapes",
			SchemaPath: "testdata/schemas/regression/conditional_branch_subschema_shapes.json",
			Instances: []notInstance{
				{Name: "ref rejects", Doc: `{"viaRef":1}`, Valid: false,
					Why: "a bare $ref to a $defs entry, which is how the branch is normally written"},
				{Name: "ref accepts", Doc: `{"viaRef":1,"a":"y"}`, Valid: true, Why: "control for the above"},

				{Name: "ref sibling rejects on the target", Doc: `{"refSibling":1,"b":2}`, Valid: false,
					Why: "the reference demands \"a\"; reading only the sibling would miss it"},
				{Name: "ref sibling rejects on the sibling", Doc: `{"refSibling":1,"a":"y"}`, Valid: false,
					Why: "the `required` beside the reference demands \"b\", and from 2019-09 it binds"},
				{Name: "ref sibling accepts", Doc: `{"refSibling":1,"a":"y","b":2}`, Valid: true, Why: "control for the two above"},

				{Name: "ref chain rejects", Doc: `{"refChain":1}`, Valid: false,
					Why: "a $ref to a $defs entry that is itself a $ref"},
				{Name: "ref chain accepts", Doc: `{"refChain":1,"a":"y"}`, Valid: true, Why: "control for the above"},

				{Name: "ref in the condition selects the branch", Doc: `{"refInIf":1}`, Valid: false,
					Why: "the condition is a $ref carrying a `required`; both hold here, so the consequence demands \"c\""},
				{Name: "ref in the condition is satisfied", Doc: `{"refInIf":1,"c":2}`, Valid: true, Why: "control for the above"},
				{Name: "ref in the condition deselects the branch", Doc: `{"refInIf":1,"x":2,"y":3,"z":4}`, Valid: true,
					Why: "the referenced half of the condition caps the object at three properties and this has four, so the condition fails; a reading that dropped the reference would apply the consequence and refuse a document the schema permits"},

				{Name: "nested then rejects", Doc: `{"nestedThen":1,"inner":2}`, Valid: false,
					Why: "the consequence is itself an if/then, and its own condition holds"},
				{Name: "nested then accepts on the inner consequence", Doc: `{"nestedThen":1,"inner":2,"deep":3}`, Valid: true, Why: "control for the above"},
				{Name: "nested then accepts on the inner condition", Doc: `{"nestedThen":1}`, Valid: true,
					Why: "control: the inner condition fails, so the inner consequence never applies"},

				{Name: "allOf beside a ref rejects on the ref", Doc: `{"allOfBesideRef":1,"b":2}`, Valid: false,
					Why: "the reference demands \"a\" beside the allOf branch"},
				{Name: "allOf beside a ref rejects on the branch", Doc: `{"allOfBesideRef":1,"a":"y"}`, Valid: false,
					Why: "the allOf branch demands \"b\" beside the reference"},
				{Name: "allOf beside a ref accepts", Doc: `{"allOfBesideRef":1,"a":"y","b":2}`, Valid: true, Why: "control for the two above"},

				{Name: "branch minProperties rejects", Doc: `{"bounded":1}`, Valid: false,
					Why: "a branch-level keyword outside object shape, read by nothing"},
				{Name: "branch minProperties accepts", Doc: `{"bounded":1,"x":2,"y":3}`, Valid: true, Why: "control for the above"},

				{Name: "inline required rejects", Doc: `{"inlineReq":1}`, Valid: false,
					Why: "control: the spelling that already worked must go on working"},
				{Name: "inline required accepts", Doc: `{"inlineReq":1,"a":"y"}`, Valid: true, Why: "control for the above"},

				{Name: "no trigger", Doc: `{}`, Valid: true,
					Why: "no group's condition holds for a document naming none of the triggers"},
			},
		},
		{
			// The draft where a $ref replaces what stands beside it. Both branches
			// are references with siblings, and on this dialect the target is the
			// whole of what each says. Enforcing the sibling would be a false
			// reject across four dialects; losing the target is #209.
			Name:       "conditional_branch_ref_draft7",
			SchemaPath: "testdata/schemas/regression/conditional_branch_ref_draft7.json",
			Instances: []notInstance{
				{Name: "then sibling does not bind", Doc: `{"kind":"x"}`, Valid: true,
					Why: "under draft-07 the $ref replaces the branch, so the `required` written beside it says nothing about \"a\""},
				{Name: "then target binds", Doc: `{"kind":"x","a":"y","z":1}`, Valid: false,
					Why: "the target caps the object at two properties, and that is the whole of what the `then` says"},
				{Name: "then target satisfied", Doc: `{"kind":"x","a":"y"}`, Valid: true, Why: "control for the two above"},

				{Name: "else target binds", Doc: `{"c":1}`, Valid: false,
					Why: "the condition fails, so the else applies; its target demands \"b\""},
				{Name: "else sibling does not bind", Doc: `{"b":1}`, Valid: true,
					Why: "the `required` on \"c\" written beside the else's reference is replaced by it"},
			},
		},
		{
			// The same group on an object that declares no properties. The struct
			// built there carries no ObjectConditionals at all, so nothing read the
			// group in part or in whole and every document passed.
			Name:       "conditional_propertyless_object",
			SchemaPath: "testdata/schemas/regression/conditional_propertyless_object.json",
			Instances: []notInstance{
				{Name: "sibling required binds", Doc: `{"kind":"x"}`, Valid: false,
					Why: "the consequence demands \"a\"; an object with no declared properties had the whole group read by nothing"},
				{Name: "target binds", Doc: `{"kind":"x","a":1,"p":2,"q":3}`, Valid: false,
					Why: "the referenced target caps the object at three properties"},
				{Name: "both satisfied", Doc: `{"kind":"x","a":1}`, Valid: true, Why: "control for the two above"},
				{Name: "condition fails", Doc: `{"p":1,"q":2,"r":3,"s":4}`, Valid: true,
					Why: "control: the consequence applies only where the condition holds"},
			},
		},
		{
			// The same position with a group the static reduction reads whole.
			// This is the one that says the two callers are asked their own
			// question: judged by the struct path's gate this group is fully read
			// and would not route, and here there is no reading to have read it.
			Name:       "conditional_propertyless_object_plain",
			SchemaPath: "testdata/schemas/regression/conditional_propertyless_object_plain.json",
			Instances: []notInstance{
				{Name: "consequence binds", Doc: `{"kind":"x"}`, Valid: false,
					Why: "the `then` demands \"a\" and states nothing a reduction could fail to read; the object simply declares no property, and that decided whether the group was enforced"},
				{Name: "consequence satisfied", Doc: `{"kind":"x","a":1}`, Valid: true, Why: "control for the above"},
				{Name: "condition fails", Doc: `{"b":2}`, Valid: true,
					Why: "control: the consequence applies only where the condition holds"},
			},
		},
	}
}

// TestConditionalBranchesAreReadWhole compiles each fixture and puts every
// document to the generated type.
func TestConditionalBranchesAreReadWhole(t *testing.T) {
	runInstanceFixtures(t, "conditional_subschema_test", conditionalSubschemaFixtures())
}
