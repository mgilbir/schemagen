package tests

import "testing"

// The behavioural half of the *value-bucket* half of the ref-with-siblings cycle
// guard. Its sibling file is ref_sibling_item_position_test.go, which covers the
// element and tuple-slot positions #348 fixed; the seeds both replay through
// FuzzGenerate and compile through TestGeneratedCorpusCompiles live beside each
// other under testdata/schemas/adversarial/cycle/.
//
// A patternProperties bucket and a per-branch overflow check have no Go field to
// hang a sub-schema's rules on -- the keys are not known until a document
// arrives -- so the value sits in a raw-JSON bucket and is decoded into a type
// minted for the position. That name is the owning struct's plus the bucket's
// index, so it grows a segment per level of a cycle and generateTypeDef's
// name-keyed guard can never fire. {"patternProperties":{"^x":{"$ref":"#",
// "minLength":1}}} recursed until the run ended in "fatal error: out of memory",
// which no recover intercepts (#349). It is #348's failure at a third position,
// and the fix is the same one made total: every arm that mints a
// position-derived name asks materializeAtPosition, and the enumeration of which
// arms those are is pinned in pkg/generator/positiontypenames_test.go.
//
// Terminating is only half of it, which is why these documents exist rather than
// only the seeds. Answering a cycle with nothing, or with a type carrying no
// check, also terminates and also compiles -- and silently stops enforcing the
// sibling at every level below the first. Only a document that must be
// *rejected* tells those apart, and every rejection below is a rejection the
// sibling keyword alone produces. The two-levels-down cases are there because
// the one-level ones still pass under a guard that delegates to nothing: that is
// what #348's first plant showed.
//
// The verdicts were taken from python-jsonschema and js-ajv through bowtie
// rather than derived by reading the specification, because two of them are not
// obvious: the branch-additionalProperties document rejects a *declared*
// property, since the allOf branch declares none of its own and everything is
// therefore additional to it; and the branch-unevaluatedProperties document
// rejects the same one, because annotations do not flow down into an allOf
// branch from its parent's properties.
func selfReferentialValueBucketFixtures() []notFixture {
	return []notFixture{
		{
			// {"patternProperties":{"^x":{"$ref":"#","minLength":1}}}: every
			// property whose name starts with x satisfies this same schema, and
			// is additionally a string of at least one character if it is a
			// string at all.
			Name:       "patternprops_self_ref_sibling",
			SchemaPath: "testdata/schemas/adversarial/cycle/patternprops-self-ref-sibling.json",
			Instances: []notInstance{
				{Name: "an empty object", Doc: `{}`, Valid: true,
					Why: "no key matches ^x, so the bucket says nothing"},
				{Name: "a non-object", Doc: `"x"`, Valid: true,
					Why: "control: patternProperties says nothing about a value that is not an object"},
				{Name: "a key outside the pattern", Doc: `{"y":""}`, Valid: true,
					Why: "control: the empty string is only judged where the pattern matches the key"},
				{Name: "a matching key holding a non-empty string", Doc: `{"x":"a"}`, Valid: true,
					Why: "control for the case below: the same shape one character longer satisfies minLength"},
				{Name: "a matching key holding the empty string", Doc: `{"x":""}`, Valid: false,
					Why: "the sibling minLength is what rejects it; a guard that dropped the sibling would accept this"},
				{Name: "a matching key holding a number", Doc: `{"x":1}`, Valid: true,
					Why: "control: minLength is vacuous on a number, and the reference is vacuous on it too"},
				{Name: "the empty string one level down", Doc: `{"x":{"x":""}}`, Valid: false,
					Why: "the reference pulls the same bucket into the value, so the sibling binds there as well"},
				{Name: "a non-empty string one level down", Doc: `{"x":{"x":"a"}}`, Valid: true,
					Why: "control for the above"},
				{Name: "the empty string two levels down", Doc: `{"x":{"x":{"x":""}}}`, Valid: false,
					Why: "the cycle has to keep enforcing past the level the guard fires at, which is where an answer of \"no check here\" still passes the case above"},
				{Name: "a non-empty string two levels down", Doc: `{"x":{"x":{"x":"a"}}}`, Valid: true,
					Why: "control for the above"},
			},
		},
		{
			// The same statement with the cycle running through a $defs entry
			// rather than through the document root, which is the other route
			// into the bucket.
			Name:       "defs_patternprops_self_ref_sibling",
			SchemaPath: "testdata/schemas/adversarial/cycle/defs-patternprops-self-ref-sibling.json",
			Instances: []notInstance{
				{Name: "an empty object", Doc: `{}`, Valid: true, Why: "no key matches ^x"},
				{Name: "a matching key holding a non-empty string", Doc: `{"x":"a"}`, Valid: true, Why: "control"},
				{Name: "a matching key holding the empty string", Doc: `{"x":""}`, Valid: false,
					Why: "the sibling minLength, reached through the definition rather than the root"},
				{Name: "the empty string one level down", Doc: `{"x":{"x":""}}`, Valid: false,
					Why: "the definition refers to itself, so the bucket recurs"},
				{Name: "the empty string two levels down", Doc: `{"x":{"x":{"x":""}}}`, Valid: false,
					Why: "past the level the guard fires at"},
				{Name: "a non-empty string two levels down", Doc: `{"x":{"x":{"x":"a"}}}`, Valid: true,
					Why: "control for the above"},
			},
		},
		{
			// {"properties":{"p":{"type":"string"}},"additionalProperties":
			// {"$ref":"#","minLength":1}}: the overflow position, which reaches
			// the same arm by the other call site.
			Name:       "addprops_beside_props_self_ref_sibling",
			SchemaPath: "testdata/schemas/adversarial/cycle/addprops-beside-props-self-ref-sibling.json",
			Instances: []notInstance{
				{Name: "an empty object", Doc: `{}`, Valid: true, Why: "nothing overflows"},
				{Name: "the declared property", Doc: `{"p":"a"}`, Valid: true, Why: "control: p is declared, so it is not additional"},
				{Name: "the declared property holding the empty string", Doc: `{"p":""}`, Valid: true,
					Why: "p is declared and its own schema is only \"string\"; additionalProperties does not reach it. This separates the document from the allOf-branch one below, which does reject it"},
				{Name: "an undeclared key holding the empty string", Doc: `{"q":""}`, Valid: false,
					Why: "q is additional, so the sibling minLength binds"},
				{Name: "an undeclared key holding a non-empty string", Doc: `{"q":"a"}`, Valid: true, Why: "control for the above"},
				{Name: "an undeclared key holding a number", Doc: `{"q":1}`, Valid: true,
					Why: "control: minLength is vacuous on a number"},
				{Name: "the empty string one level down", Doc: `{"q":{"r":""}}`, Valid: false,
					Why: "the reference makes the overflow value this same schema, so r overflows in turn"},
				{Name: "a non-empty string one level down", Doc: `{"q":{"r":"a"}}`, Valid: true, Why: "control for the above"},
			},
		},
		{
			// The same value read off an applicator branch, which is the third
			// call site into the arm.
			Name:       "branch_addprops_self_ref_sibling",
			SchemaPath: "testdata/schemas/adversarial/cycle/branch-addprops-self-ref-sibling.json",
			Instances: []notInstance{
				{Name: "an empty object", Doc: `{}`, Valid: true, Why: "nothing overflows"},
				{Name: "the declared property", Doc: `{"p":"a"}`, Valid: true,
					Why: "control: p satisfies both its own \"string\" and the branch's minLength"},
				{Name: "the declared property holding the empty string", Doc: `{"p":""}`, Valid: false,
					Why: "the allOf branch declares no properties of its own, so p is additional *to the branch* and the sibling minLength binds. Confirmed against python-jsonschema and js-ajv"},
				{Name: "an undeclared key holding the empty string", Doc: `{"q":""}`, Valid: false,
					Why: "additional to the branch as well"},
				{Name: "an undeclared key holding a non-empty string", Doc: `{"q":"a"}`, Valid: true, Why: "control for the above"},
				{Name: "the empty string one level down", Doc: `{"q":{"r":""}}`, Valid: false,
					Why: "past the level the guard fires at: the branch's value is this same schema"},
				{Name: "a non-empty string one level down", Doc: `{"q":{"r":"a"}}`, Valid: true, Why: "control for the above"},
			},
		},
		{
			// unevaluatedProperties read off the same branch, which is the
			// second builder that reaches the arm.
			Name:       "branch_unevalprops_self_ref_sibling",
			SchemaPath: "testdata/schemas/adversarial/cycle/branch-unevalprops-self-ref-sibling.json",
			Instances: []notInstance{
				{Name: "an empty object", Doc: `{}`, Valid: true, Why: "nothing is unevaluated"},
				{Name: "the declared property", Doc: `{"p":"a"}`, Valid: true, Why: "control"},
				{Name: "the declared property holding the empty string", Doc: `{"p":""}`, Valid: false,
					Why: "annotations do not flow down into an allOf branch from its parent's properties, so p is unevaluated inside the branch and the sibling minLength binds. Confirmed against python-jsonschema and js-ajv"},
				{Name: "an undeclared key holding the empty string", Doc: `{"q":""}`, Valid: false,
					Why: "unevaluated by anything"},
				{Name: "an undeclared key holding a non-empty string", Doc: `{"q":"a"}`, Valid: true, Why: "control for the above"},
				{Name: "the empty string one level down", Doc: `{"q":{"r":""}}`, Valid: false,
					Why: "past the level the guard fires at"},
				{Name: "a non-empty string one level down", Doc: `{"q":{"r":"a"}}`, Valid: true, Why: "control for the above"},
			},
		},
		{
			// The general shape, of which the case variant below is one
			// instance: *any* unknown keyword written beside a self-reference
			// makes it a reference carrying a structural sibling, which is what
			// disqualifies both ref-only arms and sends the node to the merge
			// that has to name the position. There are infinitely many such
			// keywords, so the class cannot be closed by listing them -- only by
			// guarding the arm. hasRefStructuralSiblings counts them because
			// statedConstraints reads Extensions, and a known annotation keyword
			// in the same place ($comment, title, examples) does not have the
			// effect: those are classified and do not make the reference a
			// merge. This document killed the pre-fix generator at the same
			// three cfgBits as the ones above.
			Name:       "patternprops_unknown_keyword_sibling",
			SchemaPath: "testdata/schemas/adversarial/cycle/patternprops-unknown-keyword-sibling.json",
			Instances: []notInstance{
				{Name: "an empty object", Doc: `{}`, Valid: true,
					Why: "an unknown keyword asserts nothing, and neither does the reference it stands beside"},
				{Name: "a matching key", Doc: `{"x":""}`, Valid: true, Why: "nothing on the cycle rejects"},
				{Name: "a matching key two levels down", Doc: `{"x":{"x":{"x":1}}}`, Valid: true,
					Why: "the recursion is real -- the type decodes into itself -- and terminates, which is what is under test"},
				{Name: "a non-object", Doc: `"s"`, Valid: true, Why: "control"},
			},
		},
		{
			// This was the shortest spelling of the defect when the issue was
			// filed -- thirty-nine bytes, with no sibling written out at all --
			// and it is not one any more. "$rEf" reached this arm because
			// encoding/json's case-insensitive field matching decoded it into
			// $ref *as well as* into Extensions, so one key made the node a
			// reference and gave it a structural sibling at the same time. A
			// keyword spelled in another casing is now an unrecognised keyword
			// and nothing else (#350), so the bucket holds no reference and the
			// document does not reach the arm at all.
			//
			// It is kept because it is the document the issue was filed with, and
			// because the verdict it must be given did not change with the
			// parse: every instance is accepted, which is what a bucket whose
			// sub-schema states one unrecognised keyword says under either
			// reading. js-ajv agrees; python-jsonschema errors on the document
			// rather than judging it. The #349 arm itself is held by the two
			// fixtures above, which spell their sibling out.
			Name:       "patternprops_ref_keyword_case_variant",
			SchemaPath: "testdata/schemas/adversarial/cycle/patternprops-ref-keyword-case-variant.json",
			Instances: []notInstance{
				{Name: "an empty object", Doc: `{}`, Valid: true, Why: "the schema constrains nothing"},
				{Name: "a matching key", Doc: `{"x":""}`, Valid: true, Why: "the bucket's sub-schema asserts nothing"},
				{Name: "a matching key one level down", Doc: `{"x":{"x":""}}`, Valid: true,
					Why: "nothing on either reading of the document rejects at any depth"},
				{Name: "a non-object", Doc: `"s"`, Valid: true, Why: "control"},
			},
		},
	}
}

func TestSelfReferentialValueBucketSiblingsAreEnforcedAtEveryLevel(t *testing.T) {
	runInstanceFixtures(t, "ref_sibling_value_position_test", selfReferentialValueBucketFixtures())
}
