package runtime

import (
	"encoding/json"
	"testing"
)

// TestADecodeRefusalIsWordedAsTheGeneratedHelpersWordedIt pins the wording of
// the refusals a destination type produces. The decoder used to be generated
// into every package and now is one shared copy, and its refusals are what a
// caller sees: they are held to what they were, quirks included.
//
// The quirk is the container of a shadow. A token that is not an array,
// decoded into a []Number, is worded for the element -- "expected number" -- as
// the helpers this replaced worded it, because they told the shadows by the last
// element of the destination's printed name and []jsonNumber ends in
// jsonNumber. A caller reading that is being told about the element and not the
// token. Correcting it is a change of behaviour and belongs in a change of its
// own; the case below fails when it is made, so that it is made on purpose.
func TestADecodeRefusalIsWordedAsTheGeneratedHelpersWordedIt(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		dst  any
		want string
	}{
		{"a string into a shadow integer", `"x"`, new(Integer), "expected integer, got string"},
		{"a string into a shadow number", `"x"`, new(Number), "expected number, got string"},
		{"an object into a json.Number", `{}`, new(json.Number), "expected number, got object"},
		{"a number into a slice of ints", `1`, new([]int), "expected array, got number"},
		{"a number into a slice of a shadow", `1`, new([]Number), "expected number, got number"},
		{"a number into a map of a shadow", `1`, new(map[string]Integer), "expected integer, got number"},
		{"a number into a slice of slices of a shadow", `1`, new([][]Number), "expected number, got number"},
		{"a number into a slice of json.Number", `1`, new([]json.Number), "expected array, got number"},
	}
	for _, c := range cases {
		err := jsonDecodeRefusal(json.Unmarshal([]byte(c.doc), c.dst))
		if err == nil {
			t.Errorf("%s: %s was accepted", c.name, c.doc)
			continue
		}
		if got := err.Error(); got != c.want {
			t.Errorf("%s: worded %q, want %q", c.name, got, c.want)
		}
	}
}
