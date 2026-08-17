package tests

import "testing"

// The two unevaluated keywords inside an `allOf` branch, where what the branch
// evaluated is produced by an applicator the static per-branch reading cannot
// see through.
//
// Issue #342, and it was a *false rejection* -- the direction that costs a
// document the schema permits, and the rarer of the two failure modes this
// family has. The per-branch check (collectBranchOverflowChecks ->
// buildBranchUnevalCheck) collected the branch's own `properties`,
// `patternProperties`, `additionalProperties`, its `$ref` target and its nested
// `allOf`, and had no arm for `anyOf`, `oneOf`, `if`/`then`/`else` or
// `dependentSchemas`. A branch whose evaluated set came only from one of those
// four therefore accounted for nothing at all: the emitted loop reduced to
//
//	_bAcct := false
//	if !_bAcct { return fmt.Errorf("unevaluatedProperties: property %q is not allowed", k) }
//
// which refuses every key of every non-empty object.
// {"allOf":[{"anyOf":[{"properties":{"a":{"type":"string"}}}],
// "unevaluatedProperties":false}]} rejected {"a":"x"}, which all four
// implementations Bowtie was asked accept, and which the identical schema
// written with the keyword *beside* the allOf has always accepted --
// collectEvaluatedProperties reads the same four applicators there and compiles
// them to ConditionalEval.
//
// The fix routes the shape to the runtime evaluator rather than widening the
// static reading, because a widened static reading could only ever be a union
// over branches and a union is the wrong answer here: see the "second branch is
// the one that holds" case in the anyOf group below, where the union admits a
// key that the branch which actually held did not evaluate. The union is kept
// all the same, as the fallback for a subtree the evaluator declines -- see
// buildBranchUnevalCheck -- because under-enforcing is the direction #111 chose
// for this keyword and a false rejection is the direction it did not.
//
// The pairing rule of tests/unevaluated_regression_test.go applies and is why
// the items mirror is here: the two keywords are answered by separate machinery,
// and every defect in this family so far has been one of the two learning
// something the other did not. This one is exactly that. `unevaluatedItems` was
// already correct in all of these positions, because hasCousinUnevaluatedItems
// routes any applicator branch stating that keyword to the evaluator and
// `unevaluatedProperties` had no such predicate. Sixty shapes of the items half
// were put to Bowtie while this was being reduced and not one of them was wrong,
// so the items group below is a control rather than a repair -- and it is here
// so that the halves diverging again is a failure rather than something nobody
// looks at.
//
// Every verdict below is Bowtie's, taken across python-jsonschema, js-ajv,
// go-jsonschema and rust-boon, and unanimous in all four for every document
// listed. An annotation-dependent keyword is not a thing to assert from reading
// the specification twice.
func unevaluatedBranchApplicatorFixtures() []notFixture {
	return []notFixture{
		{
			// The headline. The valid document is the one that was refused.
			Name:       "properties_anyof_producer",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_properties_anyof_producer.json",
			Instances: []notInstance{
				{Name: "first branch evaluates it", Doc: `{"a":"x"}`, Valid: true,
					Why: "the anyOf's first branch holds and evaluates a, so nothing is left over; " +
						"refusing this is issue #342"},
				{Name: "second branch evaluates it", Doc: `{"b":1}`, Valid: true,
					Why: "the second branch holds and evaluates b; the defect refused this too"},
				{Name: "both branches hold", Doc: `{"a":"x","b":1}`, Valid: true,
					Why: "anyOf collects the annotations of every branch that holds, not just the first"},
				{Name: "empty", Doc: `{}`, Valid: true,
					Why: "no key to be unevaluated"},
				{Name: "the branch that holds evaluated nothing", Doc: `{"a":1}`, Valid: false,
					Why: "a is not a string so branch one fails; branch two holds and evaluates only b, " +
						"so a is unevaluated. This document does not tell the evaluator apart from a " +
						"static union, though it looks as if it should: the merged view types a as a " +
						"string and refuses the integer before either is consulted. " +
						"branch_that_held below is the group that does tell them apart"},
				{Name: "no branch evaluates it", Doc: `{"c":true}`, Valid: false,
					Why: "c is evaluated by neither branch; a fix that simply drops the check loses this"},
			},
		},
		{
			// The mirror, which has always worked. See the note above.
			Name:       "items_anyof_producer",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_items_anyof_producer.json",
			Instances: []notInstance{
				{Name: "first branch evaluates it", Doc: `["a"]`, Valid: true,
					Why: "the anyOf's first branch holds and evaluates index 0"},
				{Name: "second branch evaluates both", Doc: `["a",1]`, Valid: true,
					Why: "the second branch holds and evaluates indices 0 and 1"},
				{Name: "unevaluated tail", Doc: `["a","b"]`, Valid: false,
					Why: "index 1 is a string, so branch two does not hold; branch one evaluates " +
						"index 0 alone and index 1 is left over"},
				{Name: "wrong slot type", Doc: `[1]`, Valid: false,
					Why: "neither branch admits an integer at index 0, so the anyOf itself fails"},
				{Name: "empty", Doc: `[]`, Valid: true,
					Why: "no position to be unevaluated"},
			},
		},
		{
			Name:       "properties_oneof_producer",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_properties_oneof_producer.json",
			Instances: []notInstance{
				{Name: "the branch evaluates it", Doc: `{"a":"x"}`, Valid: true,
					Why: "the oneOf's only branch holds and evaluates a"},
				{Name: "empty", Doc: `{}`, Valid: true, Why: "no key to be unevaluated"},
				{Name: "unevaluated key", Doc: `{"a":"x","b":1}`, Valid: false,
					Why: "b is evaluated by no branch"},
				{Name: "only the unevaluated key", Doc: `{"b":1}`, Valid: false,
					Why: "the branch holds vacuously and evaluates nothing, so b is left over"},
			},
		},
		{
			Name:       "properties_ifthenelse_producer",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_properties_ifthenelse_producer.json",
			Instances: []notInstance{
				{Name: "then branch evaluates it", Doc: `{"a":"x"}`, Valid: true,
					Why: "if holds, so then applies and a is evaluated"},
				{Name: "else branch evaluates it", Doc: `{"b":1}`, Valid: true,
					Why: "if does not hold -- a is absent and required -- so else applies and b is evaluated"},
				{Name: "empty", Doc: `{}`, Valid: true, Why: "no key to be unevaluated"},
				{Name: "then holds and b is left over", Doc: `{"a":"x","b":1}`, Valid: false,
					Why: "if holds, so else never applies and nothing evaluates b"},
				{Name: "else holds and a is left over", Doc: `{"a":1}`, Valid: false,
					Why: "a is not a string so if fails and its annotations are discarded; else " +
						"evaluates only b, leaving a unevaluated"},
			},
		},
		{
			Name:       "properties_dependentschemas_producer",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_properties_dependentschemas_producer.json",
			Instances: []notInstance{
				{Name: "trigger present, evaluates it", Doc: `{"a":"x"}`, Valid: true,
					Why: "a is present, so its dependent schema applies and evaluates a"},
				{Name: "empty", Doc: `{}`, Valid: true,
					Why: "the trigger is absent, and there is no key to be unevaluated either"},
				{Name: "unevaluated key", Doc: `{"a":"x","b":1}`, Valid: false,
					Why: "the dependent schema evaluates a alone, so b is left over"},
				{Name: "trigger absent", Doc: `{"b":1}`, Valid: false,
					Why: "a is absent so the dependent schema never applies and evaluates nothing"},
			},
		},
		{
			// The group that makes the case for routing rather than for reading
			// the branch more cleverly.
			//
			// The two anyOf branches are told apart by a required key, not by the
			// type of the key under test, so the merged view has no quarrel with
			// any of these documents and every verdict below is the unevaluated
			// keyword's alone. {"u":"y","a":"s"} is the document that matters: the
			// second branch is the one that holds, it evaluates u and b, and a is
			// therefore unevaluated and the document refused. The union of what
			// both branches declare contains a and would accept it.
			//
			// So no static reading of this branch can be made exact -- what the
			// branch evaluated is a fact about the document -- and the widened
			// static reading buildBranchUnevalCheck keeps is a fallback and not
			// the answer. All four implementations agree on all six documents.
			Name:       "properties_branch_that_held",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_properties_branch_that_held.json",
			Instances: []notInstance{
				{Name: "first branch holds and evaluates its own", Doc: `{"t":"x","a":"s"}`, Valid: true,
					Why: "branch one holds and evaluates t and a"},
				{Name: "second branch holds and evaluates its own", Doc: `{"u":"y","b":"s"}`, Valid: true,
					Why: "branch two holds and evaluates u and b"},
				{Name: "key belongs to the branch that did not hold", Doc: `{"u":"y","a":"s"}`, Valid: false,
					Why: "branch one needs t and does not hold, so nothing evaluates a. This is the " +
						"document a union over both branches accepts and the evaluator refuses, and " +
						"it is the whole reason the shape is routed rather than read statically"},
				{Name: "the mirror of it", Doc: `{"t":"x","b":"s"}`, Valid: false,
					Why: "branch two needs u and does not hold, so nothing evaluates b"},
				{Name: "both branches hold", Doc: `{"t":"x","u":"y","a":"s","b":"s"}`, Valid: true,
					Why: "anyOf collects from every branch that holds, so all four keys are evaluated"},
				{Name: "no branch holds", Doc: `{"a":"s"}`, Valid: false,
					Why: "neither required key is present, so the anyOf assertion itself fails"},
			},
		},
		{
			// The same shape as the first group, made to take the *other* path.
			//
			// Routing is an offer, not a transfer: runtimeSchemaDef hands back a
			// subtree past its size bounds or stating a keyword it does not model,
			// and the static per-branch check is what runs then. The unrecognised
			// keyword on the branch is what makes it decline here -- a schemagen
			// that has never seen a keyword cannot assume it constrains nothing --
			// so this group is the one that exercises the union
			// buildBranchUnevalCheck collects, and it is the only reason that
			// union is not dead code.
			//
			// Its verdicts are deliberately weaker than the first group's, and the
			// difference is the whole content of the group. {"a":1} is accepted
			// here and refused there: the union contains "a" because some branch
			// declares it, while the evaluator knows that the branch which
			// actually held declared nothing. That is under-enforcement, which is
			// the direction this keyword has been wrong in since #111 and is
			// allowed to be. What must not come back is the row above it --
			// {"a":"x"} refused -- which is #342 and is a document the schema
			// permits.
			//
			// Verdicts other than that one row are Bowtie's across
			// python-jsonschema, go-jsonschema and rust-boon, unanimous. js-ajv
			// declines the document outright over the unrecognised keyword and is
			// not counted.
			Name:       "properties_anyof_producer_evaluator_declines",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_properties_evaluator_declines.json",
			Instances: []notInstance{
				{Name: "the branch evaluates it", Doc: `{"a":"x"}`, Valid: true,
					Why: "the anyOf branch holds and evaluates a. Refusing this is #342 arriving " +
						"by the other path: with no union collected the accounted set is empty " +
						"and every key of every non-empty object is refused"},
				{Name: "empty", Doc: `{}`, Valid: true, Why: "no key to be unevaluated"},
				{Name: "unevaluated key", Doc: `{"a":"x","b":1}`, Valid: false,
					Why: "b is declared by no branch, so it is outside the union too and the " +
						"static check still refuses it"},
				{Name: "only the unevaluated key", Doc: `{"b":1}`, Valid: false,
					Why: "the same, with nothing else in the document"},
				{Name: "the branch that held evaluated nothing", Doc: `{"a":1}`, Valid: false,
					Why: "the anyOf branch requires a to be a string, so the anyOf assertion itself " +
						"fails and the document is refused before the union is consulted"},
			},
		},
		{
			// The other direction, in the same walk. The branch's own keyword sat
			// beside a $ref, and the old walk resolved the branch through that
			// $ref before asking whether it stated the keyword -- so the target
			// was asked, said no, and the branch's keyword was dropped. A false
			// accept, and the only spelling of the pair that was enforced was the
			// one with the keyword on the target.
			Name:       "properties_beside_ref",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_properties_beside_ref.json",
			Instances: []notInstance{
				{Name: "the ref evaluates it", Doc: `{"a":"x"}`, Valid: true,
					Why: "$ref is an adjacent in-place applicator, so what it evaluates reaches the " +
						"keyword written beside it"},
				{Name: "empty", Doc: `{}`, Valid: true, Why: "no key to be unevaluated"},
				{Name: "unevaluated key", Doc: `{"a":"x","b":1}`, Valid: false,
					Why: "b is evaluated by neither the branch nor its $ref target"},
				{Name: "only the unevaluated key", Doc: `{"b":1}`, Valid: false,
					Why: "accepting this was the branch's keyword being dropped entirely"},
				{Name: "wrong property type", Doc: `{"a":1}`, Valid: false,
					Why: "the $ref'd property schema still binds; a fix that only restores the " +
						"overflow check loses it"},
			},
		},
		{
			// The same drop one level down: the old walk read the direct members
			// of allOf and stopped, so a branch stating the keyword inside a
			// nested allOf was never checked, while the identical branch written
			// one level up was.
			Name:       "properties_nested_allof",
			SchemaPath: "testdata/schemas/regression/branch_unevaluated_properties_nested_allof.json",
			Instances: []notInstance{
				{Name: "the branch evaluates it", Doc: `{"a":"x"}`, Valid: true,
					Why: "the branch's own properties evaluates a"},
				{Name: "empty", Doc: `{}`, Valid: true, Why: "no key to be unevaluated"},
				{Name: "unevaluated key", Doc: `{"a":"x","b":1}`, Valid: false,
					Why: "b is unevaluated; accepting it was the nested branch never being reached"},
				{Name: "only the unevaluated key", Doc: `{"b":1}`, Valid: false,
					Why: "the same drop with nothing else in the document"},
				{Name: "wrong property type", Doc: `{"a":1}`, Valid: false,
					Why: "the branch's property schema still binds"},
			},
		},
	}
}

// TestUnevaluatedInBranchWithApplicatorProducer generates, compiles and runs each
// fixture and holds it to the verdicts the schema states.
//
// The assertion is the verdict rather than the emitted text, for the reason the
// rest of this family is run that way: the defect was invisible in the generated
// source. A `_bAcct := false` in an overflow loop is what a branch accounting for
// no keys correctly looks like, and a golden records it without comment.
func TestUnevaluatedInBranchWithApplicatorProducer(t *testing.T) {
	runInstanceFixtures(t, "unevaluated_branch_applicator_test", unevaluatedBranchApplicatorFixtures())
}
