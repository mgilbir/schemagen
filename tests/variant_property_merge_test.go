package tests

import "testing"

// variantPropertyMergeFixtures are issue #225: a property the root declares and
// a conditional consequence names again.
//
// The allOf merge folds both into one slot of the property map, and when the two
// schemas name different JSON types -- or when one of them names none, which is
// the ordinary way to write a bound -- it folded by erasing every assertion it
// could not reconcile. The consequence's assertions must not bind
// unconditionally, and #213 put them where they belong; but the erasure took the
// *root's* with them, and a schema stating "declared is a string of at least two
// characters" accepted {"declared":"z"} and {"declared":1} alike.
//
// The two directions are what every case here pins, because a fix in either
// direction alone is the other bug:
//
//   - with no trigger present, the root's constraint binds and the
//     consequence's does not;
//   - with the trigger present, both do.
//
// Run compiled. What changed is which check the Validate carries, and a check
// that was never emitted reads exactly like a schema with nothing more to say --
// which is what let this sit under a golden.
func variantPropertyMergeFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "variant_property_merge_positions",
			SchemaPath: "testdata/schemas/regression/variant_property_merge_positions.json",
			Instances: []notInstance{
				// The issue as reported: the consequence states a bound and no
				// type, so the two signatures differ and the erasure fired.
				{Name: "root bound binds with no trigger", Doc: `{"untypedBranch":"z"}`, Valid: false,
					Why: "issue #225: the root states minLength 2 outright and a consequence mentioning the property does not withdraw it"},
				{Name: "root type binds with no trigger", Doc: `{"untypedBranch":1}`, Valid: false,
					Why: "the same erasure took the root's `type`; a number is not a string in any document"},
				{Name: "root bound binds with the trigger too", Doc: `{"untypedTrigger":1,"untypedBranch":"z"}`, Valid: false,
					Why: "the root's constraint is unconditional, so the condition holding changes nothing about it"},
				{Name: "consequence binds where it applies", Doc: `{"untypedTrigger":1,"untypedBranch":"0123456789"}`, Valid: false,
					Why: "the condition holds, so the consequence's maxLength 9 binds alongside the root's minLength"},
				{Name: "consequence does not bind where it does not apply", Doc: `{"untypedBranch":"0123456789"}`, Valid: true,
					Why: "the #213 direction, and the one a fix to the above must not trade away: no trigger, so maxLength says nothing here"},
				{Name: "both satisfied", Doc: `{"untypedTrigger":1,"untypedBranch":"zz"}`, Valid: true,
					Why: "control for the four above"},
				{Name: "property absent", Doc: `{}`, Valid: true,
					Why: "control: every property here is optional"},

				// A consequence naming a different type outright. The merge has
				// nothing to reconcile, and erasing was how it said so.
				{Name: "root binds against an incompatible branch", Doc: `{"typedBranch":"z"}`, Valid: false,
					Why: "the consequence says integer, which is unsatisfiable beside the root's string -- but only where its condition holds, and the root's minLength binds everywhere"},
				{Name: "root satisfied, branch not in play", Doc: `{"typedBranch":"zz"}`, Valid: true,
					Why: "control: no trigger, so the consequence's type says nothing"},
				{Name: "incompatible branch binds where it applies", Doc: `{"typedTrigger":1,"typedBranch":"zz"}`, Valid: false,
					Why: "the condition holds, so the consequence demands an integer and this string cannot satisfy it"},

				// The shape that already worked, because the consequence stated
				// a type and the signatures matched. It is here so that the two
				// stop agreeing loudly rather than quietly.
				{Name: "compatible branch: root binds", Doc: `{"compatibleBranch":"z"}`, Valid: false,
					Why: "control: the root's minLength was never at risk in this shape"},
				{Name: "compatible branch does not bind unconditionally", Doc: `{"compatibleBranch":"zzzz"}`, Valid: true,
					Why: "the consequence's maxLength 3 applies only where its condition holds; this is #213's guarantee and a fix to #225 must leave it standing"},
				{Name: "compatible branch binds where it applies", Doc: `{"compatibleTrigger":1,"compatibleBranch":"zzzz"}`, Valid: false,
					Why: "over-reach control for the row above"},
				{Name: "compatible branch satisfied", Doc: `{"compatibleTrigger":1,"compatibleBranch":"zzz"}`, Valid: true,
					Why: "control for the two above"},

				// The other folding: two enum-like schemas of one JSON type were
				// unioned, which widens the root's list on every document.
				{Name: "root enum binds with no trigger", Doc: `{"enumBoth":"c"}`, Valid: false,
					Why: "the root permits only a and b; the consequence's list was folded into the field's own and made c permissible everywhere"},
				{Name: "root enum satisfied", Doc: `{"enumBoth":"a"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "consequence enum binds where it applies", Doc: `{"enumTrigger":1,"enumBoth":"a"}`, Valid: false,
					Why: "the condition holds, so the consequence's list binds too and it does not name a"},
				{Name: "both enums satisfied", Doc: `{"enumTrigger":1,"enumBoth":"b"}`, Valid: true,
					Why: "control: b is the value both lists name"},

				// The mirror of the first case: the *root* states the bound with
				// no type and the consequence supplies one.
				{Name: "untyped root bound binds", Doc: `{"untypedRoot":"z"}`, Valid: false,
					Why: "minLength 2 is what the root states, whatever the consequence adds"},
				{Name: "untyped root bound satisfied", Doc: `{"untypedRoot":"zz"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "untyped root bound says nothing about a number", Doc: `{"untypedRoot":1}`, Valid: true,
					Why: "minLength constrains strings and nothing else, and no consequence is in play; rejecting this would be the false rejection this family trades for"},
				{Name: "consequence types it where it applies", Doc: `{"untypedRootTrigger":1,"untypedRoot":1}`, Valid: false,
					Why: "over-reach control for the row above: the condition holds, so the consequence's `type` binds"},
			},
		},
	}
}

// TestRootPropertyConstraintSurvivesAVariantMerge puts every document to the
// compiled type.
func TestRootPropertyConstraintSurvivesAVariantMerge(t *testing.T) {
	runInstanceFixtures(t, "variant_property_merge_test", variantPropertyMergeFixtures())
}
