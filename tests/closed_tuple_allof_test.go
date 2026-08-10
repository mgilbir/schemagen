package tests

import "testing"

// closedTupleInAllOfFixtures are issue #222's first row: a tuple closed by
// `items: false` inside an allOf branch.
//
// A closed tail states two things at once -- which positions the tuple has, and
// that the array has no more than that many -- and generateAllOfDef reads the
// two off different schemas. The positions come off the branch, because the
// merge leaves array keywords where they are on purpose; the bounds come off the
// merged schema, which for that same reason carries none of them. So the length
// half was enforced nowhere, and every position below accepted a longer array
// where the identical branch written without the allOf around it rejects it.
//
// Both spellings, because they are two keywords and a fix to one says nothing
// about the other: 2020-12 writes prefixItems beside items:false, draft-07 and
// earlier write the tuple form of `items` beside additionalItems:false.
//
// Run compiled. A dropped bound emits no check at all, which is indistinguishable
// in a golden from a schema that never stated one.
func closedTupleInAllOfFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "closed_tuple_in_allof",
			SchemaPath: "testdata/schemas/regression/closed_tuple_in_allof.json",
			Instances: []notInstance{
				{Name: "root spelling rejects the extra element", Doc: `{"atRoot":[1,2]}`, Valid: false,
					Why: "control: the same tuple with no allOf around it has always rejected, which is what makes the rows below a drop rather than a limit"},
				{Name: "root spelling accepts the tuple", Doc: `{"atRoot":[1]}`, Valid: true, Why: "control for the above"},

				{Name: "property rejects the extra element", Doc: `{"atProperty":[1,2]}`, Valid: false,
					Why: "issue #222: the branch closes the tail and the merged schema carries no bound, so nothing refused the second element"},
				{Name: "property accepts the tuple", Doc: `{"atProperty":[1]}`, Valid: true, Why: "control for the above"},
				{Name: "property still checks the slot", Doc: `{"atProperty":["a"]}`, Valid: false,
					Why: "control: the per-position check must survive; a fix that only adds a length bound loses it"},

				{Name: "element rejects the extra element", Doc: `{"atElement":[[1,2]]}`, Valid: false,
					Why: "the same branch one position in, inside an items sub-schema"},
				{Name: "element accepts the tuple", Doc: `{"atElement":[[1]]}`, Valid: true, Why: "control for the above"},

				{Name: "map value rejects the extra element", Doc: `{"atMapValue":{"k":[1,2]}}`, Valid: false,
					Why: "and inside a schema-valued additionalProperties"},
				{Name: "map value accepts the tuple", Doc: `{"atMapValue":{"k":[1]}}`, Valid: true, Why: "control for the above"},

				{Name: "second branch rejects the extra element", Doc: `{"besideASecondBranch":[1,2]}`, Valid: false,
					Why: "a second branch stating only the type must not displace the first branch's tail"},
				{Name: "second branch accepts the tuple", Doc: `{"besideASecondBranch":[1]}`, Valid: true, Why: "control for the above"},

				{Name: "wider maxItems still closes the tail", Doc: `{"widerMaxItems":[1,2]}`, Valid: false,
					Why: "control: an explicit maxItems 3 beside items:false does not reopen the tail -- the tail schema is `false`, so index 1 is refused by the tail check rather than by a length bound"},
				{Name: "wider maxItems accepts the tuple", Doc: `{"widerMaxItems":[1]}`, Valid: true, Why: "control for the above"},

				{Name: "open tail accepts a longer array", Doc: `{"openTail":[1,"a"]}`, Valid: true,
					Why: "the false-rejection control, and the reason the bound is read off the closed tail rather than off the prefix length: prefixItems alone says nothing about what follows"},
				{Name: "open tail accepts the tuple", Doc: `{"openTail":[1]}`, Valid: true, Why: "control for the above"},

				{Name: "nothing present", Doc: `{}`, Valid: true, Why: "control: every property is optional"},
			},
		},
		{
			Name:       "closed_tuple_in_allof_draft7",
			SchemaPath: "testdata/schemas/regression/closed_tuple_in_allof_draft7.json",
			Instances: []notInstance{
				{Name: "root spelling rejects the extra element", Doc: `{"atRoot":[1,2]}`, Valid: false,
					Why: "the plain draft-07 closed tuple. No test in the tree pinned it before, so deleting the arm that reads it broke both spellings and only the allOf row said so"},
				{Name: "root spelling accepts the tuple", Doc: `{"atRoot":[1]}`, Valid: true, Why: "control for the above"},

				{Name: "rejects the extra element", Doc: `{"atProperty":[1,2]}`, Valid: false,
					Why: "the draft-07 spelling of the same statement, dropped by the same line"},
				{Name: "accepts the tuple", Doc: `{"atProperty":[1]}`, Valid: true, Why: "control for the above"},
				{Name: "still checks the slot", Doc: `{"atProperty":["a"]}`, Valid: false, Why: "control for the above"},
				{Name: "open tail accepts a longer array", Doc: `{"openTail":[1,"a"]}`, Valid: true,
					Why: "the false-rejection control: an array-valued `items` with no additionalItems beside it leaves the tail open"},
			},
		},
	}
}

// TestClosedTupleInsideAnAllOfKeepsItsTail runs each fixture compiled.
func TestClosedTupleInsideAnAllOfKeepsItsTail(t *testing.T) {
	runInstanceFixtures(t, "closed_tuple_allof_test", closedTupleInAllOfFixtures())
}
