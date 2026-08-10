package tests

import "testing"

// allOfPropertyConjunctionFixtures are issue #231: an allOf branch naming a
// property something else has already described.
//
// allOf is an in-place applicator -- every branch is asserted of the whole
// instance -- so what the parent says about a property and what a branch says
// about it both bind, and the property must satisfy their conjunction. The merge
// assigned instead, so the branch's node went into the slot whole and everything
// already there was gone:
//
//	{"properties":{"declared":{"type":"string","minLength":2}},
//	 "allOf":[{"properties":{"declared":{"maxLength":9}}}]}
//
// accepted {"declared":"z"}, which the root's own minLength forbids -- and the
// root's `type` went with it, so the property was not even held to being a
// string.
//
// This is not the conditional family. #213 and #227 hold that a branch reached
// through an if/then/else must *not* bind unconditionally, and #232 that a
// root-stated constraint survives one that does not apply. Here neither schema
// is conditional, so neither may be dropped and neither may be deferred: the
// answer is the intersection. conditional_property_binding_test.go and
// variant_property_merge_test.go are the fixtures that keep the two apart, and
// they run beside this one.
//
// Every keyword is pinned in both directions -- root tighter and branch tighter
// -- because a conjunction that only ever tightens one side is half of one, and
// half is what was there before: an assignment tightens whenever the branch
// happens to be the stricter of the two, and a fixture that only tested that
// direction would have passed against the defect.
//
// Run compiled rather than compared against a golden. The failure is a check
// that was never emitted, and a Validate missing a check reads exactly like a
// schema with nothing more to say -- which is how this sat under the goldens
// while every one of them passed.
func allOfPropertyConjunctionFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "allof_property_conjunction",
			SchemaPath: "testdata/schemas/regression/allof_property_conjunction.json",
			Instances: []notInstance{
				// The issue exactly as reported.
				{Name: "root bound binds under a branch", Doc: `{"declared":"z"}`, Valid: false,
					Why: "issue #231: the root states minLength 2 and the branch states maxLength 9; both bind, and the branch's node replaced the root's outright"},
				{Name: "branch bound binds over the root", Doc: `{"declared":"0123456789"}`, Valid: false,
					Why: "the other half of the same conjunction: ten characters exceed the branch's maxLength 9"},
				{Name: "both length bounds satisfied", Doc: `{"declared":"ok"}`, Valid: true,
					Why: "control for the two above -- a fix that simply kept the root would refuse this too"},
				{Name: "root type binds under a branch", Doc: `{"declared":1}`, Valid: false,
					Why: "the branch states no type, so the assignment left the property untyped and a number was accepted where the root says string"},

				// The same pair with the sides swapped, so that neither the root
				// nor the branch is the one that always wins.
				{Name: "branch lower bound binds under a root upper bound", Doc: `{"reversed":"z"}`, Valid: false,
					Why: "the root states maxLength 9 and the branch minLength 2; the branch's bound is the one that must survive here"},
				{Name: "root upper bound binds over a branch lower bound", Doc: `{"reversed":"0123456789"}`, Valid: false,
					Why: "control in the other direction: the root's maxLength 9 still applies"},
				{Name: "both reversed bounds satisfied", Doc: `{"reversed":"ok"}`, Valid: true,
					Why: "control for the two above"},

				// Two bounds of the same kind, where the tighter has to win
				// whichever side states it.
				{Name: "tighter length bound is the root's", Doc: `{"lenRootTighter":"abc"}`, Valid: false,
					Why: "root minLength 4 against branch minLength 2: the conjunction is 4, and taking the branch's would accept this"},
				{Name: "tighter length bound satisfied", Doc: `{"lenRootTighter":"abcd"}`, Valid: true,
					Why: "control for the row above"},

				{Name: "tighter minimum is the branch's", Doc: `{"lowBound":7}`, Valid: false,
					Why: "root minimum 5 against branch minimum 10: the conjunction is 10"},
				{Name: "tighter minimum satisfied", Doc: `{"lowBound":10}`, Valid: true,
					Why: "control for the row above"},
				{Name: "tighter minimum is the root's", Doc: `{"lowBoundRootTighter":7}`, Valid: false,
					Why: "root minimum 10 against branch minimum 5: the same answer from the other side"},
				{Name: "tighter minimum from the root satisfied", Doc: `{"lowBoundRootTighter":10}`, Valid: true,
					Why: "control for the row above"},

				{Name: "tighter maximum is the branch's", Doc: `{"highBound":70}`, Valid: false,
					Why: "root maximum 100 against branch maximum 50: the conjunction is 50"},
				{Name: "tighter maximum satisfied", Doc: `{"highBound":40}`, Valid: true,
					Why: "control for the row above"},
				{Name: "tighter maximum is the root's", Doc: `{"highBoundRootTighter":70}`, Valid: false,
					Why: "root maximum 50 against branch maximum 100: the same answer from the other side"},
				{Name: "tighter maximum from the root satisfied", Doc: `{"highBoundRootTighter":40}`, Valid: true,
					Why: "control for the row above"},

				// enum is a set intersection rather than a choice between two
				// lists, and the intersection is the same set whichever list is
				// read first.
				{Name: "branch enum narrows the root's", Doc: `{"enumBranchNarrower":"a"}`, Valid: false,
					Why: "the root permits a, b and c and the branch permits only b; a is in one list and not the other"},
				{Name: "branch enum intersection satisfied", Doc: `{"enumBranchNarrower":"b"}`, Valid: true,
					Why: "control: b is the one value both lists name"},
				{Name: "root enum narrows the branch's", Doc: `{"enumRootNarrower":"a"}`, Valid: false,
					Why: "the mirror: the root permits only b and the branch permits a, b and c"},
				{Name: "root enum intersection satisfied", Doc: `{"enumRootNarrower":"b"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "const narrows an enum", Doc: `{"constAgainstEnum":"p"}`, Valid: false,
					Why: "the root permits p and q; the branch's const pins q, and a const is an enum of one"},
				{Name: "const intersection satisfied", Doc: `{"constAgainstEnum":"q"}`, Valid: true,
					Why: "control for the row above"},

				// type is a set intersection too, and one of its members is a
				// subset of another.
				{Name: "branch type narrows the root's", Doc: `{"typeBranchNarrower":"x"}`, Valid: false,
					Why: "the root admits a string or an integer and the branch only an integer, so a string satisfies one and not both"},
				{Name: "branch type intersection satisfied", Doc: `{"typeBranchNarrower":3}`, Valid: true,
					Why: "control for the row above"},
				{Name: "root type narrows the branch's", Doc: `{"typeRootNarrower":"x"}`, Valid: false,
					Why: "the mirror: the root admits only an integer and the branch a string or an integer"},
				{Name: "root type intersection satisfied", Doc: `{"typeRootNarrower":3}`, Valid: true,
					Why: "control for the row above"},
				{Name: "integer meets number", Doc: `{"numberMeetsInteger":1.5}`, Valid: false,
					Why: "every integer is a number, so \"number\" and \"integer\" together admit the integers -- and 1.5 is not one"},
				{Name: "integer meets number satisfied", Doc: `{"numberMeetsInteger":2}`, Valid: true,
					Why: "control for the row above"},

				// Three statements about one property: the root's and two
				// branches'. A conjunction that only ever held two would drop the
				// oldest as the third arrived.
				{Name: "second branch bound binds", Doc: `{"twice":"abc"}`, Valid: false,
					Why: "the root says minLength 2, the first branch maxLength 9 and the second minLength 4; three characters satisfy the first two and not the third"},
				{Name: "first branch bound still binds after a second", Doc: `{"twice":"0123456789"}`, Valid: false,
					Why: "the sharp control: the first branch's maxLength must survive the second branch naming the same property"},
				{Name: "all three bounds satisfied", Doc: `{"twice":"abcd"}`, Valid: true,
					Why: "control for the two above"},

				// A branch reached through a $ref, which is how a branch is
				// ordinarily factored out. The merge resolves the reference and
				// then merges the target's properties, so the conjunction has to
				// happen on the far side of the hop as well.
				{Name: "root bound binds under a ref-reached branch", Doc: `{"viaRef":"z"}`, Valid: false,
					Why: "the branch is {\"$ref\":\"#/$defs/CapBranch\"} and the target names viaRef; the root's minLength 2 must survive it"},
				{Name: "ref-reached branch bound binds", Doc: `{"viaRef":"0123456789"}`, Valid: false,
					Why: "over-reach control for the row above: the target's maxLength 9 binds too"},
				{Name: "ref-reached conjunction satisfied", Doc: `{"viaRef":"ok"}`, Valid: true,
					Why: "control for the two above"},

				// `pattern` is the keyword the conjunction cannot express: two
				// regexes that must both match are not a regex, and the property
				// has one slot for one. The first stated wins, which under-enforces
				// -- a value the branch's pattern would refuse is let through --
				// rather than refusing values the schema permits. These two rows
				// pin that answer, so the limitation is a recorded behaviour and
				// not a surprise, and they pin the *order* the conjunction is built
				// in, which is what makes the parent's the side that survives.
				{Name: "first pattern binds", Doc: `{"patternFirstWins":"b1"}`, Valid: false,
					Why: "the root states pattern ^a and the branch ^b; the root's is the one kept, so a value matching only the branch's is refused"},
				{Name: "second pattern is not enforced", Doc: `{"patternFirstWins":"a1"}`, Valid: true,
					Why: "the documented gap: the branch's ^b also binds and this value does not match it, but two patterns cannot be written into one slot, so the conjunction under-enforces here"},

				// Two spellings of one JSON number are one member of a set. An
				// intersection that compared the literals as text would find
				// nothing in common between [1.0, 2] and [1, 3].
				{Name: "number spellings intersect", Doc: `{"numberSpelling":1}`, Valid: true,
					Why: "the root lists 1.0 and the branch lists 1; JSON says those are one number, so the intersection holds it"},
				{Name: "number outside the intersection", Doc: `{"numberSpelling":2}`, Valid: false,
					Why: "control for the row above: 2 is on the root's list alone"},

				// Nested properties, where the conjunction has to recurse: the
				// two schemas describe the same object, so its required lists
				// union and its members' own schemas conjoin in turn.
				{Name: "branch required key missing", Doc: `{"nested":{"a":"ok"}}`, Valid: false,
					Why: "the root requires a and the branch requires b; both lists bind on the same object"},
				{Name: "root required key missing", Doc: `{"nested":{"b":1}}`, Valid: false,
					Why: "the mirror of the row above"},
				{Name: "nested required union satisfied", Doc: `{"nested":{"a":"ok","b":1}}`, Valid: true,
					Why: "control for the two above"},
				{Name: "nested root bound binds", Doc: `{"nested":{"a":"z","b":1}}`, Valid: false,
					Why: "the recursion: the root gives nested.a minLength 2 and the branch gives it maxLength 9"},
				{Name: "nested branch bound binds", Doc: `{"nested":{"a":"0123456789","b":1}}`, Valid: false,
					Why: "the other half of the nested conjunction"},

				// The control that says nothing moved for a property only a
				// branch names. There is nothing to conjoin, and the branch's
				// schema is the whole answer exactly as it was.
				{Name: "branch-only property still enforced", Doc: `{"branchOnly":"z"}`, Valid: false,
					Why: "no other schema names branchOnly, so the branch's minLength 2 stands alone"},
				{Name: "branch-only property satisfied", Doc: `{"branchOnly":"ok"}`, Valid: true,
					Why: "control for the row above"},
			},
		},
		{
			Name:       "allof_property_conjunction_empty",
			SchemaPath: "testdata/schemas/regression/allof_property_conjunction_empty.json",
			Instances: []notInstance{
				// A conjunction with nothing in it. The decision is a type that
				// refuses every value rather than a generation-time error: the
				// document is a legal schema, it merely admits no value at that
				// position, and an object that omits the property is still valid.
				// That is what a boolean `false` at a property position has always
				// produced.
				{Name: "disjoint types admit nothing", Doc: `{"disjointTypes":"x"}`, Valid: false,
					Why: "the root says string and the branch says integer; no value is both"},
				{Name: "disjoint types admit no integer either", Doc: `{"disjointTypes":1}`, Valid: false,
					Why: "the other side of the same emptiness -- a fix that merely kept one of the two types would accept one of these"},
				{Name: "disjoint enums admit nothing", Doc: `{"disjointEnums":"a"}`, Valid: false,
					Why: "the root permits only a and the branch only b; the intersection is empty"},
				{Name: "disjoint enums admit nothing the other way", Doc: `{"disjointEnums":"b"}`, Valid: false,
					Why: "the mirror of the row above"},
				{Name: "enum outside the declared type admits nothing", Doc: `{"enumOutsideType":1}`, Valid: false,
					Why: "the root says string and the branch enumerates numbers; the conjunction names no value"},

				// The same emptiness beside a type both sides agree on. It is the
				// sharp one: with a type to read, the merge produces a plain
				// string alias and the runtime evaluator is never asked, so an
				// empty intersection recorded as *no* intersection -- a nil enum
				// rather than an empty one -- silently becomes "nothing was said"
				// and the property accepts every string.
				{Name: "typed disjoint enums admit nothing", Doc: `{"typedDisjointEnums":"a"}`, Valid: false,
					Why: "both sides say string, the root permits only a and the branch only b"},
				{Name: "typed disjoint enums admit nothing the other way", Doc: `{"typedDisjointEnums":"b"}`, Valid: false,
					Why: "the mirror of the row above"},

				// Two integers float64 cannot tell apart. Their enums share no
				// member, and an intersection that compared them through float64
				// would believe they do -- which is #215/#216/#220's failure
				// reached by a new route.
				{Name: "integers float64 conflates admit nothing", Doc: `{"bigIntegerEnums":9007199254740992}`, Valid: false,
					Why: "the root permits 9007199254740992 and the branch 9007199254740993; the two are one float64 and two JSON numbers, so the intersection is empty"},
				{Name: "the branch's integer is out too", Doc: `{"bigIntegerEnums":9007199254740993}`, Valid: false,
					Why: "the other side of the row above"},
				{Name: "an empty conjunction does not forbid the object", Doc: `{"satisfiable":"ok"}`, Valid: true,
					Why: "the sharp control: the unsatisfiable properties are optional, so a document that omits them is valid and the rest of the object is judged normally"},
				{Name: "the rest of the object is still judged", Doc: `{"satisfiable":"z"}`, Valid: false,
					Why: "control for the row above: satisfiable's own conjunction is minLength 2 and maxLength 9"},
			},
		},
		{
			// propertyNames is the same intersection one level out: it is a
			// sub-schema rather than a value, and mergePropertyNames folds the
			// two through mergeConstraints. That fold used to end with a patch
			// that re-read src's enum whenever the combined one was empty --
			// which is also what a *narrowed to nothing* intersection looks like,
			// so the schema that admits no name at all admitted the branch's
			// names instead.
			Name:       "allof_property_names_conjunction",
			SchemaPath: "testdata/schemas/regression/allof_property_names_conjunction.json",
			Instances: []notInstance{
				{Name: "name both sides admit", Doc: `{"narrowed":{"b":1}}`, Valid: true,
					Why: "the object's own propertyNames permits a and b, the branch's b and c; b is the intersection"},
				{Name: "name only the object admits", Doc: `{"narrowed":{"a":1}}`, Valid: false,
					Why: "a is on the object's list and not the branch's"},
				{Name: "name only the branch admits", Doc: `{"narrowed":{"c":1}}`, Valid: false,
					Why: "the mirror: c is on the branch's list and not the object's"},
				{Name: "empty name intersection admits no key", Doc: `{"empty":{"b":1}}`, Valid: false,
					Why: "the lists share no name, so no key is legal -- and b is the one the discarded patch would have re-admitted"},
				{Name: "empty name intersection admits no key either way", Doc: `{"empty":{"a":1}}`, Valid: false,
					Why: "the other side of the row above"},
				{Name: "an object with no keys is still legal", Doc: `{"empty":{}}`, Valid: true,
					Why: "propertyNames constrains the names that are there; there are none"},
			},
		},
	}
}

// allOfBranchTypeInferenceFixtures are the one type a merge holds that no schema
// stated.
//
// A branch naming array positions and no type is guessed to be about an array,
// which is what lets the merged schema be typed at all (#222). A later branch
// that states a type is an assertion, and an assertion settles a guess rather
// than intersecting with it. The code under that rule only removed the mark
// recording the guess, leaving the guess itself in place, so the merged type
// depended on which branch came first -- the same two branches written the other
// way round answered differently. This group runs both orders and requires the
// same answer.
func allOfBranchTypeInferenceFixtures() []notFixture {
	return []notFixture{
		{
			// The one type a merge holds that is not an assertion: the "array"
			// it infers for itself from a branch that named array positions but
			// stated no type. A guess and an assertion are not two assertions,
			// so the assertion replaces it rather than intersecting with it --
			// which is what #222's own comment has claimed since it was written,
			// and what the code under it did not do. The mark came off and the
			// guess stayed, so the answer depended on the order the two branches
			// happened to be written in.
			Name:       "allof_branch_type_settles_inference",
			SchemaPath: "testdata/schemas/regression/allof_branch_type_settles_inference.json",
			Instances: []notInstance{
				{Name: "stated type wins over an earlier inference", Doc: `{"arrayFirst":"ok"}`, Valid: true,
					Why: "the first branch states items and no type, which the merge guesses is an array; the second states string, and a stated type settles a guess"},
				{Name: "stated type still carries its own bound", Doc: `{"arrayFirst":"z"}`, Valid: false,
					Why: "control for the row above: the same branch's minLength 2 binds"},
				{Name: "the inferred array is not the answer", Doc: `{"arrayFirst":["ab"]}`, Valid: false,
					Why: "the sharp control: items applies to arrays and asserts nothing about anything else, so an array is refused by the stated string type"},

				{Name: "the other order gives the same answer", Doc: `{"typeFirst":"ok"}`, Valid: true,
					Why: "the same two branches written the other way round; a merge whose answer depends on branch order is answering about the document's layout rather than its meaning"},
				{Name: "the other order carries the bound too", Doc: `{"typeFirst":"z"}`, Valid: false,
					Why: "control for the row above"},
				{Name: "the other order refuses the array too", Doc: `{"typeFirst":["ab"]}`, Valid: false,
					Why: "control for the row above"},

				// The control that says the inference itself still works: where
				// the stated type agrees with the guess, nothing moves.
				{Name: "agreeing type keeps the array", Doc: `{"bothArrays":["ab","cd"]}`, Valid: true,
					Why: "the branch states array, which is what the merge guessed; the element schema and the minItems both apply"},
				{Name: "agreeing type keeps the array bound", Doc: `{"bothArrays":["ab"]}`, Valid: false,
					Why: "control for the row above: minItems 2"},
			},
		},
	}
}

// TestABranchTypeSettlesTheMergesArrayGuess puts every document to the compiled
// type.
func TestABranchTypeSettlesTheMergesArrayGuess(t *testing.T) {
	runInstanceFixtures(t, "allof_branch_type_inference_test", allOfBranchTypeInferenceFixtures())
}

// TestAllOfBranchConjoinsADeclaredProperty puts every document to the compiled
// type.
func TestAllOfBranchConjoinsADeclaredProperty(t *testing.T) {
	runInstanceFixtures(t, "allof_property_conjunction_test", allOfPropertyConjunctionFixtures())
}
