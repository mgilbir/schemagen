package tests

import "testing"

// conditionalOnlyPropertyFixtures are issue #213: a property that reaches a
// merged struct through an if/then/else consequence and through nothing else.
//
// The allOf merge folds a `then` or an `else` branch's `properties` into the
// parent's property map so that the branch can give the field a Go type -- that
// is what the merge is for, and dropping it leaves the value untyped in the
// overflow map. What came with the type was the branch's *rules*: an enum a
// consequence stated became the field's own materialized enum type, and a field
// enforces what it states on every document. So
//
//	{"allOf":[{"if":{"properties":{"kind":{"const":"a"}},"required":["kind"]},
//	          "then":{"properties":{"ev":{"enum":["x","y"]}}}}]}
//
// refused {"kind":"b","ev":"z"} -- the condition fails, the consequence never
// applies, "ev" is unconstrained, and the document is one the schema permits.
//
// The allOf wrapper is the whole defect. Written straight on the root the same
// group reaches no field, because nothing merges it, and always accepted; three
// shapes without the wrapper reproduce nothing at all.
//
// Run compiled rather than compared against a golden, for the reason the `not`
// and the conditional-subschema fixtures are: the two failure directions look
// alike in the emitted source. A dropped rule and a rule that was never there
// read the same, and so do a check that fires correctly and one that fires on
// more documents than the schema names.
func conditionalOnlyPropertyFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "conditional_only_property_positions",
			SchemaPath: "testdata/schemas/regression/conditional_only_property_positions.json",
			Instances: []notInstance{
				// The issue as reported, one trigger key per group.
				{Name: "then does not apply", Doc: `{"thenOnly":"z"}`, Valid: true,
					Why: "issue #213: the condition fails, so the consequence never applies and \"thenOnly\" is unconstrained -- the merged field's enum refused it anyway"},
				{Name: "then applies", Doc: `{"thenTrigger":1,"thenOnly":"z"}`, Valid: false,
					Why: "the sharp control: the condition holds, so the consequence's enum binds and this must still be refused. A fix that drops the field's rules without putting the group somewhere trades the false rejection above for a false acceptance here"},
				{Name: "then applies and is satisfied", Doc: `{"thenTrigger":1,"thenOnly":"x"}`, Valid: true,
					Why: "control for the two above"},

				// null is the same question one keyword along: a property no
				// schema describes unconditionally forbids nothing
				// unconditionally, null included.
				{Name: "null where the consequence does not apply", Doc: `{"thenOnly":null}`, Valid: true,
					Why: "the branch typed it a string, but only for the documents its condition selects; refusing the null in every document is the same false rejection"},
				{Name: "null where the consequence applies", Doc: `{"thenTrigger":1,"thenOnly":null}`, Valid: false,
					Why: "over-reach control for the row above: the enum names no null, and the condition holds"},

				// The `else` mirror. Its consequence is what applies when the
				// trigger is *absent*, so the two rows are the other way round.
				{Name: "else applies", Doc: `{"elseOnly":"z"}`, Valid: false,
					Why: "the condition fails, so the `else` applies and its enum binds"},
				{Name: "else does not apply", Doc: `{"elseTrigger":1,"elseOnly":"z"}`, Valid: true,
					Why: "issue #213 through the other consequence: the condition holds, so the `else` says nothing about \"elseOnly\""},
				{Name: "else applies and is satisfied", Doc: `{"elseOnly":"p"}`, Valid: true,
					Why: "control for the two above"},

				// A group inside a consequence. The evaluator compiles a `then`
				// whole, nested group included, so the contribution is recorded
				// against the outer group and covered by the outer group's check.
				{Name: "nested then applies", Doc: `{"nestedTrigger":1,"inner":1,"nestedOnly":"z"}`, Valid: false,
					Why: "both conditions hold, so the inner consequence binds"},
				{Name: "inner condition fails", Doc: `{"nestedTrigger":1,"nestedOnly":"z"}`, Valid: true,
					Why: "the inner condition fails, so the inner consequence never applies"},
				{Name: "outer condition fails", Doc: `{"nestedOnly":"z"}`, Valid: true,
					Why: "the outer condition fails, so nothing below it applies either"},
				{Name: "nested then satisfied", Doc: `{"nestedTrigger":1,"inner":1,"nestedOnly":"m"}`, Valid: true,
					Why: "control for the three above"},

				// The sharpest control in the fixture. "declared" is written on
				// the root *and* named by a consequence, so it has a schema that
				// binds on every document and one that binds on some; a rule keyed
				// on "a conditional mentioned this property" would drop the first
				// and turn a false rejection into a false acceptance.
				{Name: "root constraint binds with no trigger", Doc: `{"declared":"z"}`, Valid: false,
					Why: "the root states minLength 2 outright, and no consequence being in play does not withdraw it"},
				{Name: "root constraint satisfied", Doc: `{"declared":"zz"}`, Valid: true,
					Why: "control: the consequence's enum must not bind here"},
				{Name: "consequence tightens the declared property", Doc: `{"declTrigger":1,"declared":"zz"}`, Valid: false,
					Why: "the condition holds, so the consequence's enum binds alongside the root's minLength"},
				{Name: "both satisfied", Doc: `{"declTrigger":1,"declared":"ok"}`, Valid: true,
					Why: "control for the two above"},

				// Through a reference, which is how a consequence is ordinarily
				// factored out.
				{Name: "ref consequence does not apply", Doc: `{"refOnly":"z"}`, Valid: true,
					Why: "the consequence is a $ref to a $defs entry; the condition fails, so what the target says about \"refOnly\" says nothing here"},
				{Name: "ref consequence applies", Doc: `{"refTrigger":1,"refOnly":"z"}`, Valid: false,
					Why: "over-reach control for the row above"},
				{Name: "ref consequence satisfied", Doc: `{"refTrigger":1,"refOnly":"r"}`, Valid: true,
					Why: "control for the two above"},

				// The other side of the same reference: not the consequence
				// behind a $ref but the whole group, which is how an allOf branch
				// is usually factored out. The two readings of a group resolve
				// the branch by a different routine than the merge does, and the
				// answer is looked up by node, so this is where those two have to
				// land on the same one.
				{Name: "ref-reached group does not apply", Doc: `{"groupRefOnly":"z"}`, Valid: true,
					Why: "the allOf branch is a $ref to a $defs entry carrying the whole if/then; the condition fails"},
				{Name: "ref-reached group applies", Doc: `{"groupRefTrigger":1,"groupRefOnly":"z"}`, Valid: false,
					Why: "over-reach control for the row above"},
				{Name: "ref-reached group satisfied", Doc: `{"groupRefTrigger":1,"groupRefOnly":"j"}`, Valid: true,
					Why: "control for the two above"},

				// An array property, whose element rules land on no field of
				// their own and are collected separately. They are as conditional
				// as the field's own.
				{Name: "item rules do not apply", Doc: `{"itemsOnly":[{}]}`, Valid: true,
					Why: "the consequence says the elements need an \"id\", and its condition fails; the per-element checks bound anyway"},
				{Name: "item rules apply", Doc: `{"itemsTrigger":1,"itemsOnly":[{}]}`, Valid: false,
					Why: "over-reach control for the row above"},
				{Name: "item rules satisfied", Doc: `{"itemsTrigger":1,"itemsOnly":[{"id":"a"}]}`, Valid: true,
					Why: "control for the two above"},

				// An array whose elements never become a named type, so the
				// per-element rules are collected on the struct rather than
				// dispatched to an item type's Validate. Same question, different
				// list: itemsOnly above exercises the dispatch, this the rules.
				{Name: "element rules do not apply", Doc: `{"elemOnly":["a"]}`, Valid: true,
					Why: "the consequence bounds the elements' length, and its condition fails"},
				{Name: "element rules apply", Doc: `{"elemTrigger":1,"elemOnly":["a"]}`, Valid: false,
					Why: "over-reach control for the row above"},
				{Name: "element rules satisfied", Doc: `{"elemTrigger":1,"elemOnly":["abc"]}`, Valid: true,
					Why: "control for the two above"},

				// A consequence that forbids the property outright. `false` is a
				// schema like any other and is as conditional as the rest, but it
				// reaches the field by its own arm rather than through the rule
				// extractor, so it needs a row of its own.
				{Name: "forbidding consequence does not apply", Doc: `{"forbiddenOnly":1}`, Valid: true,
					Why: "the consequence says no value satisfies this property, and its condition fails; the field refused every document anyway"},
				{Name: "forbidding consequence applies", Doc: `{"falseTrigger":1,"forbiddenOnly":1}`, Valid: false,
					Why: "over-reach control for the row above"},
				{Name: "forbidding consequence with no value", Doc: `{"falseTrigger":1}`, Valid: true,
					Why: "control: `false` forbids the property, not the document"},

				// The fail-closed half, first arm: a group the evaluator declines
				// to compile. The consequence carries a keyword nothing here can
				// read, so nodeBuilder refuses the whole group -- and the lenient
				// static reading refuses it for the same keyword -- which leaves
				// the merged field as the only check the group has.
				{Name: "group the evaluator declines still over-enforces", Doc: `{"declineOnly":"ab"}`, Valid: false,
					Why: "KNOWN FALSE REJECTION, kept deliberately: the condition fails, so the schema permits this, but no reading of the group survived the unmodelled keyword beside it. The narrowing is only ever taken against a group something else applies"},
				{Name: "group the evaluator declines still binds", Doc: `{"declineTrigger":1,"declineOnly":"ab"}`, Valid: false,
					Why: "what the row above is protecting: the condition holds, and the field's rule is the only thing that says so"},
				{Name: "group the evaluator declines is satisfied", Doc: `{"declineTrigger":1,"declineOnly":"abcde"}`, Valid: true,
					Why: "control for the two above"},

				// The fail-closed half, second arm, and the shape is different:
				// here the group is one the evaluator would compile happily.
				// The group sits inside a second allOf, which
				// the merge walks into and which the two readings of a group --
				// collectConditionalRuntimeChecks and extractObjectConditionalDefs
				// -- do not: they look at the schema and its direct allOf branches
				// only. So nothing applies this group with its condition in front
				// of it, and the field's rules are the only thing checking it at
				// all. Withdrawing them there would turn one false rejection into
				// a false acceptance, so they stay.
				{Name: "group below a second allOf still over-enforces", Doc: `{"deepOnly":"z"}`, Valid: false,
					Why: "KNOWN FALSE REJECTION, kept deliberately: the schema permits this -- the condition fails -- but no reading of the group reaches two allOf levels down, so the merged field is the only check there is. Flipping this row is right only together with a reading that gets there"},
				{Name: "group below a second allOf still binds", Doc: `{"deepTrigger":1,"deepOnly":"z"}`, Valid: false,
					Why: "what the row above is protecting: the condition holds and the enum binds, and it is the field that says so"},
			},
		},
		{
			// The applicator this change stops at, kept as a fixture rather than
			// as a sentence so that extending the narrowing to it has to move a
			// row and say why.
			Name:       "conditional_only_property_anyof",
			SchemaPath: "testdata/schemas/regression/conditional_only_property_anyof.json",
			Instances: []notInstance{
				{Name: "other variant matched", Doc: `{"b":1,"branchOnly":"z"}`, Valid: false,
					Why: "KNOWN FALSE REJECTION, kept deliberately: the second variant matches, so \"branchOnly\" is unconstrained -- but the anyOf reduction never applies a variant's property schemas, so withdrawing the field's enum here would leave the row below checked by nothing"},
				{Name: "variant applies", Doc: `{"a":1,"branchOnly":"z"}`, Valid: false,
					Why: "what the row above is protecting: this satisfies neither variant, and the field's enum is part of why it is refused"},
				{Name: "variant satisfied", Doc: `{"a":1,"branchOnly":"g"}`, Valid: true,
					Why: "control: the first variant matches outright"},
				{Name: "no variant", Doc: `{"c":1}`, Valid: false,
					Why: "control: neither variant's required key is present"},
			},
		},
	}
}

// TestConditionalOnlyPropertyDoesNotBindUnconditionally puts every document to
// the compiled type.
func TestConditionalOnlyPropertyDoesNotBindUnconditionally(t *testing.T) {
	runInstanceFixtures(t, "conditional_property_binding_test", conditionalOnlyPropertyFixtures())
}
