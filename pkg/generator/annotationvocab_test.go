package generator

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestAnnotationVocabularyConstrainsNothing holds the one thing that has to stay
// true of "deprecated", "readOnly" and "writeOnly" now that they are fields on
// schema.Schema rather than entries in Extensions: they state no constraint.
//
// The move is what makes this worth a test. Every gate in this package that asks
// "does this schema state anything" is built on schemaKeywordSet, which reads
// the *re-marshaled* schema -- so a keyword with no field is invisible there and
// a keyword with one is not. These three were invisible and are now visible, and
// nothing else in the package would notice them arriving on the wrong side of
// that line.
//
// unenforcedKeywords feeds two callers that both act on the answer.
// acceptsEveryValue refuses a schema over any keyword outside
// {allOf, anyOf, oneOf, $ref}, so a wrong answer here silently changes the code
// emitted for a schema that permits everything. unenforcedAliasDef writes the
// list into the generated source as the constraints an `any` alias is dropping,
// so a wrong answer puts an annotation in a list of lost constraints and tells
// the reader something untrue.
//
// "examples" is here for the opposite reason: it is deliberately *not* a field,
// so it arrives through Extensions and inertKeywords, and this is what says the
// two routes agree.
func TestAnnotationVocabularyConstrainsNothing(t *testing.T) {
	for _, doc := range []string{
		`{"deprecated": true}`,
		`{"readOnly": true}`,
		`{"writeOnly": true}`,
		`{"deprecated": false}`,
		`{"readOnly": false, "writeOnly": false}`,
		`{"examples": [1, 2]}`,
		`{"title": "t", "description": "d", "deprecated": true, "readOnly": true, "writeOnly": true, "examples": [1]}`,
	} {
		var s schema.Schema
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			t.Fatalf("parsing %s: %v", doc, err)
		}
		if dropped := unenforcedKeywords(&s); len(dropped) > 0 {
			t.Errorf("%s: reported %v as unenforced constraints, and the annotation vocabulary constrains nothing", doc, dropped)
		}
		g := New(DefaultConfig())
		if !g.acceptsEveryValue(&s, 0, map[*schema.Schema]bool{}) {
			t.Errorf("%s: judged not to accept every value, and it constrains nothing", doc)
		}
	}

	// The control, and the half that a fix written as "ignore everything" would
	// break: a schema that does state a constraint still says so.
	var s schema.Schema
	if err := json.Unmarshal([]byte(`{"deprecated": true, "minLength": 3}`), &s); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	dropped := unenforcedKeywords(&s)
	if len(dropped) != 1 || dropped[0] != "minLength" {
		t.Errorf("got %v, want exactly [minLength]: the annotation is inert and the length keyword is not", dropped)
	}
	if g := New(DefaultConfig()); g.acceptsEveryValue(&s, 0, map[*schema.Schema]bool{}) {
		t.Errorf("a schema with minLength was judged to accept every value")
	}
}
