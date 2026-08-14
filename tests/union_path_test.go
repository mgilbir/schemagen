package tests

import "testing"

// TestUnionMessagesNameTheDocument holds a oneOf or discriminated-union refusal
// to a path a caller can follow in their own document, which is what issue #289
// was.
//
// The refusal happens in UnmarshalJSON and named a Go type and a Go field where
// the path goes:
//
//	Root.Value: no matching oneOf variant: %!w(<nil>)
//	Root.Payload: unknown discriminator value "zzz" for field "kind"
//
// Three things in those two lines. "Root.Value" is Go where everything else the
// generator prints at that position is JSON, and at depth it does not even
// resemble the path -- `arr[1]: RootArrItem.Value: ...` for the second element
// of an array. The second line mixes both conventions inside one message:
// "Payload" is the Go field and "kind" is already the property the document
// wrote. And "%!w(<nil>)" is a %w verb given a nil error: no branch had been put
// to a decode at all, because every one of them is gated on required properties
// this document does not carry, so there was no branch reason to report.
//
// The prefix is now the property that reaches the union, put in front by the
// same rule as every other message (jsonPathError), so a union that is the whole
// value has no prefix and one at depth has the whole path. The refusal with no
// branch reason behind it says only what is true. The discriminator names the
// property by the word its own neighbouring refusals use.
//
// Run compiled and compared for whole-message equality, for the reason
// TestErrorPathsNameTheDocument gives. An empty Want means the document is legal
// and must be accepted, which is what holds the verdicts still while the
// messages move.
func TestUnionMessagesNameTheDocument(t *testing.T) {
	runErrorPathFixtures(t, "union_path_test", unionPathFixtures())
}

func unionPathFixtures() []errorPathFixture {
	return []errorPathFixture{
		{
			// The issue's own reproduction, and the nil %w with it: neither
			// branch is put to a decode, because both are gated on a required
			// key a string does not carry.
			Name:   "issue_289_no_branch_matched_and_none_was_even_tried",
			Schema: `{"type":"object","properties":{"value":{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"value":"notanobject"}`,
					Want:   `value: no matching oneOf variant`,
					Reason: "was `Root.Value: no matching oneOf variant: %!w(<nil>)` -- a Go type and field, and a %w verb with nothing to wrap"},
				{Name: "ambiguity", Doc: `{"value":{"a":"x","b":1}}`,
					Want:   `value: multiple oneOf variants matched (2), expected exactly 1`,
					Reason: "was Root.Value: multiple oneOf variants matched ..."},
				{Name: "the first branch", Doc: `{"value":{"a":"x"}}`, Reason: "control: no verdict may change"},
				{Name: "the second branch", Doc: `{"value":{"b":1}}`, Reason: "control: no verdict may change"},
				{Name: "absent", Doc: `{}`, Reason: "control"},
			},
		},
		{
			// A branch that was tried has a reason, and it is still reported
			// behind the path rather than behind a Go name.
			Name:   "a_branch_reason_is_reported_behind_the_path",
			Schema: `{"type":"object","properties":{"v":{"oneOf":[{"type":"string","minLength":3},{"type":"integer","minimum":5}]}}}`,
			Cases: []errorPathCase{
				{Name: "a branch's own constraint", Doc: `{"v":"ab"}`,
					Want:   `v: no matching oneOf variant: variant String: value does not satisfy the variant's constraints`,
					Reason: "was Root.V: ...; the variant name is a branch label rather than a position, and stays"},
				{Name: "the other branch's constraint", Doc: `{"v":1}`,
					Want:   `v: no matching oneOf variant: variant Integer: value does not satisfy the variant's constraints`,
					Reason: "was Root.V: ..."},
				{Name: "the string branch", Doc: `{"v":"abc"}`, Reason: "control"},
				{Name: "the integer branch", Doc: `{"v":9}`, Reason: "control"},
			},
		},
		{
			// The discriminated form, and the two conventions that met inside
			// one message.
			Name:   "issue_289_the_discriminator_speaks_one_language",
			Schema: `{"type":"object","properties":{"payload":{"oneOf":[{"type":"object","title":"Alpha","properties":{"kind":{"const":"a"},"x":{"type":"string"}},"required":["kind"]},{"type":"object","title":"Beta","properties":{"kind":{"const":"b"},"y":{"type":"integer"}},"required":["kind"]}]}}}`,
			Cases: []errorPathCase{
				{Name: "a value no branch declares", Doc: `{"payload":{"kind":"zzz"}}`,
					Want:   `payload: unknown discriminator value "zzz" for property "kind"`,
					Reason: "was `Root.Payload: unknown discriminator value \"zzz\" for field \"kind\"` -- a Go field beside a JSON name, in one message"},
				{Name: "the discriminator is missing", Doc: `{"payload":{}}`,
					Want:   `payload: discriminator property "kind" is missing`,
					Reason: "was Root.Payload: discriminator property ...; the two refusals now use one word for one thing"},
				{Name: "inside the branch it selected", Doc: `{"payload":{"kind":"a","x":5}}`,
					Want:   `payload: variant Alpha: x: expected string, got number`,
					Reason: "was `Root.Payload (variant Alpha): x: expected string, got number`"},
				{Name: "accepts", Doc: `{"payload":{"kind":"a","x":"q"}}`, Reason: "control"},
				{Name: "accepts the other branch", Doc: `{"payload":{"kind":"b","y":1}}`, Reason: "control"},
			},
		},
		{
			// The path has to be right at depth. Before, the prefix was the Go
			// type of whatever struct happened to hold the union, so the deeper
			// the position the less it resembled the document.
			Name:   "the_path_is_right_at_depth",
			Schema: `{"type":"object","properties":{"outer":{"type":"object","properties":{"value":{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]}}},"arr":{"type":"array","items":{"type":"object","properties":{"value":{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]}}}}}}`,
			Cases: []errorPathCase{
				{Name: "two objects down", Doc: `{"outer":{"value":"notanobject"}}`,
					Want:   `outer.value: no matching oneOf variant`,
					Reason: "was `outer: RootOuter.Value: no matching oneOf variant: %!w(<nil>)`"},
				{Name: "an array element", Doc: `{"arr":[{"value":{"a":"x"}},{"value":"notanobject"}]}`,
					Want:   `arr[1].value: no matching oneOf variant`,
					Reason: "was `arr[1]: RootArrItem.Value: ...` -- the index was right and what followed it was a Go type"},
				{Name: "all legal", Doc: `{"outer":{"value":{"a":"x"}},"arr":[{"value":{"b":1}}]}`, Reason: "control"},
			},
		},
		{
			// A map value reaches the union through the accessor spelling #280
			// established, and the union's own name follows it with a dot.
			Name:   "the_path_is_right_in_a_map_value",
			Schema: `{"type":"object","additionalProperties":{"type":"object","properties":{"value":{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"kk":{"value":"notanobject"}}`,
					Want:   `["kk"].value: no matching oneOf variant`,
					Reason: "was [\"kk\"]: RootValue.Value: ..."},
				{Name: "a key with a dot in it", Doc: `{"a.b":{"value":"notanobject"}}`,
					Want:   `["a.b"].value: no matching oneOf variant`,
					Reason: "control: the brackets keep a dotted key from reading as two steps"},
				{Name: "accepts", Doc: `{"kk":{"value":{"a":"x"}}}`, Reason: "control"},
			},
		},
		{
			// A union that is the whole value has no name of its own to report
			// under, and says so by saying nothing -- the root array and root map
			// of #280 read the same way.
			Name:   "a_union_that_is_the_whole_value_has_no_name",
			Schema: `{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]}`,
			Cases: []errorPathCase{
				{Name: "ambiguity at the root", Doc: `{"a":"x","b":1}`,
					Want:   `multiple oneOf variants matched (2), expected exactly 1`,
					Reason: "was Root.Value: multiple ...; there is no member of the document to name"},
				{Name: "accepts", Doc: `{"a":"x"}`, Reason: "control"},
			},
		},
		{
			// A property whose name is a format verb, and one spelled like an
			// accessor: the name reaches the message intact and is joined the way
			// a member name is.
			Name:   "control_awkward_union_property_names",
			Schema: `{"type":"object","properties":{"a%d":{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]},"[x]":{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]}}}`,
			Cases: []errorPathCase{
				{Name: "a percent", Doc: `{"a%d":"notanobject"}`,
					Want:   `a%d: no matching oneOf variant`,
					Reason: "control: the name is carried through a format string on its way into the path"},
				{Name: "a name spelled like an accessor", Doc: `{"[x]":"notanobject"}`,
					Want:   `[x]: no matching oneOf variant`,
					Reason: "control: a property really named [x] is a member name"},
				{Name: "accepts", Doc: `{"a%d":{"a":"x"},"[x]":{"b":1}}`, Reason: "control"},
			},
		},
	}
}
