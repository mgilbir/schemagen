package schemagen

import (
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// A document keyed "…/a#" also answers to "…/a", its trimmed spelling, and a
// run can hold another document keyed "…/a" exactly. The exact key has to win,
// every time: the map both come from is ranged in a random order, and when the
// alias was written in the same pass the document behind "…/a" was whichever of
// the two the order happened to write last.
func TestTheExactIDWinsOverATrimmedAlias(t *testing.T) {
	exact := &schema.Schema{ID: "https://determinism.example/a"}
	hashed := &schema.Schema{ID: "https://determinism.example/a##"}
	docs := map[string]*schema.Schema{
		"https://determinism.example/a":  exact,
		"https://determinism.example/a#": hashed,
	}
	// A two-member map repeats its order often, so the question is asked many
	// times; each asks with a fresh iteration.
	for i := 0; i < 200; i++ {
		r := newCanonicalInstanceResolver(nil, docs)
		if got := r.byID["https://determinism.example/a"]; got != exact {
			t.Fatalf("attempt %d: \"…/a\" resolved to the document keyed \"…/a#\", not the one keyed \"…/a\"", i+1)
		}
		if got := r.byID["https://determinism.example/a#"]; got != hashed {
			t.Fatalf("attempt %d: \"…/a#\" lost its own document", i+1)
		}
	}
}
