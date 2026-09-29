package validation

import "testing"

// TestTypeSchemaCyclesAreJudgedWithoutLooping holds a draft 3 schema-valued
// "type" entry that leads back to its own definition to a verdict, where the
// generated Validate used to call itself until the stack ran out -- on any
// document, since the cycle never reaches into the value.
//
// Such a schema's meaning is left undefined by JSON Schema. The reading taken
// is the least one (see the generator's resolveTypeBranchesInPlace): a branch
// that re-enters a definition already judging the same value contributes
// nothing, so a definition is exactly what its other alternatives admit, and
// one with no other alternative admits nothing.
func TestTypeSchemaCyclesAreJudgedWithoutLooping(t *testing.T) {
	t.Parallel()
	runErrorPathFixtures(t, "type_schema_cycle_test", []errorPathFixture{
		{
			Name:   "a_definition_whose_only_type_is_itself_admits_nothing",
			Schema: `{"$defs":{"C":{"type":[{"$ref":"#/$defs/C"}]}},"$ref":"#/$defs/C"}`,
			Cases: []errorPathCase{
				{Name: "a string", Doc: `"x"`, Want: `type: string is not allowed`,
					Reason: "was a stack overflow"},
				{Name: "an object", Doc: `{}`, Want: `type: object is not allowed`,
					Reason: "was a stack overflow"},
			},
		},
		{
			Name:   "a_self_reference_beside_a_type_admits_that_type",
			Schema: `{"$defs":{"C":{"type":["string",{"$ref":"#/$defs/C"}]}},"$ref":"#/$defs/C"}`,
			Cases: []errorPathCase{
				{Name: "a string", Doc: `"x"`, Reason: "the one alternative that is not the cycle"},
				{Name: "a number", Doc: `5`, Want: `type: number is not allowed`,
					Reason: "was a stack overflow"},
			},
		},
		{
			Name: "a_mutual_cycle_admits_what_either_side_adds",
			Schema: `{"$defs":{"A":{"type":[{"$ref":"#/$defs/B"},"string"]},
			                   "B":{"type":[{"$ref":"#/$defs/A"},"number"]}},"$ref":"#/$defs/A"}`,
			Cases: []errorPathCase{
				{Name: "a string", Doc: `"x"`, Reason: "A's own alternative"},
				{Name: "a number", Doc: `5`, Reason: "B's alternative, reached through A"},
				{Name: "an object", Doc: `{}`, Want: `type: object is not allowed`,
					Reason: "was a stack overflow"},
			},
		},
	})
}
