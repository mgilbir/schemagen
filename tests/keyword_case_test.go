package tests

import "testing"

// The behavioural half of issue #350: a key that spells a keyword in another
// casing is not that keyword, and the generated type must not enforce it.
//
// JSON Schema keywords are case-sensitive, and an unrecognised keyword is to be
// ignored. encoding/json matches struct fields the other way round -- a key that
// matches no field exactly is matched a second time case-insensitively -- so
// every keyword on schema.Schema was accepted in every casing and enforced as
// the keyword it resembles. The fix is in Schema.UnmarshalJSON; see
// exactKeywordObject. pkg/schema holds the parse-level guards, and this is what
// says the verdict a caller actually gets changed with them.
//
// Each property of the first fixture carries one facet, and each is a wrong
// verdict in its own direction:
//
//   - "a" is {"type":"string","MinLength":5}, which refused "ab" -- a constraint
//     the document does not state, enforced anyway.
//   - "b" is {"$rEf":"#/$defs/S"}, which took a type from a reference nobody
//     wrote and so refused 1.
//   - "d" is {"type":"string","minLength":1,"MinLength":9}, where the keyword is
//     stated once and its value is 1. Both spellings filled the same field, so
//     which one won was decided by their order in the document.
//   - "e" is {"TYPE":"integer"}, which states nothing and constrained the value
//     to an integer.
//
// The controls that keep this from being a fix which switches the keywords off
// are the exactly-spelled ones -- "d" must still refuse the empty string, and
// "a" must still refuse a non-string -- because those are what a fix that
// dropped the keyword rather than its case variant would break.
func keywordCaseVariantFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "keyword_case_variants",
			SchemaPath: "testdata/schemas/adversarial/misc/keyword-case-variants.json",
			Instances: []notInstance{
				{Name: "a string shorter than the unrecognised minLength", Doc: `{"a":"ab"}`, Valid: true,
					Why: `issue #350: "MinLength" is not "minLength", so the schema states no length at all`},
				{Name: "a string longer than it", Doc: `{"a":"abcdef"}`, Valid: true, Why: "control"},
				{Name: "a number where the unrecognised $ref would have made a string", Doc: `{"b":1}`, Valid: true,
					Why: `issue #350: "$rEf" resolves nothing, so the position is unconstrained`},
				{Name: "a string in that same position", Doc: `{"b":"x"}`, Valid: true, Why: "control"},
				{Name: "a string at the stated minimum length", Doc: `{"d":"a"}`, Valid: true,
					Why: "issue #350: the stated bound is 1, and the case variant beside it is not a second statement of it"},
				{Name: "a non-integer where the unrecognised type would have forbidden one", Doc: `{"e":"x"}`, Valid: true,
					Why: `issue #350: "TYPE" is not "type"`},
				{Name: "every position at once", Doc: `{"a":"ab","b":1,"d":"a","e":"x"}`, Valid: true,
					Why: "control: no facet depends on the others being absent"},
				{Name: "an empty object", Doc: `{}`, Valid: true, Why: "control"},

				{Name: "the empty string where a length is stated", Doc: `{"d":""}`, Valid: false,
					Why: `control: "minLength" is spelled exactly here and must still bind -- dropping the case variant must not drop the keyword`},
				{Name: "a number where a string type is stated", Doc: `{"a":1}`, Valid: false,
					Why: `control: "type" is spelled exactly here and must still bind`},
			},
		},
		{
			// The fourth facet, kept in a document of its own because it fails
			// earlier than the others and would otherwise hide them. The value of
			// an unrecognised keyword is not constrained by anything, so
			// {"MinLength":"not a number"} is a legal schema -- and it was refused
			// at parse time, because the string was decoded into the FlexInt field
			// the keyword resembles. Generation failed outright, so a fixture
			// carrying this property never reached its instances at all.
			Name:       "keyword_case_variant_value_shape",
			SchemaPath: "testdata/schemas/adversarial/misc/keyword-case-variant-value-shape.json",
			Instances: []notInstance{
				{Name: "a boolean in the position", Doc: `{"c":true}`, Valid: true,
					Why: "issue #350: the schema states nothing about this property, and the document it stands in is legal"},
				{Name: "a string in the position", Doc: `{"c":"x"}`, Valid: true, Why: "control"},
				{Name: "an empty object", Doc: `{}`, Valid: true, Why: "control"},
			},
		},
	}
}

func TestKeywordCaseVariantsAreNotTheKeyword(t *testing.T) {
	runInstanceFixtures(t, "issue350_keyword_case_test", keywordCaseVariantFixtures())
}
