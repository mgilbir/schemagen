package tests

import "testing"

// TestConditionalMessagesNameTheBranchThatRefused holds every message an
// object-level `if`/`then`/`else` produces to the sub-schema a caller has to go
// and read, which is issue #281.
//
// Both a `then` violation and an `else` violation were announced as `if:`. The
// check was not misattached -- the evaluator runs the condition and then the one
// branch it selects, correctly -- but the emitted message stamped the whole group
// with the keyword the check is filed under, at a point that no longer knew which
// branch had run. So {"k":"yes","p":{}} came out `if: property p: object is
// missing required property r` against a condition that says nothing whatever
// about p.
//
// `if` is not a weaker label here, it is an unreachable one. A condition is not
// an assertion: an `if` that fails selects `else`, or, where there is no `else`,
// accepts. No document is ever refused by it, so no message may ever open with
// it -- which is the control the false_branches and if_refuses cases below stand
// for, and why none of the Wants in this file contains the word.
//
// The two other paths that judge the same group already named the branch: the
// static object reading (`then: property "p" does not satisfy the then schema`,
// the static_* fixtures) and the value-level dynamic reading. The runtime
// evaluator was the outlier, and the three now agree on what the message opens
// with. They still differ in what follows it, because they know different
// amounts: the evaluator ran the branch and can say what in it was violated,
// while the static readings compiled the branch to one boolean expression and
// have no per-keyword reason to give.
//
// Run compiled and compared whole, for the reason errorPathCase gives: a wrong
// label is a right label with something else in its place, and `if:` is a
// substring of nothing here, so a containment check on the reason alone passes
// under the very defect.
func TestConditionalMessagesNameTheBranchThatRefused(t *testing.T) {
	runErrorPathFixtures(t, "error_path_test", conditionalBranchMessageFixtures())
}

// condGroup is the issue's own reproduction: a condition on `k`, a `then` about
// `p` and an `else` about `q`, with each consequence stating an object the
// static reading cannot reduce so that the group compiles to the evaluator.
const condGroup = `{"type":"object","properties":{"k":{"type":"string"}},
 "if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
 "then":{"properties":{"p":{"type":"object","required":["r"],"properties":{"r":{"type":"string"}}}}},
 "else":{"properties":{"q":{"type":"object","required":["s"],"properties":{"s":{"type":"string"}}}}}}`

func conditionalBranchMessageFixtures() []errorPathFixture {
	return []errorPathFixture{
		{
			// The issue's two reproductions, and the four documents that must
			// stay accepted beside them.
			Name:   "issue_281_reproduction",
			Schema: condGroup,
			Cases: []errorPathCase{
				{Name: "then violation", Doc: `{"k":"yes","p":{}}`,
					Want:   `then: property p: object is missing required property r`,
					Reason: "was `if:`; the condition says nothing about p, so a caller sent to it reads the wrong sub-schema"},
				{Name: "else violation", Doc: `{"k":"no","q":{}}`,
					Want:   `else: property q: object is missing required property s`,
					Reason: "was `if:`; the condition says nothing about q either"},
				{Name: "then satisfied", Doc: `{"k":"yes","p":{"r":"x"}}`,
					Reason: "control: no verdict may change -- this is a label, not a check"},
				{Name: "else satisfied", Doc: `{"k":"no","q":{"s":"x"}}`,
					Reason: "control: no verdict may change"},
				{Name: "then branch says nothing about an absent p", Doc: `{"k":"yes"}`,
					Reason: "control: the branch constrains p only where p is present"},
				{Name: "the other branch's property is not applied", Doc: `{"k":"yes","q":{}}`,
					Reason: "control: the condition selected then, and then says nothing about q"},
			},
		},
		{
			// The control this fix could have broken: a document the condition
			// itself refuses. There are four such configurations and not one of
			// them can produce an `if:` message, because the condition is not an
			// assertion -- it selects, and what it selects is what refuses.
			Name:   "the_condition_refusing_never_names_itself",
			Schema: condGroup,
			Cases: []errorPathCase{
				{Name: "condition refused, else satisfied", Doc: `{"k":"no","q":{"s":"x"}}`,
					Reason: "the condition refuses this document and the document is legal: an if that fails is not a failure"},
				{Name: "condition refused, else violated", Doc: `{"k":"no","q":{}}`,
					Want:   `else: property q: object is missing required property s`,
					Reason: "the condition refused, and the sub-schema that then refused the document is the else -- which is what the message names"},
			},
		},
		{
			Name: "condition_refused_with_no_else_accepts",
			Schema: `{"type":"object","properties":{"k":{"type":"string"}},
			  "if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			  "then":{"properties":{"p":{"type":"object","required":["r"],"properties":{"r":{"type":"string"}}}}}}`,
			Cases: []errorPathCase{
				{Name: "condition holds, then violated", Doc: `{"k":"yes","p":{}}`,
					Want:   `then: property p: object is missing required property r`,
					Reason: "if without else: the only branch that can refuse is then"},
				{Name: "condition refused", Doc: `{"k":"no","p":{}}`,
					Reason: "control: with no else there is nothing to apply, so the document is legal however badly it fails the condition"},
				{Name: "condition holds, then satisfied", Doc: `{"k":"yes","p":{"r":"x"}}`,
					Reason: "control"},
			},
		},
		{
			Name: "else_without_then",
			Schema: `{"type":"object","properties":{"k":{"type":"string"}},
			  "if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			  "else":{"properties":{"q":{"type":"object","required":["s"],"properties":{"s":{"type":"string"}}}}}}`,
			Cases: []errorPathCase{
				{Name: "condition refused, else violated", Doc: `{"k":"no","q":{}}`,
					Want:   `else: property q: object is missing required property s`,
					Reason: "else without then: the only branch that can refuse is else"},
				{Name: "condition holds", Doc: `{"k":"yes","q":{}}`,
					Reason: "control: with no then there is nothing to apply"},
				{Name: "condition refused, else satisfied", Doc: `{"k":"no","q":{"s":"x"}}`,
					Reason: "control"},
			},
		},
		{
			// The sharpest form of "the condition is what refuses": both
			// consequences are `false`, so exactly one of them rejects every
			// document, and which one is decided by the condition. The message
			// still names the schema that did the refusing rather than the one
			// that chose it.
			Name: "false_branches_name_the_branch_not_the_condition",
			Schema: `{"type":"object","properties":{"k":{"type":"string"}},
			  "if":{"$ref":"#/$defs/O","properties":{"k":{"const":"yes"}},"required":["k"]},
			  "then":false,"else":false,
			  "$defs":{"O":{"type":"object"}}}`,
			Cases: []errorPathCase{
				{Name: "condition holds", Doc: `{"k":"yes"}`,
					Want:   `then: schema is false, which no value satisfies`,
					Reason: "the condition held, so `then: false` is what forbids the document"},
				{Name: "condition refused", Doc: `{"k":"no"}`,
					Want:   `else: schema is false, which no value satisfies`,
					Reason: "the condition refused, so `else: false` is what forbids it -- naming the condition would name a schema this document simply does not match"},
			},
		},
		{
			// A branch written as a $ref, which is the spelling that routes the
			// group to the evaluator in the first place (#209).
			Name: "ref_branch",
			Schema: `{"type":"object","properties":{"k":{"type":"string"}},
			  "if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			  "then":{"$ref":"#/$defs/NeedsP"},
			  "$defs":{"NeedsP":{"required":["p"]}}}`,
			Cases: []errorPathCase{
				{Name: "then violated", Doc: `{"k":"yes"}`,
					Want:   `then: object is missing required property p`,
					Reason: "the branch is a reference and the message names the keyword that reached it"},
				{Name: "condition refused", Doc: `{"k":"no"}`, Reason: "control"},
				{Name: "then satisfied", Doc: `{"k":"yes","p":1}`, Reason: "control"},
			},
		},
		{
			// Nested groups: each one names the branch it took, so the message
			// is the trail of branches the document was judged under rather than
			// the outermost keyword alone.
			Name: "nested_conditionals",
			Schema: `{"type":"object","properties":{"k":{"type":"string"},"m":{"type":"string"}},
			  "if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			  "then":{"if":{"properties":{"m":{"const":"on"}},"required":["m"]},
			          "then":{"properties":{"p":{"type":"object","required":["r"],"properties":{"r":{"type":"string"}}}}},
			          "else":{"properties":{"q":{"type":"object","required":["s"],"properties":{"s":{"type":"string"}}}}}}}`,
			Cases: []errorPathCase{
				{Name: "inner then", Doc: `{"k":"yes","m":"on","p":{}}`,
					Want:   `then: then: property p: object is missing required property r`,
					Reason: "outer then, then inner then: both are sub-schemas the caller has to walk through to reach the one at fault"},
				{Name: "inner else", Doc: `{"k":"yes","m":"off","q":{}}`,
					Want:   `then: else: property q: object is missing required property s`,
					Reason: "the inner condition refused, so the inner else is what applies"},
				{Name: "outer condition refused", Doc: `{"k":"no","m":"on","p":{}}`,
					Reason: "control: the outer group has no else, so nothing inside it applies"},
				{Name: "both satisfied", Doc: `{"k":"yes","m":"on","p":{"r":"x"}}`,
					Reason: "control"},
			},
		},
		{
			// A group inside an allOf branch is merged into this same struct, so
			// its check lands here or nowhere -- the reach #135 drew for anyOf.
			Name: "conditional_inside_an_allof_branch",
			Schema: `{"type":"object","properties":{"k":{"type":"string"}},
			  "allOf":[{"if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			            "then":{"properties":{"p":{"type":"object","required":["r"],"properties":{"r":{"type":"string"}}}}},
			            "else":{"properties":{"q":{"type":"object","required":["s"],"properties":{"s":{"type":"string"}}}}}}]}`,
			Cases: []errorPathCase{
				{Name: "then violation", Doc: `{"k":"yes","p":{}}`,
					Want:   `then: property p: object is missing required property r`,
					Reason: "the group came from an allOf branch and is labelled the same way"},
				{Name: "else violation", Doc: `{"k":"no","q":{}}`,
					Want:   `else: property q: object is missing required property s`,
					Reason: "same, on the other branch"},
				{Name: "then satisfied", Doc: `{"k":"yes","p":{"r":"x"}}`, Reason: "control"},
			},
		},
		{
			// The keyword is a segment of the path, so it is joined to the
			// container's path with a "." -- the family #283 left dotted
			// (n.not, p.anyOf, pn.propertyNames).
			Name:   "the_keyword_is_a_dotted_segment_of_the_path",
			Schema: `{"type":"object","properties":{"outer":` + condGroup + `}}`,
			Cases: []errorPathCase{
				{Name: "under a property", Doc: `{"outer":{"k":"yes","p":{}}}`,
					Want:   `outer.then: property p: object is missing required property r`,
					Reason: "was outer.if:; a keyword a caller can follow stays joined with a dot",
				},
			},
		},
		{
			Name:   "under_an_array_element",
			Schema: `{"type":"object","properties":{"list":{"type":"array","items":` + condGroup + `}}}`,
			Cases: []errorPathCase{
				{Name: "in an element", Doc: `{"list":[{"k":"no","q":{"s":"x"}},{"k":"yes","p":{}}]}`,
					Want:   `list[1].then: property p: object is missing required property r`,
					Reason: "was list[1].if:; the accessor and the keyword segment both keep their spelling",
				},
			},
		},

		// ---- the second reading of the same group ----
		//
		// Where every consequence is a scalar check the static object reading
		// takes the group instead, and it already named the branch. These
		// fixtures are here so the two readings of one schema can be held
		// against each other rather than described.
		{
			Name: "static_reading_of_the_same_group",
			Schema: `{"type":"object","properties":{"k":{"type":"string"}},
			  "if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			  "then":{"properties":{"p":{"type":"string","minLength":3}}},
			  "else":{"properties":{"q":{"type":"string","minLength":4}}}}`,
			Cases: []errorPathCase{
				{Name: "then violation", Doc: `{"k":"yes","p":"a"}`,
					Want:   `then: property "p" does not satisfy the then schema`,
					Reason: "the static reading names the branch, as it always did; what it cannot say is which keyword of the branch was violated, because it compiled the branch to one boolean expression"},
				{Name: "else violation", Doc: `{"k":"no","q":"a"}`,
					Want:   `else: property "q" does not satisfy the else schema`,
					Reason: "same on the other branch"},
				{Name: "then satisfied", Doc: `{"k":"yes","p":"abc"}`, Reason: "control"},
				{Name: "condition refused, else satisfied", Doc: `{"k":"no","q":"abcd"}`, Reason: "control"},
			},
		},
		{
			Name: "static_reading_required_key",
			Schema: `{"type":"object","properties":{"k":{"type":"string"},"p":{"type":"string"}},
			  "if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			  "then":{"required":["p"]}}`,
			Cases: []errorPathCase{
				{Name: "then violation", Doc: `{"k":"yes"}`,
					Want:   `then: required property "p" is missing`,
					Reason: "the static reading's other message, named the same way"},
				{Name: "condition refused", Doc: `{"k":"no"}`, Reason: "control"},
			},
		},
		{
			Name: "static_reading_else_only",
			Schema: `{"type":"object","properties":{"k":{"type":"string"},"q":{"type":"string"}},
			  "if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			  "else":{"required":["q"]}}`,
			Cases: []errorPathCase{
				{Name: "condition refused, else violated", Doc: `{"k":"no"}`,
					Want:   `else: required property "q" is missing`,
					Reason: "the static reading of an else without a then, named the same way"},
				{Name: "condition holds", Doc: `{"k":"yes"}`,
					Reason: "control: with no then there is nothing to apply"},
			},
		},
	}
}
