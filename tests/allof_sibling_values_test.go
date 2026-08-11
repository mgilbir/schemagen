package tests

import "testing"

// allOfSiblingValuesFixtures are issue #238: an allOf written beside a schema's
// own `enum` or `const`, naming fewer values than it does.
//
// allOf is an in-place applicator, so a branch is asserted of the very instance
// the schema itself is asserted of, and two statements of which values are legal
// admit only the values both list. The three ladders that turn a schema into a
// Go type each read the enum before they read the allOf and returned there, so
// the branch was never reached:
//
//	{"properties":{"p":{"type":"string","enum":["a","b"],"allOf":[{"enum":["b"]}]}}}
//
// typed p as an enum naming a and b and accepted {"p":"a"}, which the branch
// forbids.
//
// This is the neighbour of #231, not a repeat of it. There the *object* carried
// the allOf and a branch named a property the object had already declared; that
// path reaches mergeAllOfBranches, which conjoins. Here the property itself
// carries the allOf and nothing on the way ever asked the branch. The two
// spellings are pinned in separate files so a fix to one cannot be mistaken for
// a fix to the other, and allof_property_conjunction_test.go runs beside this.
//
// Every case is written in both directions -- the branch narrowing what the
// schema states and the schema narrowing what the branch states -- because a
// conjunction that only tightens one way is half of one. The second direction
// already answered correctly before the fix, precisely because the old arm kept
// the schema's own list; it is here as the control that a fix which simply took
// the branch's list instead would fail.
//
// Run compiled rather than compared against a golden. Two of the three
// positions produce a Validate that reads perfectly well and checks the wrong
// set of members, and the empty cases produce a type whose whole content is a
// rejection -- neither is legible as a diff.
func allOfSiblingValuesFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "allof_sibling_values",
			SchemaPath: "testdata/schemas/regression/allof_sibling_values.json",
			Instances: []notInstance{
				// The issue exactly as reported: the property carries the allOf.
				{Name: "branch narrows the property's own enum", Doc: `{"branchNarrows":"a"}`, Valid: false,
					Why: "issue #238: the property states enum [a,b] and its allOf branch states enum [b]; the enum arm answered from the property's list alone and named a as well"},
				{Name: "the value both sides admit", Doc: `{"branchNarrows":"b"}`, Valid: true,
					Why: "control for the row above -- a fix that emptied the set rather than intersecting it would refuse this too"},

				// The other direction: the schema's own list is the narrower of
				// the two. Correct before the fix, and it has to stay correct --
				// a fix that assigned the branch's list would accept "a" here.
				{Name: "the property's own enum narrows the branch's", Doc: `{"rootNarrows":"a"}`, Valid: false,
					Why: "the property states enum [b] and the branch states [a,b]; the conjunction is b, and taking the branch's list would accept this"},
				{Name: "the value both sides admit, other direction", Doc: `{"rootNarrows":"b"}`, Valid: true,
					Why: "control for the row above"},

				// const on one side, enum on the other, both ways round.
				{Name: "a const in the branch narrows an enum", Doc: `{"constInBranch":"p"}`, Valid: false,
					Why: "the property states enum [p,q] and the branch a const of q; a const is a one-member enum and intersects as one"},
				{Name: "the value the branch's const names", Doc: `{"constInBranch":"q"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "an enum in the branch does not widen a const", Doc: `{"constOnProperty":"p"}`, Valid: false,
					Why: "the property states const q and the branch enum [p,q]; the conjunction is q, and reading the branch as the answer would accept p"},
				{Name: "the value the property's const names", Doc: `{"constOnProperty":"q"}`, Valid: true,
					Why: "control for the row above"},

				// More than one branch, and a branch whose own allOf narrows again.
				{Name: "first of two branches is not the whole answer", Doc: `{"chain":"a"}`, Valid: false,
					Why: "enum [a,b,c] against branches [a,b] and [b,c]: only b is in all three, and a fix that stopped at the first branch would accept a"},
				{Name: "last of two branches is not the whole answer", Doc: `{"chain":"c"}`, Valid: false,
					Why: "the other end of the same chain -- c survives the second branch and not the first"},
				{Name: "the value every branch admits", Doc: `{"chain":"b"}`, Valid: true,
					Why: "control for the two above"},
				{Name: "a later branch narrows where the first did not", Doc: `{"laterBranchNarrows":"a"}`, Valid: false,
					Why: "enum [a,b] against branches [a,b] and [b]: the first branch takes nothing away, so a predicate that answered from the first alone would find nothing narrowed and leave the arm standing"},
				{Name: "the value the later branch leaves", Doc: `{"laterBranchNarrows":"b"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "a branch's own allOf narrows too", Doc: `{"nestedChain":"b"}`, Valid: false,
					Why: "the branch states nothing itself and holds an allOf of [b,c] and [c]; b survives only the outer of those"},
				{Name: "the value the nested chain leaves", Doc: `{"nestedChain":"c"}`, Valid: true,
					Why: "control for the row above"},

				// The branch reached through a reference, and an allOf reached
				// through one.
				{Name: "a branch that is a $ref narrows", Doc: `{"viaRef":"a"}`, Valid: false,
					Why: "the branch is {\"$ref\":\"#/$defs/OnlyB\"}; a predicate that read the branch node without following the reference would see a branch stating nothing"},
				{Name: "the value the referenced branch admits", Doc: `{"viaRef":"b"}`, Valid: true,
					Why: "control for the row above"},
				{Name: "an allOf reached through a $ref narrows", Doc: `{"allOfThroughRef":"b"}`, Valid: false,
					Why: "the branch references a schema whose own allOf states [b,c] and [c]; both hops have to be followed"},
				{Name: "the value the referenced allOf leaves", Doc: `{"allOfThroughRef":"c"}`, Valid: true,
					Why: "control for the row above"},

				// 1, 1.0 and 1e0 are one JSON number, so the two spellings are
				// one member and the intersection keeps it.
				{Name: "a member the two sides spell differently survives", Doc: `{"numberSpelling":1}`, Valid: true,
					Why: "enum [1.0,2] against [1,3]: 1.0 and 1 are the same number, so the intersection is 1 and not empty"},
				{Name: "a member only one side lists", Doc: `{"numberSpelling":2}`, Valid: false,
					Why: "2 is on the property and not in the branch"},

				// Controls that the arm does not fire where nothing narrows.
				{Name: "branches that agree keep both members", Doc: `{"agrees":"a"}`, Valid: true,
					Why: "enum [a,b] against [b,a] is the same set; a fix that narrowed on the presence of an allOf rather than on what it says would drop a"},
				{Name: "branches that agree still refuse a non-member", Doc: `{"agrees":"z"}`, Valid: false,
					Why: "control for the row above"},
				{Name: "a branch stating no values leaves the set alone", Doc: `{"silentBranch":"a"}`, Valid: true,
					Why: "the branch states minLength only, which says nothing about which values are legal"},
				{Name: "a branch stating no values still refuses a non-member", Doc: `{"silentBranch":"z"}`, Valid: false,
					Why: "control for the row above"},

				// The same schema in the two positions that reach resolveType
				// rather than resolvePropertyType.
				{Name: "a map value's branch narrows", Doc: `{"mapValues":{"k":"a"}}`, Valid: false,
					Why: "issue #238 in the additionalProperties position, which resolveType types and which had the same enum arm"},
				{Name: "the value a map value's branch admits", Doc: `{"mapValues":{"k":"b"}}`, Valid: true,
					Why: "control for the row above"},
				{Name: "an array element's branch narrows", Doc: `{"listItems":["a"]}`, Valid: false,
					Why: "issue #238 in the items position"},
				{Name: "the value an array element's branch admits", Doc: `{"listItems":["b"]}`, Valid: true,
					Why: "control for the row above"},
			},
		},
		{
			Name:       "allof_sibling_values_empty",
			SchemaPath: "testdata/schemas/regression/allof_sibling_values_empty.json",
			Instances: []notInstance{
				// Two disjoint statements admit nothing, so the type has to
				// refuse every value -- including the ones each side lists.
				// This is what the three sites had no answer for at all: the
				// enum arm builds a type from whichever list it read and there
				// is no arm behind it that says "no value at all".
				{Name: "a value only the property lists", Doc: `{"disjointEnums":"a"}`, Valid: false,
					Why: "enum [a] against branch enum [b]: nothing satisfies both, so a is refused although the property names it"},
				{Name: "a value only the branch lists", Doc: `{"disjointEnums":"b"}`, Valid: false,
					Why: "the other half -- a fix that took the branch's list would accept this"},
				{Name: "a value neither lists", Doc: `{"disjointEnums":"z"}`, Valid: false,
					Why: "control: the type refuses everything, not just the two named values"},

				{Name: "a const the branch's enum excludes", Doc: `{"constAgainstDisjointEnum":"a"}`, Valid: false,
					Why: "const a against branch enum [b]; before the fix the const check stood alone and accepted the one value the conjunction admits nothing of"},
				{Name: "the value the branch's enum names", Doc: `{"constAgainstDisjointEnum":"b"}`, Valid: false,
					Why: "the other half of the same empty intersection"},
				{Name: "an enum the branch's const excludes", Doc: `{"enumAgainstDisjointConst":"a"}`, Valid: false,
					Why: "enum [a] against branch const b, the same pair with the spellings swapped"},
				{Name: "the value the branch's const names", Doc: `{"enumAgainstDisjointConst":"b"}`, Valid: false,
					Why: "the other half"},

				// Two integers float64 cannot tell apart. Folding them would
				// make this intersection non-empty and keep a member neither
				// schema lists.
				{Name: "an integer only the property lists", Doc: `{"bigIntegers":9007199254740992}`, Valid: false,
					Why: "9007199254740992 against branch enum [9007199254740993]: distinct JSON numbers, so the intersection is empty. Compared through float64 they are one value and this would be accepted"},
				{Name: "an integer only the branch lists", Doc: `{"bigIntegers":9007199254740993}`, Valid: false,
					Why: "the other half of the same pair"},

				{Name: "a branch admitting nothing empties the set", Doc: `{"emptyBranchEnum":"a"}`, Valid: false,
					Why: "the branch is spelled \"enum\": [], which states members and lists none; reading its values alone would mistake it for a branch that says nothing"},
				{Name: "the second of two branches empties the set", Doc: `{"chainEmptiesLate":"b"}`, Valid: false,
					Why: "enum [a,b] against [b] then [a]: each branch alone leaves a member and the two together leave none, so a predicate that stopped at the first would keep b"},
				{Name: "the first of two branches is not the answer either", Doc: `{"chainEmptiesLate":"a"}`, Valid: false,
					Why: "the other half of the same chain"},

				{Name: "a map value with an empty intersection", Doc: `{"mapValues":{"k":"a"}}`, Valid: false,
					Why: "the empty case in the additionalProperties position, which resolveType types"},
				{Name: "an array element with an empty intersection", Doc: `{"listItems":["a"]}`, Valid: false,
					Why: "the empty case in the items position"},

				// The control that separates a fix from an amputation: a
				// satisfiable conjunction in the same document still accepts
				// what it admits.
				{Name: "a satisfiable conjunction beside the empty ones", Doc: `{"satisfiable":"b"}`, Valid: true,
					Why: "enum [a,b] against [b] is b; a fix that answered every conjunction with the forbidding wrapper would refuse this"},
				{Name: "a satisfiable conjunction still refuses the excluded value", Doc: `{"satisfiable":"a"}`, Valid: false,
					Why: "control for the row above"},

				// An object that omits the property is still a valid instance:
				// the schema forbids values for it, not the document.
				{Name: "the document that omits every forbidden property", Doc: `{}`, Valid: true,
					Why: "an empty intersection says no value is legal, not that the object is; refusing this would reject a document the schema permits"},
			},
		},
		{
			Name:       "allof_sibling_values_root",
			SchemaPath: "testdata/schemas/regression/allof_sibling_values_root.json",
			Instances: []notInstance{
				{Name: "branch narrows the root's own enum", Doc: `"a"`, Valid: false,
					Why: "issue #238 where the whole document carries the enum and the allOf; generateTypeDef's enum arm returned before its allOf arm was reached"},
				{Name: "the value both sides admit", Doc: `"b"`, Valid: true,
					Why: "control for the row above"},
				{Name: "a value neither side lists", Doc: `"z"`, Valid: false,
					Why: "control: the type is still an enum and still refuses what is outside it"},
			},
		},
		{
			Name:       "allof_sibling_values_root_empty",
			SchemaPath: "testdata/schemas/regression/allof_sibling_values_root_empty.json",
			Instances: []notInstance{
				{Name: "a value only the root lists", Doc: `"a"`, Valid: false,
					Why: "enum [a] against branch enum [b] at the document root: nothing satisfies both"},
				{Name: "a value only the branch lists", Doc: `"b"`, Valid: false,
					Why: "the other half"},
			},
		},
		{
			Name:       "allof_sibling_values_ref_displaces",
			SchemaPath: "testdata/schemas/regression/allof_sibling_values_ref_displaces.json",
			Instances: []notInstance{
				// #151: through draft 7 a $ref replaces everything written
				// beside it, so neither the enum nor the allOf is written at
				// all and the target admits every string. The new arms stand
				// behind the same gate the enum arms do, and this is what says
				// so.
				{Name: "a value the sibling enum would exclude", Doc: `{"p":"z"}`, Valid: true,
					Why: "draft-07: the $ref displaces its siblings, so neither the enum nor the allOf beside it is read and the target admits every string (issue #151)"},
				{Name: "a value the branch would exclude", Doc: `{"p":"a"}`, Valid: true,
					Why: "the same rule for the allOf: reading it here would refuse a document the draft admits"},
				{Name: "a value of the wrong type", Doc: `{"p":1}`, Valid: false,
					Why: "control: the target's own type still binds, so the reference was followed rather than dropped"},
			},
		},
		{
			Name:       "allof_sibling_values_ref_merges",
			SchemaPath: "testdata/schemas/regression/allof_sibling_values_ref_merges.json",
			Instances: []notInstance{
				// #153: from 2019-09 on the reference and the siblings all
				// bind, and only the merge can say all three at once.
				{Name: "a value the reference's target forbids", Doc: `{"p":"abc"}`, Valid: false,
					Why: "2019-09 on: the target states minLength 5, and the sibling enum listing abc does not excuse it (issue #153)"},
				{Name: "a value the allOf branch forbids", Doc: `{"p":"abcde"}`, Valid: false,
					Why: "five characters satisfy the target, but the branch's enum names only abcdef -- this is #238 reached through the merge arm"},
				{Name: "the value all three admit", Doc: `{"p":"abcdef"}`, Valid: true,
					Why: "control for the two above"},
			},
		},
	}
}

// TestAllOfBesideStatedValuesNarrowsThem is issue #238.
//
// The verdict cannot be read off the generated source: a type whose enum names
// two members where the schema admits one is a perfectly ordinary enum type, and
// the position that carries a const emits a check that reads correctly and
// compares against a value the conjunction excludes. So each document is put to
// the compiled type.
func TestAllOfBesideStatedValuesNarrowsThem(t *testing.T) {
	runInstanceFixtures(t, "allof_sibling_values_test", allOfSiblingValuesFixtures())
}
