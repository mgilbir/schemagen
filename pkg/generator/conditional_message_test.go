package generator

import (
	"slices"
	"strings"
	"testing"
)

// TestConditionalRuntimeCheckStatesNothingButTheGroup holds the invariant the
// emitted message rests on.
//
// Where MessageNamesItsOwnKeyword says so, the emitter drops the keyword it
// would otherwise stamp and passes the evaluator's reason up whole, because the
// keyword the check is filed under -- `if` -- can never be the one that refused
// (issue #281). That is only safe while the evaluator always has a keyword to
// put there, and it does for exactly one reason: the compiled node states
// nothing but the group. An `If`, and a `Then` or an `Else`. So the only thing
// in it that can refuse a document is a branch, and the evaluator opens a
// branch's reason with the branch's name.
//
// A node carrying any other keyword breaks that: it could fail for a reason with
// no keyword in front of it, and the message would open with a bare colon or, if
// the reason were empty, with nothing at all. The list is therefore asserted
// here rather than remembered at the emission site, which is a template and
// cannot ask.
func TestConditionalRuntimeCheckStatesNothingButTheGroup(t *testing.T) {
	for name, input := range map[string]string{
		"if then else": `{
			"title":"Doc","type":"object","properties":{"k":{"type":"string"}},
			"if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			"then":{"properties":{"p":{"type":"object","required":["r"]}}},
			"else":{"properties":{"q":{"type":"object","required":["s"]}}}}`,
		"if then": `{
			"title":"Doc","type":"object","properties":{"k":{"type":"string"}},
			"if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			"then":{"properties":{"p":{"type":"object","required":["r"]}}}}`,
		"if else": `{
			"title":"Doc","type":"object","properties":{"k":{"type":"string"}},
			"if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			"else":{"properties":{"q":{"type":"object","required":["s"]}}}}`,
		"branch behind a ref": `{
			"title":"Doc","type":"object","properties":{"k":{"type":"string"}},
			"if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			"then":{"$ref":"#/$defs/NeedsP"},
			"$defs":{"NeedsP":{"required":["p"]}}}`,
		"group in an allOf branch": `{
			"title":"Doc","type":"object","properties":{"k":{"type":"string"}},
			"allOf":[{"if":{"properties":{"k":{"const":"yes"}},"required":["k"]},
			          "then":{"properties":{"p":{"type":"object","required":["r"]}}}}]}`,
		"false branches": `{
			"title":"Doc","type":"object","properties":{"k":{"type":"string"}},
			"if":{"$ref":"#/$defs/O","properties":{"k":{"const":"yes"}},"required":["k"]},
			"then":false,"else":false,"$defs":{"O":{"type":"object"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ir := generateForItemTest(t, input)
			doc := structNamed(t, ir, "Doc")
			found := 0
			for _, c := range doc.RuntimeBranchChecks {
				if !c.MessageNamesItsOwnKeyword() {
					continue
				}
				found++
				fields := topLevelNodeFields(c.NodeLiteral)
				for _, f := range fields {
					if f != "If" && f != "Then" && f != "Else" {
						t.Errorf("the compiled group states %q beside the group itself, so it can fail for a reason the evaluator puts no keyword in front of:\n%s",
							f, c.NodeLiteral)
					}
				}
				if !slices.Contains(fields, "Then") && !slices.Contains(fields, "Else") {
					t.Errorf("the compiled group states no consequence, so nothing in it can refuse a document and name itself:\n%s",
						c.NodeLiteral)
				}
			}
			if found != 1 {
				t.Fatalf("%d groups compiled to the evaluator, want 1: %+v", found, doc.RuntimeBranchChecks)
			}
		})
	}
}

// topLevelNodeFields names the fields a _schemaNode composite literal sets at
// its own level. The literal is written one field per line at one tab of
// indent, and everything a branch states is nested deeper, so the indent is
// what tells the group's own keywords from its branches'.
func topLevelNodeFields(literal string) []string {
	var out []string
	for _, line := range strings.Split(literal, "\n") {
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		name, _, ok := strings.Cut(strings.TrimPrefix(line, "\t"), ":")
		if !ok {
			continue
		}
		out = append(out, name)
	}
	return out
}
