package generator

import (
	"net/url"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestConjoinedPropertyAnswersForTheContributionItStandsIn holds the three
// fields conjoinAllOfProperty copies onto the node it synthesizes.
//
// The node replaces a property's own schema in the merged property map, so
// everything that used to ask that schema a question now asks this one -- and
// the question that matters is which dialect it is read under. draftForSchema
// answers it from $schema and, failing that, from the document root the node
// belongs to; a synthesized node naming neither falls back to the *root
// document's* draft, which is the wrong answer for a property that came from a
// document with a dialect of its own.
//
// It is a unit test rather than a fixture because no document in the corpus
// distinguishes the two: reaching a cross-dialect disagreement at this exact
// position needs a property contributed by a remote resource whose draft
// differs, and every plant tried against the fixtures left them all passing.
// The choice to carry the fields is therefore recorded here, where deleting any
// of them fails, instead of being an unwatched assumption.
func TestConjoinedPropertyAnswersForTheContributionItStandsIn(t *testing.T) {
	g := New(Config{PackageName: "testpkg"})
	root := &schema.Schema{Schema: "http://json-schema.org/draft-07/schema#"}
	base, err := url.Parse("https://example.com/left.json")
	if err != nil {
		t.Fatalf("parsing base URI: %v", err)
	}
	existing := &schema.Schema{
		Type:         schema.TypeList{"string"},
		Schema:       "http://json-schema.org/draft-07/schema#",
		BaseURI:      base,
		DocumentRoot: root,
	}
	next := &schema.Schema{MaxLength: flexInt(9)}

	conjoined := g.conjoinAllOfProperty(existing, next)
	if conjoined == existing || conjoined == next {
		t.Fatalf("the conjunction must be a fresh node: schema nodes are shared across $ref targets, and writing through to either input leaks this merge into every other use of it")
	}
	if len(conjoined.AllOf) != 2 || conjoined.AllOf[0] != existing || conjoined.AllOf[1] != next {
		t.Fatalf("conjunction should be allOf[existing, next], got %#v", conjoined.AllOf)
	}
	if got := g.draftForSchema(conjoined); got != schema.Draft07 {
		t.Errorf("the conjoined node is read under draft %v, and the property it stands for is draft-07; a keyword whose meaning differs between the two would be read wrongly", got)
	}
	if conjoined.BaseURI == nil || conjoined.BaseURI.String() != base.String() {
		t.Errorf("conjoined BaseURI = %v, want %v -- a relative $ref written under this property resolves from there", conjoined.BaseURI, base)
	}
	if conjoined.DocumentRoot != root {
		t.Errorf("conjoined DocumentRoot = %p, want %p -- a JSON pointer fragment under this property resolves against it", conjoined.DocumentRoot, root)
	}
}

// TestConjoinAllOfPropertyResolvesTheBooleanSchemas covers the four shortcuts,
// which are the ends of the conjunction rather than an optimisation: `false`
// admits nothing, so the conjunction admits nothing, and `true` asserts nothing,
// so the other side is the whole answer. Wrapping either in an allOf would be
// correct too, but the boolean is the shape the rest of the generator already
// recognises as forbidding.
func TestConjoinAllOfPropertyResolvesTheBooleanSchemas(t *testing.T) {
	g := New(Config{PackageName: "testpkg"})
	yes, no := true, false
	trueSchema := &schema.Schema{BooleanSchema: &yes}
	falseSchema := &schema.Schema{BooleanSchema: &no}
	ordinary := &schema.Schema{Type: schema.TypeList{"string"}}

	for _, tc := range []struct {
		name           string
		existing, next *schema.Schema
		want           *schema.Schema
	}{
		{"false on the left admits nothing", falseSchema, ordinary, falseSchema},
		{"false on the right admits nothing", ordinary, falseSchema, falseSchema},
		{"true on the left says nothing", trueSchema, ordinary, ordinary},
		{"true on the right says nothing", ordinary, trueSchema, ordinary},
		{"nothing already there", nil, ordinary, ordinary},
		{"nothing contributed", ordinary, nil, ordinary},
		{"the same node twice", ordinary, ordinary, ordinary},
	} {
		if got := g.conjoinAllOfProperty(tc.existing, tc.next); got != tc.want {
			t.Errorf("%s: got %#v, want the %p schema", tc.name, got, tc.want)
		}
	}
}

func flexInt(n int) *schema.FlexInt {
	f := schema.FlexInt(n)
	return &f
}
