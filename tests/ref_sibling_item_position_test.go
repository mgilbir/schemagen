package tests

import "testing"

// This file is the behavioural half of the item-position half of the
// ref-with-siblings cycle guard. The other halves are the seeds under
// testdata/schemas/adversarial/cycle/*-self-ref-sibling.json, which every
// `go test ./...` replays through FuzzGenerate and compiles through
// TestGeneratedCorpusCompiles.
//
// A $ref written beside a keyword that survives it is generated as an implicit
// allOf of the reference and the siblings, and that merge is a *new* schema
// object, so the arm that materializes it has to be re-entrancy guarded on the
// node rather than on the name. Every position that names its type after the
// position it was reached from -- a property, an element, a tuple slot -- mints
// a name one segment longer at each level of a cycle, so g.generated[name]
// never fires and the arm re-enters itself forever. The property positions were
// guarded (cyclicNodeName, and the seeds beside these named ref-sibling-*); the
// element and tuple-slot positions were not, and {"items":{"$ref":"#",
// "minItems":1}} -- thirty-five bytes of legal schema -- recursed until the
// process died. Not of stack exhaustion: each level also mints a type name five
// characters longer than the last, so the names alone cost O(n^2) and the run
// ended in "fatal error: out of memory", which no recover intercepts. It took a
// nightly fuzz worker down with "fuzzing process hung or terminated
// unexpectedly while minimizing: EOF".
//
// Terminating is only half of what a fix has to do, which is why these
// documents exist rather than only the seeds. Answering the cycle with the name
// the node is already being generated under is exact -- that type's Validate is
// the whole of what the node says -- but a guard that answered with nothing, or
// with a type carrying no check, would also terminate, compile, and silently
// stop enforcing the sibling at every level below the first. Only a document
// that must be *rejected* can tell those apart, and every rejection below is
// rejected by the sibling keyword alone.
func selfReferentialItemSiblingFixtures() []notFixture {
	return []notFixture{
		{
			// {"items":{"$ref":"#","minItems":1}}: an array whose every element
			// is this same schema, and additionally a non-empty array if it is
			// an array at all.
			Name:       "items_self_ref_sibling",
			SchemaPath: "testdata/schemas/adversarial/cycle/items-self-ref-sibling.json",
			Instances: []notInstance{
				{Name: "an empty array", Doc: `[]`, Valid: true,
					Why: "the root states nothing about its own length: minItems is the element's keyword, and there are no elements"},
				{Name: "a non-array", Doc: `"x"`, Valid: true,
					Why: "control: \"items\" says nothing about a value that is not an array"},
				{Name: "an element that is not an array", Doc: `[1]`, Valid: true,
					Why: "control: both the reference and minItems are vacuous on a number"},
				{Name: "an empty array as the element", Doc: `[[]]`, Valid: false,
					Why: "the element is an array of length 0 and the sibling minItems demands 1; this is the case a guard that dropped the sibling would accept"},
				{Name: "a non-empty array as the element", Doc: `[[1]]`, Valid: true,
					Why: "control for the above: the same shape one item longer satisfies the same keyword"},
				{Name: "an empty array two levels down", Doc: `[[[]]]`, Valid: false,
					Why: "the cycle has to keep enforcing past the level the guard fires at, which is where an answer of \"no check here\" would still pass the case above"},
				{Name: "a non-empty array two levels down", Doc: `[[[1]]]`, Valid: true,
					Why: "control for the above"},
			},
		},
		{
			// The same statement written as a tuple position, which reaches the
			// arm by the other route: prefixItems rather than a uniform items.
			Name:       "prefixitems_self_ref_sibling",
			SchemaPath: "testdata/schemas/adversarial/cycle/prefixitems-self-ref-sibling.json",
			Instances: []notInstance{
				{Name: "an empty array", Doc: `[]`, Valid: true,
					Why: "prefixItems constrains position 0 only where the array has one; an empty array has no position 0"},
				{Name: "a non-array", Doc: `"x"`, Valid: true, Why: "control"},
				{Name: "a number at position 0", Doc: `[1]`, Valid: true,
					Why: "control: both the reference and minItems are vacuous on a number"},
				{Name: "an empty array at position 0", Doc: `[[]]`, Valid: false,
					Why: "the tuple slot delegates to the merged type, and minItems is what that type enforces"},
				{Name: "a non-empty array at position 0", Doc: `[[1]]`, Valid: true, Why: "control for the above"},
				{Name: "an empty array at position 0 of position 0", Doc: `[[[]]]`, Valid: false,
					Why: "the recursive delegation must still bind below the level the guard fires at"},
				{Name: "a non-empty array two levels down", Doc: `[[[1]]]`, Valid: true, Why: "control for the above"},
			},
		},
	}
}

func TestSelfReferentialItemSiblingsAreEnforcedAtEveryLevel(t *testing.T) {
	runInstanceFixtures(t, "ref_sibling_item_position_test", selfReferentialItemSiblingFixtures())
}
