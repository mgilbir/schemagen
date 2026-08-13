package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/generator"
	"github.com/mgilbir/schemagen/pkg/schema"
)

// This file holds the behavioural half of issues #269 and #272. The compile
// half is TestGeneratedCorpusCompiles, and it is not enough on its own: three
// of these six schemas emitted Go that did not build, and the fix for each of
// them could have been a Validate that builds and checks the wrong thing.
// Every fixture below is generated, compiled and run against documents.

// duplicateAndHugeNumberFixtures are issue #269: three legal schemas whose
// generated Go did not compile.
//
// The two halves have different causes and the same shape of consequence.
// {"enum":["a","a","a"]} is legal -- 2020-12 says enum members SHOULD be
// unique, not MUST -- and became three Go constants of one value with a switch
// naming all three. 1e308 is an ordinary numeric keyword and became a
// three-hundred-and-nine-digit integer constant, which is 1024 bits against the
// 512 gc allows and the 256 the specification guarantees.
//
// A fix for either could compile and mean nothing, which is what the documents
// here are for: the duplicate enum must still admit "a" and refuse "b", and the
// numeric bounds must still be bounds.
func duplicateAndHugeNumberFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "enum_duplicate_values",
			SchemaPath: "testdata/schemas/adversarial/contra/enum-duplicate-values.json",
			Instances: []notInstance{
				{Name: "the repeated member", Doc: `"a"`, Valid: true,
					Why: "issue #269: the schema admits \"a\" and the fix must not narrow it to nothing"},
				{Name: "anything else", Doc: `"b"`, Valid: false,
					Why: "control: deduplicating the members must not turn the enum into a schema that admits everything"},
			},
		},
		{
			Name:       "enum_huge_numbers",
			SchemaPath: "testdata/schemas/adversarial/num/enum-huge-numbers.json",
			Instances: []notInstance{
				{Name: "the largest member", Doc: `1e308`, Valid: true,
					Why: "issue #269: this member is what became a 309-digit integer constant"},
				{Name: "the same member spelled with a point", Doc: `1.0e308`, Valid: true,
					Why: "control: an enum is decided on the number, not on how the document spelled it"},
				{Name: "the negative member", Doc: `-1e308`, Valid: true, Why: "control"},
				{Name: "the smallest denormal", Doc: `5e-324`, Valid: true,
					Why: "control: this member was already emitted in exponent notation and must not move"},
				{Name: "zero", Doc: `0`, Valid: true, Why: "control"},
				{Name: "a number no member names", Doc: `7`, Valid: false,
					Why: "control: the enum must still be an enum"},
				{Name: "past the largest member", Doc: `1.5e308`, Valid: false,
					Why: "control: a float64 larger than every member, so nothing matches"},
			},
		},
		{
			Name:       "huge_numbers",
			SchemaPath: "testdata/schemas/adversarial/num/huge-numbers.json",
			Instances: []notInstance{
				{Name: "at the maximum", Doc: `1e308`, Valid: true,
					Why: "issue #269: minimum, maximum and multipleOf all named this number and all three overflowed"},
				{Name: "at the minimum", Doc: `-1e308`, Valid: true, Why: "control"},
				{Name: "past the maximum", Doc: `1.5e308`, Valid: false,
					Why: "the bound has to still be enforced: a compiling Validate that checks nothing is not a fix"},
				{Name: "below the minimum", Doc: `-1.5e308`, Valid: false, Why: "control for the above"},
				{Name: "zero", Doc: `0`, Valid: true, Why: "control: inside the window and a multiple of everything"},
			},
		},
	}
}

// bigNumberConstFixtures are issue #272(1): a const or enum naming an integer
// no float64 holds.
//
// The generated Validate reduced the instance to comparable text by decoding it
// into `any`, which makes every JSON number a float64, and compared it against
// a list the generator had folded through the same float64. So every integer
// inside one rounding of 123456789012345678901234567890 was accepted -- the
// neighbour above it and the neighbour below it alike.
//
// Both neighbours are here, because one direction alone does not distinguish a
// fix from an off-by-one: a comparison that happened to reject ...891 while
// still accepting ...889 would pass a test naming only the first.
func bigNumberConstFixtures() []notFixture {
	const exact = `123456789012345678901234567890`
	return []notFixture{
		{
			Name:       "bignum_const",
			SchemaPath: "testdata/schemas/adversarial/num/bignum-const.json",
			Instances: []notInstance{
				{Name: "the const itself", Doc: exact, Valid: true, Why: "control: the schema admits this and only this"},
				{Name: "the same number spelled with an exponent", Doc: `12345678901234567890123456789e1`, Valid: true,
					Why: "control: const is decided on the number, so a fix must not become an equality on the literal"},
				{Name: "the neighbour above", Doc: `123456789012345678901234567891`, Valid: false,
					Why: "issue #272: accepted, because it shares a float64 with the const"},
				{Name: "the neighbour below", Doc: `123456789012345678901234567889`, Valid: false,
					Why: "issue #272, the other direction: the rounding is two-sided and so must the check be"},
				{Name: "the float64 they all shared", Doc: `1.2345678901234568e+29`, Valid: false,
					Why: "issue #272: this is the text the const had been baked as, so it was the one value certain to be accepted"},
				{Name: "an unrelated number", Doc: `1`, Valid: false, Why: "control"},
			},
		},
		{
			Name:       "bignum_enum",
			SchemaPath: "testdata/schemas/adversarial/num/bignum-enum.json",
			Instances: []notInstance{
				{Name: "the big member", Doc: exact, Valid: true, Why: "control"},
				{Name: "the neighbour above it", Doc: `123456789012345678901234567891`, Valid: false,
					Why: "issue #272, on the enum path rather than the const one"},
				{Name: "the small member", Doc: `1`, Valid: true, Why: "control"},
				{Name: "the small member with a point", Doc: `1.0`, Valid: true,
					Why: "control: from draft 6 on, 1.0 is the integer 1 and the enum admits it"},
				{Name: "the small member with an exponent", Doc: `1e0`, Valid: true, Why: "control for the above"},
				{Name: "a member of neither", Doc: `2`, Valid: false, Why: "control"},
			},
		},
	}
}

// disallowFixtures are issue #272(2): draft 3's "disallow" with a JSON null
// among its entries.
//
// A null decodes into a Schema without error and leaves it at its zero value,
// which is the empty schema -- the schema that matches everything. "not"
// everything admits nothing, so {"disallow":[null]} refused every document
// there is: {}, "x", 5, [] and null alike.
//
// The three controls are what tell the fix from switching the keyword off.
// Draft 3's own spellings must still bind, an entry list that mixes a legible
// entry with an illegible one must still enforce the legible one, and a dialect
// that does not define the keyword at all must go on ignoring it.
func disallowFixtures() []notFixture {
	return []notFixture{
		{
			Name:       "disallow_array_null",
			SchemaPath: "testdata/schemas/adversarial/nil2/disallow-array-null.json",
			Instances: []notInstance{
				{Name: "an object", Doc: `{}`, Valid: true,
					Why: "issue #272: a null entry names nothing to forbid, and no dialect reading of this document refuses an object"},
				{Name: "a string", Doc: `"x"`, Valid: true, Why: "control"},
				{Name: "a number", Doc: `5`, Valid: true, Why: "control"},
				{Name: "an array", Doc: `[]`, Valid: true, Why: "control"},
				{Name: "a null", Doc: `null`, Valid: true,
					Why: "control: not even the value that looks like the entry is forbidden by it"},
			},
		},
		{
			Name:       "disallow_draft3_entries",
			SchemaPath: "testdata/schemas/regression/disallow_draft3_entries.json",
			Instances: []notInstance{
				{Name: "a forbidden null", Doc: `null`, Valid: false,
					Why: "control: draft 3's properly spelled entries must go on binding"},
				{Name: "a forbidden string", Doc: `"x"`, Valid: false, Why: "control for the above"},
				{Name: "a permitted number", Doc: `5`, Valid: true, Why: "control: nothing forbids a number"},
				{Name: "a permitted object", Doc: `{}`, Valid: true, Why: "control"},
			},
		},
		{
			Name:       "disallow_draft3_null_entry",
			SchemaPath: "testdata/schemas/regression/disallow_draft3_null_entry.json",
			Instances: []notInstance{
				{Name: "a forbidden string", Doc: `"x"`, Valid: false,
					Why: "the legible entries of a mixed list must still bind: dropping the null must not drop the list"},
				{Name: "a forbidden integer", Doc: `5`, Valid: false,
					Why: "control: the schema-valued entry beside the null is legible and forbids integers"},
				{Name: "a permitted null", Doc: `null`, Valid: true,
					Why: "the null entry itself names nothing, so a null is not forbidden by it"},
				{Name: "a permitted object", Doc: `{}`, Valid: true, Why: "control"},
			},
		},
		{
			Name:       "disallow_outside_draft3",
			SchemaPath: "testdata/schemas/regression/disallow_outside_draft3.json",
			Instances: []notInstance{
				{Name: "the type the keyword names", Doc: `"x"`, Valid: true,
					Why: "draft 7 has no \"disallow\", so the keyword states nothing and the document is valid"},
				{Name: "anything else", Doc: `5`, Valid: true, Why: "control"},
			},
		},
	}
}

// emptyRefFixtures are issue #272(3): {"$ref": ""}.
//
// An empty Ref is how this package spells "no $ref", so the keyword vanished
// and the position it stood in became `any` -- accepting a string where the
// reference says the value must be whatever the root says.
//
// It is resolved rather than refused, and the two directions of that decision
// are both wrong to take the other way. Refusing would refuse a legal
// reference: RFC 3986 resolves the empty URI-reference against the base URI,
// which is the same target "#" names, and python-jsonschema resolves it. Going
// on ignoring it is what the issue reports. So the fixture is paired with the
// "#" spelling of the same schema, and both must answer identically.
func emptyRefFixtures() []notFixture {
	instances := []notInstance{
		{Name: "a nested object", Doc: `{"a":{}}`, Valid: true,
			Why: "the reference resolves to the root, which is an object, so an object satisfies it"},
		{Name: "nested twice more", Doc: `{"a":{"a":{"a":{}}}}`, Valid: true,
			Why: "control: the reference makes the schema recursive and every level is judged the same way"},
		{Name: "a string", Doc: `{"a":"x"}`, Valid: false,
			Why: "issue #272: accepted, because the empty $ref left the property typed any"},
		{Name: "a number", Doc: `{"a":5}`, Valid: false, Why: "control for the above"},
		{Name: "the property absent", Doc: `{}`, Valid: true,
			Why: "control: the reference says nothing about whether the property is required"},
	}
	return []notFixture{
		{
			Name:       "ref_empty_with_props",
			SchemaPath: "testdata/schemas/adversarial/malformed/ref-empty-with-props.json",
			Instances:  instances,
		},
		{
			// The same document with the reference spelled "#". It is the
			// control that says what "resolved" had to mean: the two spellings
			// are one reference, so anything the empty one answers differently
			// is the fix having invented a third reading.
			Name:       "ref_hash_with_props",
			SchemaPath: "testdata/schemas/regression/ref_hash_with_props.json",
			Instances:  instances,
		},
	}
}

func TestDuplicateAndHugeNumberSchemasValidate(t *testing.T) {
	runInstanceFixtures(t, "issue269_test", duplicateAndHugeNumberFixtures())
}

func TestBigNumberConstAndEnumAreComparedExactly(t *testing.T) {
	runInstanceFixtures(t, "issue272_bignum_test", bigNumberConstFixtures())
}

func TestDisallowEntriesBindOnlyWhereTheyAreLegible(t *testing.T) {
	runInstanceFixtures(t, "issue272_disallow_test", disallowFixtures())
}

func TestEmptyRefResolvesToTheDocumentRoot(t *testing.T) {
	runInstanceFixtures(t, "issue272_emptyref_test", emptyRefFixtures())
}

// TestUnresolvableRefStillFailsGeneration is the other side of the empty-ref
// decision: resolving "" must not have relaxed anything about a reference that
// really cannot be served.
//
// The message is asserted and not just the failure. It names the ref, which is
// the only part of it a caller can act on, and a fix that turned it into
// `cannot resolve $ref ""` -- or into a bare error -- would be a regression
// nobody would notice from an exit code. The guidance about --lenient-refs and
// the other three ways out is wrapped around this by cmd/schemagen rather than
// by the generator, so the whole message a caller sees is held one layer up, by
// TestEmptyRefResolvesWhileAnUnservableRefStillFails.
func TestUnresolvableRefStillFailsGeneration(t *testing.T) {
	s, err := schema.LoadFromFile(filepath.Join("..", "testdata", "schemas", "adversarial", "malformed", "ref-empty-with-props.json"))
	if err != nil {
		t.Fatalf("loading the empty-ref schema: %v", err)
	}
	// The same document with the reference replaced by one nothing can serve.
	s.Properties["a"].Ref = "#/$defs/nope"
	s.NormalizeForDraft(schema.DraftUnknown)

	_, err = generator.New(generator.Config{PackageName: "testpkg", OmitEmpty: true}).Generate(s)
	if err == nil {
		t.Fatal("a $ref naming a definition that does not exist generated without error")
	}
	if !strings.Contains(err.Error(), `cannot resolve $ref "#/$defs/nope"`) {
		t.Errorf("strict-ref failure does not name the ref:\n%v", err)
	}
}
