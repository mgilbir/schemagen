package tests

import "testing"

// nullSubschemaFixtures are issue #222's two null-typed value positions, with
// every sibling position beside them as a control.
//
// `{"type":"null"}` is the one JSON type no Go type stands for. Every other type
// is enforced by decoding the value into the Go type that holds it and letting
// the decoder refuse the rest; `null` has none, and *any -- which is what a
// position falling through to resolveType gets -- decodes a null and every other
// JSON value besides. So the two positions that never called the arm which
// materializes the wrapper accepted every value of every type, and the Validate
// they emitted read like a schema with nothing left to check.
//
// The two were the overflow map of an object with a schema-valued
// additionalProperties -- in both spellings, with declared properties beside it
// and without -- and a leftover key judged by a schema-valued
// unevaluatedProperties. `unevaluatedItems` had always checked the identical
// sub-schema, which is what makes the second one an omission rather than a
// limit: unevaluatedSubschemaKeywords names `type` as a keyword both positions
// carry, and only one of them carried all of it.
func nullSubschemaFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "null_subschema_value_positions",
			SchemaPath: "testdata/schemas/regression/null_subschema_value_positions.json",
			Instances: []notInstance{
				{Name: "overflow value rejects", Doc: `{"leftover":1}`, Valid: false,
					Why: "issue #222: the root's additionalProperties says every extra key is null, and the overflow map was typed *any, which holds anything"},
				{Name: "overflow value accepts", Doc: `{"leftover":null}`, Valid: true,
					Why: "control: a null must still be accepted; a fix that refuses it is an amputation"},

				{Name: "unevaluated property rejects", Doc: `{"atUnevaluatedProperty":{"q":"x","p":1}}`, Valid: false,
					Why: "issue #222: unevaluatedProperties has no Go type for null, and answered \"allow everything\" instead"},
				{Name: "unevaluated property accepts", Doc: `{"atUnevaluatedProperty":{"q":"x","p":null}}`, Valid: true,
					Why: "control for the above"},
				{Name: "unevaluated property, nothing left over", Doc: `{"atUnevaluatedProperty":{"q":"x"}}`, Valid: true,
					Why: "control: `properties` evaluates q, so the sub-schema judges nothing"},

				{Name: "property rejects", Doc: `{"atProperty":1}`, Valid: false,
					Why: "control: a declared property has always materialized this shape"},
				{Name: "property accepts", Doc: `{"atProperty":null}`, Valid: true, Why: "control for the above"},

				{Name: "element rejects", Doc: `{"atElement":[1]}`, Valid: false,
					Why: "control: the element position was fixed for this shape by #126"},
				{Name: "element accepts", Doc: `{"atElement":[null]}`, Valid: true, Why: "control for the above"},

				{Name: "map value rejects", Doc: `{"atMapValue":{"k":1}}`, Valid: false,
					Why: "control: a map reached as a property resolves its value through resolveArrayItemType and already worked"},
				{Name: "map value accepts", Doc: `{"atMapValue":{"k":null}}`, Valid: true, Why: "control for the above"},

				{Name: "pattern property rejects", Doc: `{"atPattern":{"ab":1}}`, Valid: false, Why: "control"},
				{Name: "pattern property accepts", Doc: `{"atPattern":{"ab":null}}`, Valid: true, Why: "control for the above"},
				{Name: "pattern property, no key the pattern names", Doc: `{"atPattern":{"b":1}}`, Valid: true,
					Why: "control: patternProperties says nothing about a key no pattern matches"},

				{Name: "tuple slot rejects", Doc: `{"atTupleSlot":[1]}`, Valid: false, Why: "control"},
				{Name: "tuple slot accepts", Doc: `{"atTupleSlot":[null]}`, Valid: true, Why: "control for the above"},

				{Name: "unevaluated item rejects", Doc: `{"atUnevaluatedItem":[true,1]}`, Valid: false,
					Why: "control, and the pair the unevaluatedProperties row above is judged against: this half has always checked the sub-schema"},
				{Name: "unevaluated item accepts", Doc: `{"atUnevaluatedItem":[true,null]}`, Valid: true, Why: "control for the above"},

				{Name: "contains rejects", Doc: `{"atContains":[1]}`, Valid: false,
					Why: "control: no element is a null, so nothing matches the contains schema"},
				{Name: "contains accepts", Doc: `{"atContains":[1,null]}`, Valid: true,
					Why: "control: contains asks for one element, not all of them"},

				{Name: "nothing present", Doc: `{}`, Valid: true,
					Why: "control: every declared property is optional and there is no extra key"},
			},
		},
		{
			Name:       "null_subschema_map_root",
			SchemaPath: "testdata/schemas/regression/null_subschema_map_root.json",
			Instances: []notInstance{
				{Name: "value rejects", Doc: `{"k":1}`, Valid: false,
					Why: "issue #222 as reported: an object whose whole shape is additionalProperties reaches the value type by a different arm than the struct above, and it was typed *any"},
				{Name: "value accepts", Doc: `{"k":null}`, Valid: true,
					Why: "control: a null must still be accepted"},
				{Name: "no keys", Doc: `{}`, Valid: true,
					Why: "control: additionalProperties says nothing about an object with no keys"},
			},
		},
	}
}

// TestNullTypedSubschemaBindsAtEveryValuePosition runs each fixture compiled.
func TestNullTypedSubschemaBindsAtEveryValuePosition(t *testing.T) {
	runInstanceFixtures(t, "null_subschema_test", nullSubschemaFixtures())
}
