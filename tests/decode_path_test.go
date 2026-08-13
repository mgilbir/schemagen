package tests

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
)

// TestDecodePathsNameTheDocument holds the messages a document is refused with
// at *decode* time to the same path a Validate refusal carries, which is what
// issue #282 was about.
//
// The refusal happens in UnmarshalJSON, before Validate runs, and what reached
// the caller was encoding/json's own text or a message local to the leaf that
// raised it. Three shapes of that, all from one cause -- the decode of every
// declared member happens in one call, so the position at fault is whatever
// encoding/json chose to say about the Go value it was filling:
//
//   - a member's own type refused and its message arrived with nothing in front
//     of it, so {"name":null} and {"owner":{"name":null}} were one message, and
//     the second named a root property that is not at fault;
//   - the path encoding/json does write is a Go field path through the
//     `type Alias T` shadow the decode goes through, and carries no array index
//     at all -- a caller with a 200-element array learned nothing;
//   - a leaf that answers for its own decode is handed the bytes, so its words
//     are the whole message: `cannot unmarshal string into Go value of type
//     int64`, `ParseAddr("nope"): unable to parse IP`, and a null refused under
//     the name of a Go type nobody wrote.
//
// The fix is that a failed decode is traced back to the member at fault by
// putting each declared member the document carries to its own decode again
// (see StructDef.DecodeMembers and jsonDecodeMemberError), and that the leaves
// answer in the schema's words rather than the destination type's.
//
// Run compiled, and compared for whole-message equality, for the reasons
// TestErrorPathsNameTheDocument gives: the answer is a string a document
// produces at run time, and a wrong path is a right path with something extra
// glued to it, so a containment check passes under the very defect.
func TestDecodePathsNameTheDocument(t *testing.T) {
	runErrorPathFixtures(t, "decode_path_test", decodePathFixtures())
}

func decodePathFixtures() []errorPathFixture {
	return []errorPathFixture{
		{
			// The issue's own reproduction of the first defect. Two different
			// violations gave one message, and the second pointed at "name" --
			// a real root property, which in this document is legal.
			Name: "null_refusals_name_the_member_that_carries_them",
			Schema: `{"properties":{"name":{"type":"string"},
			           "owner":{"type":"object","properties":{"name":{"type":"string"}}}}}`,
			Cases: []errorPathCase{
				{Name: "at the root", Doc: `{"name":null}`,
					Want:   `name: null is not allowed`,
					Reason: "control: the declared-property position was already right"},
				{Name: "one level down", Doc: `{"owner":{"name":null}}`,
					Want:   `owner.name: null is not allowed`,
					Reason: "was `name: null is not allowed` -- the nested type's message with nothing in front of it, naming a root property that is not at fault"},
				{Name: "both legal", Doc: `{"name":"a","owner":{"name":"b"}}`,
					Reason: "control: no verdict may change"},
				{Name: "absent is not null", Doc: `{"owner":{}}`,
					Reason: "control: an absent property is not a null one"},
			},
		},
		{
			// The four scalars answered four different ways, and only the
			// integer had no path at all.
			Name:   "scalar_type_mismatches_carry_the_member_and_the_schema's_words",
			Schema: `{"type":"object","properties":{"s":{"type":"string"},"n":{"type":"integer"},"f":{"type":"number"},"b":{"type":"boolean"}}}`,
			Cases: []errorPathCase{
				{Name: "string", Doc: `{"s":5}`, Want: `s: expected string, got number`,
					Reason: "was `json: cannot unmarshal number into Go struct field .Alias.s of type string`"},
				{Name: "integer", Doc: `{"n":"x"}`, Want: `n: expected integer, got string`,
					Reason: "was `cannot unmarshal string into Go value of type int64` -- no path at all, because the shadow answers for its own decode and encoding/json never decorates it"},
				{Name: "number", Doc: `{"f":"x"}`, Want: `f: expected number, got string`,
					Reason: "was `json: cannot unmarshal string into Go struct field .Alias.f of type float64`"},
				{Name: "boolean", Doc: `{"b":"x"}`, Want: `b: expected boolean, got string`,
					Reason: "was `json: cannot unmarshal string into Go struct field .Alias.b of type bool`"},
				{Name: "a boolean where a string is wanted", Doc: `{"s":true}`,
					Want:   `s: expected string, got boolean`,
					Reason: "the token encoding/json saw is respelled too: it says bool, and JSON Schema spells the type boolean"},
				{Name: "a number no integer holds", Doc: `{"n":1.5}`,
					Want:   `n: value 1.5 cannot be held as an integer`,
					Reason: "was `value 1.5 cannot be represented as int64` -- no path, and int64 is a fact about this program rather than about the document"},
				{Name: "a magnitude no integer holds", Doc: `{"n":9223372036854775808}`,
					Want:   `n: value 9223372036854775808 cannot be held as an integer`,
					Reason: "the same sentence: to a caller who asked for an integer, a fractional part and an unrepresentable magnitude are one answer"},
				{Name: "a magnitude no number holds", Doc: `{"f":1e400}`,
					Want:   `f: value 1e400 cannot be held as a number`,
					Reason: "was `json: cannot unmarshal number 1e400 into Go struct field .Alias.f of type float64`; `expected number, got number 1e400` would be a mystery, since what is wrong is the value and not its type"},
				{Name: "the member at fault is named among legal siblings", Doc: `{"s":"a","f":1.5,"b":true,"n":"x"}`,
					Want:   `n: expected integer, got string`,
					Reason: "the trace names the one member that refuses, not the first one declared"},
				{Name: "all legal", Doc: `{"s":"a","n":1,"f":1.5,"b":true}`, Reason: "control"},
				{Name: "an integer written 1.0 is still an integer", Doc: `{"n":1.0}`,
					Reason: "control: the member is probed through the same shadow the decode uses, so draft 6's integer is not reported as a fault"},
				{Name: "a legal 1.0 beside a real fault", Doc: `{"n":1.0,"s":5}`,
					Want:   `s: expected string, got number`,
					Reason: "the shadow again, and this is where dropping it shows: probed as a bare int64, 1.0 refuses and n is named for a document whose fault is s",
				},
			},
		},
		{
			// Draft 4 requires the integer *token*, so an integer there is a
			// bare int64 with no shadow in front of it and encoding/json raises
			// the refusal itself. The two routes have to give one answer: which
			// of them a position takes is a fact about its dialect, and a caller
			// reading the message is not asking about dialects.
			Name:   "an_integer_refused_by_the_decoder_reads_as_one_refused_by_the_shadow",
			Schema: `{"$schema":"http://json-schema.org/draft-04/schema#","type":"object","properties":{"i":{"type":"integer"},"s":{"type":"string"}}}`,
			Cases: []errorPathCase{
				{Name: "a fractional value", Doc: `{"i":1.5}`,
					Want:   `i: value 1.5 cannot be held as an integer`,
					Reason: "was `json: cannot unmarshal number 1.5 into Go struct field .Alias.i of type int64`"},
				{Name: "the integer token draft 4 requires", Doc: `{"i":1.0}`,
					Want:   `i: value 1.0 cannot be held as an integer`,
					Reason: "control: draft 4 refuses the float spelling, and that verdict does not move"},
				{Name: "a type mismatch is still a type mismatch", Doc: `{"i":"x"}`,
					Want:   `i: expected integer, got string`,
					Reason: "the literal only appears where the token was of the right kind"},
				{Name: "accepts", Doc: `{"i":2,"s":"x"}`, Reason: "control"},
			},
		},
		{
			// The Alias segments, and the array index that was not there.
			Name: "decode_paths_are_json_paths_and_carry_the_index",
			Schema: `{"type":"object","properties":{
			           "a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{"type":"string"}}}}},
			           "items":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
			           "grid":{"type":"array","items":{"type":"array","items":{"type":"string"}}},
			           "m":{"type":"object","additionalProperties":{"type":"string"}}}}`,
			Cases: []errorPathCase{
				{Name: "three members deep", Doc: `{"a":{"b":{"c":5}}}`,
					Want:   `a.b.c: expected string, got number`,
					Reason: "was `json: cannot unmarshal number into Go struct field .Alias.a.Alias.b.Alias.c of type string`"},
				{Name: "the second element of an array", Doc: `{"items":[{"name":"aaa"},{"name":5}]}`,
					Want:   `items[1].name: expected string, got number`,
					Reason: "was `... .Alias.items.Alias.name ...`: the index vanished, so a 200-element array said nothing about which element"},
				{Name: "an element of an element", Doc: `{"grid":[["a"],["b",5]]}`,
					Want:   `grid[1][1]: expected string, got number`,
					Reason: "both indices, because each container level is opened up rather than decoded whole"},
				{Name: "a map value", Doc: `{"m":{"kk":5}}`,
					Want:   `m["kk"]: expected string, got number`,
					Reason: "the key in brackets and quoted, for the reason issue #280 gave: a key may contain a dot"},
				{Name: "the whole member is of the wrong kind", Doc: `{"items":"x"}`,
					Want:   `items: expected array, got string`,
					Reason: "was `json: cannot unmarshal string into Go struct field .Alias.items of type []...`"},
				{Name: "all legal", Doc: `{"a":{"b":{"c":"x"}},"items":[{"name":"aaa"}],"grid":[["a"]],"m":{"kk":"v"}}`,
					Reason: "control"},
			},
		},
		{
			// #276 reached the declared-property positions and no others.
			Name: "a_null_element_and_a_null_map_value_are_named_by_position",
			Schema: `{"type":"object","properties":{
			           "arr":{"type":"array","items":{"type":"object","properties":{"x":{"type":"string"}}}},
			           "m":{"type":"object","additionalProperties":{"type":"object","properties":{"y":{"type":"string"}}}},
			           "o":{"type":"object","properties":{"z":{"type":"string"}}}}}`,
			Cases: []errorPathCase{
				{Name: "array element", Doc: `{"arr":[{"x":"a"},null]}`,
					Want:   `arr[1]: null is not allowed`,
					Reason: "was `null is not allowed for type RootArrItem` -- a Go type the caller never wrote, and no position"},
				{Name: "map value", Doc: `{"m":{"kk":null}}`,
					Want:   `m["kk"]: null is not allowed`,
					Reason: "was `null is not allowed for type RootMValue`"},
				{Name: "declared property", Doc: `{"o":null}`,
					Want:   `o: null is not allowed`,
					Reason: "control: this is the position #276 reached"},
				{Name: "all legal", Doc: `{"arr":[{"x":"a"}],"m":{"kk":{"y":"b"}},"o":{"z":"c"}}`,
					Reason: "control"},
			},
		},
		{
			// A root scalar and a root container have no path of their own, and
			// say so by saying nothing rather than by naming their Go type.
			Name:   "a_root_null_names_no_type",
			Schema: `{"type":"object","properties":{"a":{"type":"string"}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `null`, Want: `null is not allowed`,
					Reason: "was `null is not allowed for type Root`"},
				{Name: "accepts", Doc: `{"a":"x"}`, Reason: "control"},
			},
		},
		{
			// A format at a position the assertion gives a Go type to was
			// refused by that type's parser, which names neither the keyword nor
			// the path. One sibling string keyword away, the same format at the
			// same position was already answered for properly.
			Name: "a_typed_format_is_refused_in_the_schema's_words",
			Schema: `{"type":"object","properties":{
			           "plainIpv4":{"type":"string","format":"ipv4"},
			           "constrainedIpv4":{"type":"string","format":"ipv4","minLength":1},
			           "v6":{"type":"string","format":"ipv6"},
			           "stamp":{"type":"string","format":"date-time"},
			           "list":{"type":"array","items":{"type":"string","format":"ipv4"}}}}`,
			Config: func(c *generator.Config) { c.FormatAssertion = true },
			Cases: []errorPathCase{
				{Name: "ipv4", Doc: `{"plainIpv4":"nope"}`,
					Want:   `plainIpv4: "nope" is not a valid IPv4 address`,
					Reason: "was `ParseAddr(\"nope\"): unable to parse IP`"},
				{Name: "the same format with a sibling keyword", Doc: `{"constrainedIpv4":"nope"}`,
					Want:   `constrainedIpv4: "nope" is not a valid IPv4 address`,
					Reason: "control: this position was already right, and the two now read the same"},
				{Name: "ipv6", Doc: `{"v6":"nope"}`,
					Want:   `v6: "nope" is not a valid IPv6 address`,
					Reason: "was `ParseAddr(\"nope\"): unable to parse IP`"},
				{Name: "date-time", Doc: `{"stamp":"nope"}`,
					Want:   `stamp: "nope" is not a valid date-time (RFC 3339)`,
					Reason: "was `parsing time \"nope\" as \"2006-01-02T15:04:05Z07:00\": cannot parse \"nope\" as \"2006\"`"},
				{Name: "date-time given a number", Doc: `{"stamp":5}`,
					Want:   `stamp: expected string, got number`,
					Reason: "was `Time.UnmarshalJSON: input is not a JSON string`, which names a Go method and not the token"},
				{Name: "an ipv4 inside an array", Doc: `{"list":["1.2.3.4","nope"]}`,
					Want:   `list[1]: "nope" is not a valid IPv4 address`,
					Reason: "the element position takes the same shadow, chosen from the format on the items schema"},
				{Name: "the lower case spelling RFC 3339 permits", Doc: `{"stamp":"2020-01-02t03:04:05z"}`,
					Reason: "control: issue #264's acceptance is not given up for the wording"},
				{Name: "an ipv6 written where ipv4 is asked", Doc: `{"plainIpv4":"::1"}`,
					Want:   `plainIpv4: "::1" is not a valid IPv4 address`,
					Reason: "control: the address family is Validate's verdict and stays there, so the value still decodes and is still refused"},
				{Name: "all legal", Doc: `{"plainIpv4":"1.2.3.4","constrainedIpv4":"1.2.3.4","v6":"::1","stamp":"2020-01-02T03:04:05Z","list":["1.2.3.4"]}`,
					Reason: "control"},
				{Name: "the empty string still decodes", Doc: `{"plainIpv4":""}`,
					Reason: "control: netip.Addr's own decoder reads empty text as the zero address and reports nothing, and the shadow stands in for that decoder"},
			},
		},
		{
			// A pointer is not descended through by the probe, because
			// encoding/json fills a settable pointer with nil for a JSON null
			// without consulting the type's own UnmarshalJSON at all.
			Name:   "a_legal_null_object_is_not_named_for_a_fault_elsewhere",
			Schema: `{"type":"object","properties":{"nn":{"type":["object","null"],"properties":{"q":{"type":"string"}}},"s":{"type":"string"}}}`,
			Cases: []errorPathCase{
				{Name: "the fault is the sibling", Doc: `{"nn":null,"s":5}`,
					Want:   `s: expected string, got number`,
					Reason: "probing nn as its value type rather than as the pointer would refuse the null the schema admits, and name a member that is not at fault"},
				{Name: "the null alone is legal", Doc: `{"nn":null}`, Reason: "control"},
				{Name: "accepts", Doc: `{"nn":{"q":"x"},"s":"y"}`, Reason: "control"},
			},
		},
		{
			// The same choice where the null is not legal: which member is
			// reported still follows what the decode itself was handed. The
			// pointer is nil-filled without the type's UnmarshalJSON being
			// consulted, so the type mismatch is what the decode failed on and
			// is what the trace finds.
			Name:   "a_pointer_member_is_probed_as_the_pointer",
			Schema: `{"type":"object","properties":{"o":{"type":"object","properties":{"q":{"type":"string"}}},"s":{"type":"string"}}}`,
			Cases: []errorPathCase{
				{Name: "a null pointer beside a type mismatch", Doc: `{"o":null,"s":5}`,
					Want:   `s: expected string, got number`,
					Reason: "probing o as its value type would call that type's UnmarshalJSON with the null the decode never gave it, and report o instead"},
				{Name: "the null on its own", Doc: `{"o":null}`, Want: `o: null is not allowed`,
					Reason: "control: the null rule still refuses it, from the block that reads the document's keys"},
				{Name: "accepts", Doc: `{"o":{"q":"x"},"s":"y"}`, Reason: "control"},
			},
		},
		{
			// The overflow map's values are a position no Go field answers for,
			// and it is joined by the accessor rule rather than the member one.
			Name:   "an_overflow_value_is_named_by_its_key",
			Schema: `{"type":"object","properties":{"cfg":{"type":"object","additionalProperties":{"type":"integer"}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"cfg":{"a.b":"x"}}`,
					Want:   `cfg["a.b"]: expected integer, got string`,
					Reason: "the key is bracketed and quoted, so a dot in it does not read as a second step"},
				{Name: "accepts", Doc: `{"cfg":{"a.b":1}}`, Reason: "control"},
			},
		},
		{
			// The null walker reached from an overflow value: the key it is
			// joined under is an accessor, and so is everything the walker adds
			// below it.
			// The map is reached through a $ref so that it is a type of its own
			// and answers for its own values: written inline, the parent covers
			// the same positions from its own null rule, and the site under test
			// is never reached.
			Name: "a_null_below_an_overflow_value_keeps_every_accessor",
			Schema: `{"type":"object","properties":{"cfg":{"$ref":"#/$defs/M"}},
			  "$defs":{"M":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"cfg":{"a.b":["x",null]}}`,
					Want:   `cfg["a.b"][1]: null is not allowed`,
					Reason: "the key and the index are both accessors, and neither takes a dot in front of it"},
				{Name: "the value is of the wrong kind", Doc: `{"cfg":{"a.b":5}}`,
					Want:   `cfg["a.b"]: expected array, got number`,
					Reason: "was `[\"a.b\"]: json: cannot unmarshal number into Go value of type []string`"},
				{Name: "accepts", Doc: `{"cfg":{"a.b":["x"]}}`, Reason: "control"},
			},
		},
		{
			// An alias decodes through a `type Alias T` of its own, which is
			// what encoding/json names when the value will not fill it.
			Name:   "an_alias_refuses_in_the_schema's_words_too",
			Schema: `{"type":"object","properties":{"p":{"$ref":"#/$defs/T"}},"$defs":{"T":{"type":"string","minLength":3}}}`,
			Cases: []errorPathCase{
				{Name: "under a member", Doc: `{"p":5}`,
					Want:   `p: expected string, got number`,
					Reason: "was `json: cannot unmarshal number into Go struct field .Alias.p of type main.Alias` -- a type that exists for four lines inside the alias's own decoder, qualified by the caller's package"},
				{Name: "the root is not an object at all", Doc: `5`,
					Want:   `expected object, got number`,
					Reason: "was `json: cannot unmarshal number into Go value of type struct { *main.Alias }`, which names the shadow struct the decode is built on"},
				{Name: "accepts", Doc: `{"p":"abc"}`, Reason: "control"},
				{Name: "the constraint still speaks for itself", Doc: `{"p":"ab"}`,
					Want:   `p: length 2 is less than minimum 3`,
					Reason: "control: issue #279's result does not move"},
			},
		},
		{
			// The same two shadows where the type *is* the document, which is
			// the only position their own words reach the caller unaltered: at
			// a member the trace reads the refusal on the way past.
			Name:   "a_root_alias_refuses_in_the_schema's_words",
			Schema: `{"type":"string","minLength":3}`,
			Cases: []errorPathCase{
				{Name: "a number", Doc: `5`, Want: `expected string, got number`,
					Reason: "was `json: cannot unmarshal number into Go value of type main.Alias`"},
				{Name: "an object", Doc: `{"kk":"v"}`, Want: `expected string, got object`,
					Reason: "was the same, naming the alias shadow"},
				{Name: "accepts", Doc: `"abc"`, Reason: "control"},
				{Name: "the constraint still speaks for itself", Doc: `"ab"`,
					Want: `length 2 is less than minimum 3`, Reason: "control: issue #279's result does not move"},
			},
		},
		{
			Name:   "a_root_map_refuses_in_the_schema's_words",
			Schema: `{"type":"object","additionalProperties":{"type":"string"}}`,
			Cases: []errorPathCase{
				{Name: "a number", Doc: `5`, Want: `expected object, got number`,
					Reason: "was `json: cannot unmarshal number into Go value of type struct { *main.Alias }` -- the anonymous struct the decode is built on, which no schema and no caller ever named"},
				{Name: "a string", Doc: `"abc"`, Want: `expected object, got string`,
					Reason: "the same"},
				{Name: "a null value", Doc: `{"kk":null}`, Want: `["kk"]: null is not allowed`,
					Reason: "control: issue #280's result does not move"},
				{Name: "accepts", Doc: `{"kk":"v"}`, Reason: "control"},
			},
		},
		{
			// A property whose name cannot go in a struct tag is decoded by
			// hand, in a block of its own that the trace never reaches -- so it
			// names the position itself, and by the name the document wrote.
			Name:   "a_hand_decoded_property_names_the_property",
			Schema: `{"type":"object","properties":{"a\"b":{"type":"string"},"a\\b":{"type":"integer"}}}`,
			Cases: []errorPathCase{
				{Name: "a quote in the name", Doc: `{"a\"b":5}`,
					Want:   `a"b: expected string, got number`,
					Reason: "was `unmarshaling Root.AB2: json: cannot unmarshal number into Go value of type string` -- a Go field name the caller cannot find, for a property that exists precisely because the name is unspellable as a tag"},
				{Name: "a backslash in the name", Doc: `{"a\\b":"x"}`,
					Want:   `a\b: expected integer, got string`,
					Reason: "was `unmarshaling Root.AB3: cannot unmarshal string into Go value of type int64`"},
				{Name: "accepts", Doc: `{"a\"b":"v","a\\b":1}`, Reason: "control"},
			},
		},
		{
			// A property name that is a format verb, and one that reads like an
			// accessor, both survive the trace intact.
			Name:   "control_awkward_member_names_reach_the_message_intact",
			Schema: `{"type":"object","properties":{"a%d":{"type":"string"},"[x]":{"type":"object","properties":{"n":{"type":"string"}}}}}`,
			Cases: []errorPathCase{
				{Name: "a percent", Doc: `{"a%d":5}`, Want: `a%d: expected string, got number`,
					Reason: "control: the name is carried through a format string on its way into the path"},
				{Name: "a name spelled like an accessor", Doc: `{"[x]":{"n":5}}`,
					Want:   `[x].n: expected string, got number`,
					Reason: "control: a property really named [x] is a member name and is joined with a dot"},
				{Name: "accepts", Doc: `{"a%d":"v","[x]":{"n":"v"}}`, Reason: "control"},
			},
		},
	}
}
