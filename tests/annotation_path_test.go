package tests

import "testing"

// TestAnnotationSchemaMessagesJoinAsTheyAre holds the verdict of a schema kept
// as data to the same join rule as every other message, which is what issue #284
// was.
//
// A type whose whole schema is interpreted at validation time hands its
// evaluator's reason up, and that reason is one of two things: a sentence about
// the value ("value matches no anyOf branch") or a message that opens with a
// step into it ("then: ...", "property q: ..."). The site handed both up bare,
// so the container glued a "." in front of whichever arrived and a sentence came
// out looking like a path:
//
//	p.value matches no anyOf branch
//
// There is no member p.value, and a document may carry a perfectly legal "value"
// property beside p -- the message then sends the caller to a member that is not
// at fault. It is #279 and #280's defect at a site #283's mechanism did not
// reach.
//
// The evaluator now records which of the two it built where the failure is
// raised, the way #285 made the failing branch name itself, and _evalError puts
// the reason behind the path with the separator that goes with it.
//
// Run compiled and compared for whole-message equality, for the reason
// TestErrorPathsNameTheDocument gives: a wrong path is a right path with
// something extra glued to it, so a containment check passes under the very
// defect. An empty Want means the document is legal and must be accepted, which
// is what holds the verdicts still while the messages move.
func TestAnnotationSchemaMessagesJoinAsTheyAre(t *testing.T) {
	runErrorPathFixtures(t, "annotation_path_test", annotationPathFixtures())
}

func annotationPathFixtures() []errorPathFixture {
	return []errorPathFixture{
		{
			// The issue's own reproduction. unevaluatedItems beside the anyOf is
			// what puts this property on the runtime evaluator.
			Name:   "issue_284_a_value_sentence_is_not_a_member",
			Schema: `{"type":"object","properties":{"p":{"anyOf":[{"type":"string","minLength":3},{"type":"integer"}],"unevaluatedItems":false}}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"p":"ab"}`, Want: `p: value matches no anyOf branch`,
					Reason: "was `p.value matches no anyOf branch` -- a sentence joined as though it were a member name"},
				{Name: "the string branch", Doc: `{"p":"abc"}`, Reason: "control: no verdict may change"},
				{Name: "the integer branch", Doc: `{"p":7}`, Reason: "control: no verdict may change"},
				{Name: "absent", Doc: `{}`, Reason: "control: an absent optional property is the parent's business"},
			},
		},
		{
			// A document with a real "value" property beside the one at fault:
			// the invented segment named a member that exists and is legal.
			Name:   "the_invented_segment_named_a_real_and_legal_sibling",
			Schema: `{"type":"object","properties":{"p":{"anyOf":[{"type":"string","minLength":3},{"type":"integer"}],"unevaluatedItems":false},"value":{"type":"integer","minimum":100}}}`,
			Cases: []errorPathCase{
				{Name: "the evaluated property is at fault", Doc: `{"p":"ab","value":200}`,
					Want:   `p: value matches no anyOf branch`,
					Reason: "was p.value, which is a member of this document and is legal here"},
				{Name: "the real value property is at fault", Doc: `{"p":"abc","value":1}`,
					Want:   `value: value 1 is less than minimum 100`,
					Reason: "control: a member actually named value still reports under its own name"},
				{Name: "both legal", Doc: `{"p":"abc","value":200}`, Reason: "control"},
			},
		},
		{
			// Every other shape of sentence the evaluator raises, through the
			// same site.
			Name:   "the_other_sentences_the_evaluator_raises",
			Schema: `{"type":"object","properties":{"p":{"anyOf":[{"type":"object","properties":{"q":{"type":"string","minLength":3}},"required":["q"]},{"type":"integer"}],"unevaluatedItems":false},"n":{"not":{"type":"string"},"unevaluatedItems":false},"o":{"oneOf":[{"type":"integer","minimum":0},{"type":"integer","maximum":10}],"unevaluatedItems":false}}}`,
			Cases: []errorPathCase{
				{Name: "not", Doc: `{"n":"s"}`, Want: `n: value matches a forbidden schema`,
					Reason: "was n.value matches a forbidden schema"},
				{Name: "oneOf ambiguity", Doc: `{"o":5}`,
					Want:   `o: value matches 2 oneOf branches, expected exactly 1`,
					Reason: "was o.value matches 2 oneOf branches ..."},
				{Name: "a branch's own object constraint", Doc: `{"p":{}}`,
					Want:   `p: value matches no anyOf branch`,
					Reason: "the anyOf is what refused, and it speaks about the value"},
				{Name: "below the maximum only", Doc: `{"o":-1}`, Reason: "control"},
				{Name: "above the minimum only", Doc: `{"o":20}`, Reason: "control"},
				{Name: "not, satisfied", Doc: `{"n":5}`, Reason: "control"},
			},
		},
		{
			// The neighbouring case the issue names: a reason that opens with a
			// step keeps the "." it always had. This is #285's result at this
			// site, and the two kinds of reason are now told apart rather than
			// both being guessed at.
			Name:   "control_a_reason_that_opens_with_a_step_keeps_its_dot",
			Schema: `{"type":"object","properties":{"p":{"if":{"type":"object"},"then":{"required":["r"]},"unevaluatedItems":false},"e":{"if":{"type":"object"},"else":{"type":"string"},"unevaluatedItems":false},"m":{"allOf":[{"type":"object","properties":{"k":{"type":"string","minLength":3}}}],"unevaluatedItems":false}}}`,
			Cases: []errorPathCase{
				{Name: "then", Doc: `{"p":{}}`, Want: `p.then: object is missing required property r`,
					Reason: "control: the branch names itself and the dotted keyword segment is a location a caller can follow"},
				{Name: "else", Doc: `{"e":5}`, Want: `e.else: value is not of type string`,
					Reason: "control: the same for the other branch"},
				{Name: "a property the evaluator descended into", Doc: `{"m":{"k":"ab"}}`,
					Want:   `m.property k: string is shorter than the minimum length`,
					Reason: "control: a step into the value, joined with a dot"},
				{Name: "a sentence re-raised by an in-place applicator", Doc: `{"m":5}`,
					Want:   `m: value is not of type object`,
					Reason: "the allOf branch judges the same value and says nothing about a member, so its sentence stays a sentence on the way up"},
				{Name: "all legal", Doc: `{"p":{"r":1},"e":"s","m":{"k":"abc"}}`, Reason: "control"},
			},
		},
		{
			// The path has to be right at depth, not only one level down.
			Name:   "the_path_is_right_at_depth",
			Schema: `{"type":"object","properties":{"outer":{"type":"object","properties":{"p":{"anyOf":[{"type":"string","minLength":3},{"type":"integer"}],"unevaluatedItems":false}}},"arr":{"type":"array","items":{"anyOf":[{"type":"string","minLength":3},{"type":"integer"}],"unevaluatedItems":false}}}}`,
			Cases: []errorPathCase{
				{Name: "two objects down", Doc: `{"outer":{"p":"ab"}}`,
					Want:   `outer.p: value matches no anyOf branch`,
					Reason: "was outer.p.value matches no anyOf branch"},
				{Name: "an array element", Doc: `{"arr":["abc","ab"]}`,
					Want:   `arr[1]: value matches no anyOf branch`,
					Reason: "was arr[1].value matches no anyOf branch; the index is the last step and takes nothing after it"},
				{Name: "all legal", Doc: `{"outer":{"p":"abc"},"arr":["abc",7]}`, Reason: "control"},
			},
		},
		{
			// A map value, which reaches the same type through the accessor
			// spelling #280 established.
			Name:   "the_path_is_right_in_a_map_value",
			Schema: `{"type":"object","additionalProperties":{"anyOf":[{"type":"string","minLength":3},{"type":"integer"}],"unevaluatedItems":false}}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `{"kk":"ab"}`, Want: `["kk"]: value matches no anyOf branch`,
					Reason: "was [\"kk\"].value matches no anyOf branch"},
				{Name: "a key with a dot in it", Doc: `{"a.b":"ab"}`,
					Want:   `["a.b"]: value matches no anyOf branch`,
					Reason: "control: the brackets are what keep a dotted key from reading as two steps"},
				{Name: "accepts", Doc: `{"kk":"abc"}`, Reason: "control"},
			},
		},
		{
			// At the root there is no path in front, so the sentence stands
			// alone -- and must not acquire a separator of its own.
			Name:   "control_at_the_root_the_sentence_stands_alone",
			Schema: `{"anyOf":[{"type":"string","minLength":3},{"type":"integer"}],"unevaluatedItems":false}`,
			Cases: []errorPathCase{
				{Name: "rejects", Doc: `"ab"`, Want: `value matches no anyOf branch`,
					Reason: "control: nothing joins a root message, and it was already right"},
				{Name: "accepts", Doc: `"abc"`, Reason: "control"},
			},
		},
	}
}
