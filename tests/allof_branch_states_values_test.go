package tests

import (
	"strings"
	"testing"
)

// allOfBranchStatesValuesFixtures are issue #242: an allOf branch stating the
// only enum or const on an inline schema.
//
// allOf is an in-place applicator, so a branch is asserted of the very instance
// the schema is asserted of, and a branch listing the admissible values lists
// them for the schema. Where the schema states a "type" and no values of its own,
// the arm that reads the type answered the whole schema from it and the branch
// was dropped entire:
//
//	{"properties":{"p":{"type":"string","allOf":[{"enum":["a","b"]}]}}}
//	{"properties":{"p":{"type":"string","allOf":[{"const":"x"}]}}}
//
// both typed p a plain *string with a Validate of `return nil`, so every string
// was accepted -- including the ones the branch names to forbid.
//
// It is the position that lost them and not the schema. The identical sub-schema
// written as a $defs entry and referenced, written as the whole document, or
// written in a 2020-12 tuple slot reaches generateTypeDef, whose allOf arm hands
// it to generateAllOfDef and has always come out an enum type. namedEntry and
// tupleSlot below are those controls, and they were already right; propEnum,
// mapValue and items are the three positions that were not.
//
// Distinct from #238, where the property states an enum of its own and the branch
// narrows it. There the enum arms claim the schema before this decision is
// reached, and the answer is those arms standing down; here there is no list to
// narrow and the arms never fired. Nothing in this group states values on both
// sides of the allOf, so the two do not overlap.
//
// Nothing is intersected for this. Naming the position routes it to
// generateAllOfDef, which is this repository's one reading of what a conjunction
// means -- it folds the branches' value lists together and answers an empty
// result with the forbidding wrapper, which is the whole of the _empty group
// below.
//
// Run compiled rather than compared against a golden, for #231's reason: a
// property typed *string whose Validate reads `return nil` is exactly what a
// schema with nothing left to say produces, so the defect is not legible as a
// diff. Every row that pins a rejection is paired with the acceptance that
// separates a fix from an amputation.
func allOfBranchStatesValuesFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "allof_branch_states_values",
			SchemaPath: "testdata/schemas/regression/allof_branch_states_values.json",
			Instances: []notInstance{
				// The issue exactly as reported, in both spellings.
				{Name: "branch enum refuses an unlisted string", Doc: `{"propEnum":"zzz"}`, Valid: false,
					Why: "issue #242: the branch names a and b, and zzz is neither; the property came out *string with no check at all"},
				{Name: "branch enum admits a listed member", Doc: `{"propEnum":"a"}`, Valid: true,
					Why: "control for the row above -- a fix that merely forbade the position would refuse this too"},
				{Name: "branch enum admits its other member", Doc: `{"propEnum":"b"}`, Valid: true,
					Why: "control: both members of the branch's list are legal, not just the first"},
				{Name: "branch const refuses another string", Doc: `{"propConst":"zzz"}`, Valid: false,
					Why: "issue #242 in its const spelling: the branch pins x, and a const is an enum of one"},
				{Name: "branch const admits its value", Doc: `{"propConst":"x"}`, Valid: true,
					Why: "control for the row above"},

				// The same shape at each position that reaches the inline
				// ladders. A map value and an element are typed by resolveType
				// and a property by resolvePropertyType, which falls through to
				// it; all three were dropping the branch.
				{Name: "map value refuses an unlisted string", Doc: `{"mapValue":{"k":"zzz"}}`, Valid: false,
					Why: "the same sub-schema as an additionalProperties value: it typed the map map[string]string"},
				{Name: "map value admits a listed member", Doc: `{"mapValue":{"k":"a"}}`, Valid: true,
					Why: "control for the row above"},
				{Name: "element refuses an unlisted string", Doc: `{"items":["zzz"]}`, Valid: false,
					Why: "the same sub-schema as an items schema: it typed the field []string"},
				{Name: "element admits a listed member", Doc: `{"items":["a"]}`, Valid: true,
					Why: "control for the row above"},

				// The two positions that were already right, and are the reason
				// this is a defect of the position rather than of the schema.
				{Name: "tuple slot refuses an unlisted string", Doc: `{"tupleSlot":["zzz"]}`, Valid: false,
					Why: "control: a 2020-12 prefixItems slot is named by tupleItemDefFor and has always reached generateAllOfDef"},
				{Name: "tuple slot admits a listed member", Doc: `{"tupleSlot":["a"]}`, Valid: true,
					Why: "control for the row above"},
				{Name: "named entry refuses an unlisted string", Doc: `{"namedEntry":"zzz"}`, Valid: false,
					Why: "control: the identical sub-schema as a $defs entry, which generateTypeDef has always answered with an enum type"},
				{Name: "named entry admits a listed member", Doc: `{"namedEntry":"a"}`, Valid: true,
					Why: "control for the row above"},

				// Each scalar the values can be carried as, since the Go type
				// the members become constants of differs for every one and a
				// list that fits none of them does not compile.
				{Name: "integer branch refuses an unlisted number", Doc: `{"intEnum":3}`, Valid: false,
					Why: "the branch names 1 and 2"},
				{Name: "integer branch admits a listed member", Doc: `{"intEnum":2}`, Valid: true,
					Why: "control for the row above"},
				{Name: "number branch refuses another number", Doc: `{"numberConst":2.5}`, Valid: false,
					Why: "the branch pins 1.5"},
				{Name: "number branch admits its value", Doc: `{"numberConst":1.5}`, Valid: true,
					Why: "control for the row above"},
				{Name: "boolean branch refuses the other boolean", Doc: `{"boolConst":false}`, Valid: false,
					Why: "the branch pins true, and a boolean has exactly one other value -- which is what makes this the sharpest of the scalars"},
				{Name: "boolean branch admits its value", Doc: `{"boolConst":true}`, Valid: true,
					Why: "control for the row above"},

				// The branches this has to see are the branches
				// mergeAllOfBranches will merge: a $ref is followed and a nested
				// allOf is descended, or the merge applies a list the position
				// was never named for.
				{Name: "branch reached through a ref refuses", Doc: `{"branchIsRef":"zzz"}`, Valid: false,
					Why: "the branch is {\"$ref\":\"#/$defs/Values\"} and the target is where the enum is written"},
				{Name: "branch reached through a ref admits", Doc: `{"branchIsRef":"a"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "branch nesting an allOf refuses", Doc: `{"branchNestsAllOf":"zzz"}`, Valid: false,
					Why: "the values are one level further down, in the branch's own allOf"},
				{Name: "branch nesting an allOf admits", Doc: `{"branchNestsAllOf":"a"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "a ref target's own allOf refuses", Doc: `{"refTargetNestsAllOf":"zzz"}`, Valid: false,
					Why: "the two combined: the branch is a $ref, and the values are in the target's own allOf rather than on the target itself -- the descent through a resolved reference, which neither of the rows above reaches"},
				{Name: "a ref target's own allOf admits", Doc: `{"refTargetNestsAllOf":"a"}`, Valid: true,
					Why: "control for the row above"},

				// The shape that has caught a predicate reading only the first
				// branch: the one that states the values is not the one written
				// first. Both orders are run, because a walk that stops at the
				// first branch passes one of them.
				{Name: "a later branch states the values", Doc: `{"laterBranchStatesValues":"zzz"}`, Valid: false,
					Why: "the first branch states minLength alone and takes no value away; a walk that stopped there would have left this accepting every string"},
				{Name: "a later branch's values are admitted", Doc: `{"laterBranchStatesValues":"a"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "the first branch still carries its own bound", Doc: `{"laterBranchStatesValues":""}`, Valid: false,
					Why: "the sharp control: naming the position must not lose the other branch, whose minLength 1 refuses the empty string"},
				{Name: "the first branch states the values", Doc: `{"firstBranchStatesValues":"zzz"}`, Valid: false,
					Why: "the same two branches written the other way round; an answer that depends on their order is answering about the document's layout"},
				{Name: "the first branch's values are admitted", Doc: `{"firstBranchStatesValues":"a"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "the later bound still binds", Doc: `{"firstBranchStatesValues":""}`, Valid: false,
					Why: "control for the row above, from the other side"},

				// Two branches that both state values are two statements about
				// one instance, so the answer is their intersection and not
				// either list.
				{Name: "a value only the first branch lists", Doc: `{"twoBranchesStateValues":"a"}`, Valid: false,
					Why: "the branches name [a,b] and [b,c]; a is on one list and not the other"},
				{Name: "a value only the second branch lists", Doc: `{"twoBranchesStateValues":"c"}`, Valid: false,
					Why: "the mirror of the row above, and the control that a fix taking the last branch's list would fail"},
				{Name: "the value both branches list", Doc: `{"twoBranchesStateValues":"b"}`, Valid: true,
					Why: "control for the two above: b is the whole of the intersection"},

				// Two spellings of one JSON number are one member of a set.
				{Name: "number spellings intersect", Doc: `{"numberSpellings":1}`, Valid: true,
					Why: "one branch lists 1.0 and the other 1; JSON says those are one number, so the intersection holds it"},
				{Name: "a number on one list only", Doc: `{"numberSpellings":2}`, Valid: false,
					Why: "control for the row above: 2 is on the first branch's list alone"},

				// Controls that nothing was taken from the schemas this does not
				// claim.
				{Name: "a branch stating no values keeps its bound", Doc: `{"nonValueBranch":"abc"}`, Valid: false,
					Why: "control: {\"type\":\"string\",\"allOf\":[{\"minLength\":5}]} is enforced by allOfConstraintRules and must go on being"},
				{Name: "a branch stating no values admits", Doc: `{"nonValueBranch":"abcdef"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "a branch restating the type admits every string", Doc: `{"branchAgreesWithType":"anything"}`, Valid: true,
					Why: "control: the branch says only what the schema already says, so nothing may be refused"},
				{Name: "a branch restating the type still refuses a number", Doc: `{"branchAgreesWithType":1}`, Valid: false,
					Why: "control for the row above: the type itself still binds"},
				{Name: "an untyped schema was already right", Doc: `{"untypedBranchValues":"zzz"}`, Valid: false,
					Why: "control: with no \"type\" to read, boxedInferredType already named the position and the branch was already enforced"},
				{Name: "an untyped schema admits a listed member", Doc: `{"untypedBranchValues":"a"}`, Valid: true,
					Why: "control for the row above"},

				// The positions this deliberately does not claim, pinned as the
				// under-enforcement they are so that a later widening has to say
				// so here. An object or an array member cannot be a Go constant,
				// so carrying the list would mean replacing the map or the slice
				// with a json.RawMessage alias; a nullable scalar is claimed by an
				// arm that runs ahead of this one.
				{Name: "an object-shaped position does not carry the list", Doc: `{"objectShaped":{"z":9}}`, Valid: true,
					Why: "the documented scope line: the branch names {\"k\":1} and forbids this, but the position keeps map[string]any and the branch goes unenforced"},
				{Name: "an array-shaped position does not carry the list", Doc: `{"arrayShaped":[9]}`, Valid: true,
					Why: "the same line for []any"},
				{Name: "a nullable scalar does not carry the list", Doc: `{"nullableScalar":"zzz"}`, Valid: true,
					Why: "the same: [\"string\",\"null\"] is claimed by the nullable arm ahead of this one, which answers *string"},
				{Name: "a type name Go cannot name does not carry the list", Doc: `{"unknownTypeName":"zzz"}`, Valid: true,
					Why: "the third of the same line: a type name outside the seven maps to Go `any`, which is not a scalar the members can be constants of -- the position keeps `any`, and `any` carries no Validate for the branch to live in"},
				// A composition written beside the allOf is one of
				// allOfNeedsNamedType's disqualifying keywords, and the arm is
				// asked behind them rather than in front. Moving it in front does
				// claim these -- and would fix them, by sending the whole schema
				// to the runtime evaluator, which carries the oneOf this position
				// is also dropping today. That second keyword is a gap of the
				// composition arms and not of this one, and reaching around them
				// to fix it is a larger change than the one this makes; the
				// residue is pinned here so that a later widening has to say so.
				{Name: "a oneOf beside the allOf leaves the values unenforced", Doc: `{"oneOfBeside":"zzz"}`, Valid: true,
					Why: "the documented residue: the branch names a and bb and forbids this, and so does the oneOf, and the position carries neither"},
				{Name: "a oneOf beside the allOf admits what both permit", Doc: `{"oneOfBeside":"a"}`, Valid: true,
					Why: "control for the row above: whatever else moves, this must stay valid -- it is on the branch's list and satisfies exactly one oneOf branch"},
				{Name: "an anyOf beside the allOf leaves the values unenforced", Doc: `{"anyOfBeside":"zzz"}`, Valid: true,
					Why: "the same residue for the other composition"},
				{Name: "an anyOf beside the allOf admits what both permit", Doc: `{"anyOfBeside":"bb"}`, Valid: true,
					Why: "control for the row above"},

				{Name: "a type union was already right", Doc: `{"typeUnion":"zzz"}`, Valid: false,
					Why: "control, and not a gap: no single Go type holds a string and an integer, so multiTypeUnionType names the position ahead of this arm and the branch's list already reached it"},
				{Name: "a type union admits a listed member", Doc: `{"typeUnion":1}`, Valid: true,
					Why: "control for the row above"},
			},
		},
		{
			Name:       "allof_branch_states_values_empty",
			SchemaPath: "testdata/schemas/regression/allof_branch_states_values_empty.json",
			Instances: []notInstance{
				// A conjunction with nothing in it. The answer is a type that
				// refuses every value, which is what generateAllOfDef already
				// produced for {"allOf":[false]} and what naming the position
				// routes it to -- these ladders had no forbidding path of their
				// own, which is the reason the intersection is not done here.
				{Name: "disjoint branch enums admit nothing", Doc: `{"disjointBranchEnums":"a"}`, Valid: false,
					Why: "one branch permits only a and the other only b; the intersection is empty"},
				{Name: "disjoint branch enums admit nothing the other way", Doc: `{"disjointBranchEnums":"b"}`, Valid: false,
					Why: "the mirror of the row above -- a fix that took either branch's list would accept one of these"},
				{Name: "a branch const outside a branch enum admits nothing", Doc: `{"branchConstAgainstBranchEnum":"a"}`, Valid: false,
					Why: "the const pins a and the enum permits b and c; the spellings crossed"},
				{Name: "the same emptiness from the enum's side", Doc: `{"branchConstAgainstBranchEnum":"b"}`, Valid: false,
					Why: "the mirror of the row above"},
				{Name: "values outside the declared type admit nothing", Doc: `{"valuesOutsideDeclaredType":1}`, Valid: false,
					Why: "the schema says integer and the branch enumerates strings, so no value is both -- and 1 is the one an unfiltered list would admit"},
				{Name: "an empty enum branch admits nothing", Doc: `{"emptyEnumBranch":"a"}`, Valid: false,
					Why: "\"enum\": [] asserts that the instance equals one of no values; statesEnumOrConst reads the field rather than its length, which is what tells it from an absent enum"},
				{Name: "an empty enum on the schema itself admits nothing", Doc: `{"ownEnumIsEmpty":"a"}`, Valid: false,
					Why: "the same emptiness written on the schema rather than in the branch; the forbidding arms claim it before this one is asked, which is what statesEnumOrConst leaves them -- see TestAnEmptyEnumOfItsOwnKeepsTheForbiddingPath"},
				{Name: "integers float64 conflates admit nothing", Doc: `{"bigIntegerBranches":9007199254740992}`, Valid: false,
					Why: "the branches name 9007199254740992 and 9007199254740993, which are one float64 and two JSON numbers; an intersection folding through float64 would admit this"},
				{Name: "the other integer of the pair is out too", Doc: `{"bigIntegerBranches":9007199254740993}`, Valid: false,
					Why: "the other side of the row above"},

				// The controls that separate the forbidding type from an
				// amputation of the document.
				{Name: "an empty conjunction does not forbid the object", Doc: `{"satisfiable":"a"}`, Valid: true,
					Why: "the unsatisfiable properties are optional, so a document omitting them is valid and the rest of the object is judged normally"},
				{Name: "the rest of the object is still judged", Doc: `{"satisfiable":"zzz"}`, Valid: false,
					Why: "control for the row above: satisfiable's own branch permits a and b"},
			},
		},
		{
			// A document whose metaschema declares its vocabularies and leaves
			// the validation one out. `enum` is a keyword of that vocabulary, so
			// it asserts nothing here -- and a branch stating one asserts nothing
			// either. Every other value arm stands down for this, and an arm that
			// did not would refuse documents the schema permits, which is the one
			// failure this repository treats as worse than a missing check.
			Name:       "allof_branch_states_values_no_vocabulary",
			SchemaPath: "testdata/schemas/regression/allof_branch_states_values_no_vocabulary.json",
			Instances: []notInstance{
				{Name: "a branch enum asserts nothing without the vocabulary", Doc: `{"branchValues":"zzz"}`, Valid: true,
					Why: "the validation vocabulary is not declared, so enum is not a keyword of this dialect and the branch names no forbidden value"},
				{Name: "a schema's own enum asserts nothing either", Doc: `{"ownValues":"zzz"}`, Valid: true,
					Why: "the control that says which answer is the right one: the same list written directly on a property is already ignored here, and a branch's may not be treated differently"},
				{Name: "the type still binds", Doc: `{"branchValues":1}`, Valid: false,
					Why: "control: `type` is a keyword of the validation vocabulary too, but the Go type the property is given carries it in the decoder, so a number is still refused"},
			},
		},
		{
			// A branch reached through a $ref that leads back to itself. The walk
			// follows a reference because the merge does, so a cycle is reachable
			// with two keywords; without the on-path set the recursion does not
			// end, and a stack overflow is not a wrong answer but no answer at
			// all. Both halves are run: the cycle that states values on the way
			// round, so the walk has to reach them before it stops, and the one
			// that states none, so stopping is not mistaken for finding some.
			Name:       "allof_branch_states_values_cycle",
			SchemaPath: "testdata/schemas/regression/allof_branch_states_values_cycle.json",
			Instances: []notInstance{
				{Name: "values found through a cycle bind", Doc: `{"throughCycle":"zzz"}`, Valid: false,
					Why: "the branch is a $ref to a definition that states an enum and refers to itself; the enum still binds"},
				{Name: "values found through a cycle admit", Doc: `{"throughCycle":"a"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "a cycle stating no values takes nothing away", Doc: `{"cycleOnly":"anything"}`, Valid: true,
					Why: "control: the definition and its branch say nothing about which values are legal, so every string is still one"},
			},
		},
		{
			// A position whose Go type comes from its "format" is not one of the
			// scalars a value list can be carried at: the value is a time.Time,
			// and the two ladders read the format at different points -- before
			// delegating in resolvePropertyType, after it in resolveType -- so
			// claiming it would leave a property and a map value written from one
			// sub-schema with different Go types. The branch goes unenforced at
			// all three instead, which is what it had. Draft 7, because that is a
			// dialect where format asserts without a flag.
			Name:       "allof_branch_states_values_format_draft7",
			SchemaPath: "testdata/schemas/regression/allof_branch_states_values_format_draft7.json",
			Instances: []notInstance{
				{Name: "a branch const beside a format is not carried at a map value", Doc: `{"stampMap":{"k":"2021-06-06T00:00:00Z"}}`, Valid: true,
					Why: "the documented scope line: the branch pins 2020-01-01T00:00:00Z and forbids this, but the position keeps time.Time and the branch goes unenforced"},
				{Name: "the format still asserts at that map value", Doc: `{"stampMap":{"k":"not-a-date"}}`, Valid: false,
					Why: "control: what the position keeps is the format's own Go type, and draft 7 asserts format"},
				{Name: "the same at an element", Doc: `{"stampList":["2021-06-06T00:00:00Z"]}`, Valid: true,
					Why: "the same line one position over"},
				{Name: "the format still asserts at that element", Doc: `{"stampList":["not-a-date"]}`, Valid: false,
					Why: "control for the row above"},
				{Name: "a map value with no format does carry the branch", Doc: `{"plainMap":{"k":"other"}}`, Valid: false,
					Why: "the sharp control beside them: the same position without a format is claimed, so what stands the arm down is the format and not the draft or the position"},
				{Name: "that map value admits the branch's value", Doc: `{"plainMap":{"k":"keep"}}`, Valid: true,
					Why: "control for the row above"},
			},
		},
		{
			// #151: through draft 7 a $ref replaces every keyword written beside
			// it, so the schema states no allOf either and there is nothing for a
			// branch to say. The position must stay the reference's target. This
			// arm needs no gate of its own for that -- a schema carrying any
			// reference is disqualified before the question is asked -- and this
			// is what says so.
			Name:       "allof_branch_states_values_ref_displaces",
			SchemaPath: "testdata/schemas/regression/allof_branch_states_values_ref_displaces.json",
			Instances: []notInstance{
				{Name: "the branch is displaced with the rest", Doc: `{"displaced":"zzz"}`, Valid: true,
					Why: "issue #151: on draft 7 the $ref replaces its siblings, so the allOf is not written at all and the target admits every string"},
				{Name: "the target still binds", Doc: `{"displaced":1}`, Valid: false,
					Why: "control: the reference's own type is what the position is held to"},
			},
		},
		{
			// #153: from 2019-09 on the reference and its siblings both bind, and
			// the merge generateTypeDef synthesizes for the pair is the only
			// place both can live. The position must carry the target's minLength
			// and the branch's list at once.
			Name:       "allof_branch_states_values_ref_merges",
			SchemaPath: "testdata/schemas/regression/allof_branch_states_values_ref_merges.json",
			Instances: []notInstance{
				{Name: "the target's bound binds", Doc: `{"merged":"xy"}`, Valid: false,
					Why: "issue #153: the target states minLength 5, which two characters do not meet"},
				{Name: "the branch's list binds", Doc: `{"merged":"zzzzzz"}`, Valid: false,
					Why: "the other half: the branch permits abcdef and xy, and this is neither"},
				{Name: "both are satisfied", Doc: `{"merged":"abcdef"}`, Valid: true,
					Why: "control for the two above: abcdef is on the branch's list and is long enough"},
			},
		},
	}
}

// TestAllOfBranchStatesTheOnlyValues compiles each fixture and puts every
// document to the generated type.
//
// Reading the generated source is not enough: the defect's symptom is a Validate
// that reads `return nil`, which is also what a schema with nothing to check
// produces, and that is how it sat under a passing golden suite.
func TestAllOfBranchStatesTheOnlyValues(t *testing.T) {
	runInstanceFixtures(t, "allof_branch_states_values_test", allOfBranchStatesValuesFixtures())
}

// TestAnEmptyEnumOfItsOwnKeepsTheForbiddingPath is the one half of the arm's
// condition no document can report on.
//
// A schema spelled `"enum": []` states its values and lists none, so
// statesEnumOrConst answers yes for it and the arm stands down. It is the only
// shape that can reach that question: a non-empty enum and a const are refused by
// allOfNeedsNamedType's disqualifying keywords before it is asked. Dropping the
// clause routes the position here instead, and what comes out still refuses every
// value -- both paths are correct about the document, and the fixture group above
// pins that they are. What moves is the Go type: the property stops being the
// *string the forbidding-property check is emitted against and becomes a wrapper
// of its own. Only the source says so, so this is where it is said.
func TestAnEmptyEnumOfItsOwnKeepsTheForbiddingPath(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/allof_branch_states_values_empty.json"))
	if strings.Contains(src, "type RootOwnEnumIsEmpty ") {
		t.Errorf("a property whose own enum is empty should keep the type the forbidding arms give it, not be materialized under a name by the branch-values arm; got:\n%s", src)
	}
	if !strings.Contains(src, `OwnEnumIsEmpty `) || !strings.Contains(src, `json:"ownEnumIsEmpty,omitempty"`) {
		t.Errorf("the property should still be there and still be the pointer the forbidding-property check is emitted against; got:\n%s", src)
	}
	// The neighbouring property in the same document is the control: a branch
	// stating the only values *is* materialized, so a run in which nothing is
	// materialized would not pass this by doing nothing.
	if !strings.Contains(src, "type RootSatisfiable string") {
		t.Errorf("the branch-values arm should still have materialized `satisfiable`; got:\n%s", src)
	}
}

// TestBranchValuesStandDownWithoutTheValidationVocabulary is the other half of
// the arm's condition a document cannot report on.
//
// Where the declared vocabularies leave the validation one out, `enum` is not a
// keyword of the dialect and asserts nothing -- which the fixture group above
// pins, and which holds with the gate removed too, because the emitted Validate
// is `return nil` either way. What the gate decides is whether the position is
// given a named type for a keyword that says nothing: without it the arm mints
// `type RootBranchValues string` where the same list written directly on a
// property is left alone, and this arm would be the only value arm in the file
// not asking the question.
func TestBranchValuesStandDownWithoutTheValidationVocabulary(t *testing.T) {
	src := string(generateFromSchema(t, "testdata/schemas/regression/allof_branch_states_values_no_vocabulary.json"))
	if strings.Contains(src, "type RootBranchValues ") {
		t.Errorf("a branch's enum should mint no type where the validation vocabulary is not declared, since a property's own enum mints none either; got:\n%s", src)
	}
	if !strings.Contains(src, `json:"branchValues,omitempty"`) || !strings.Contains(src, `json:"ownValues,omitempty"`) {
		t.Errorf("both properties should still be there; got:\n%s", src)
	}
}
