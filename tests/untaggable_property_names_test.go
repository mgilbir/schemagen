package tests

import (
	"encoding/json"
	"fmt"
	"testing"
)

// untaggableFixtureNames are the property names in
// testdata/schemas/regression/untaggable_property_names.json, in the order the
// fixture declares them. Every one of them appears at six positions there --
// required struct field, optional struct field, a nested object, a map value,
// an array element's object, and (for the ones that collide) a bag whose names
// differ only in ways a struct tag cannot express.
//
// The first eleven were already carried correctly when this test was written and
// are here as controls: a regression in any of them would be worse than the two
// defects this fixture is named for, because those names are ordinary. The last
// five are the ones needsManualJSON used to get wrong.
var untaggableFixtureNames = []string{
	"ok",          // control: an ordinary name
	"a`b",         // control: closes the raw string literal the tag is written in
	"a\\b",        // control: reflect.StructTag reads it as an escape
	"a\tb",        // control: a control character
	"a\nb",        // control: reflect.StructTag fails to unquote it
	"a\rb",        // control: the Go scanner drops it from a raw string literal
	"a b",         // control: a space, which is legal in a tag name
	"a\"b",        // control: ends the tag value
	"-",           // #3 of the four: a bare `json:"-"` means "skip this field"
	"omitempty",   // control: an option word, but in the name position
	"1a",          // control: not a legal Go identifier
	"café",        // control: non-ASCII, and a letter, so the tag can carry it
	"🎉",           // #4: non-ASCII and *not* a letter, so isValidTag refuses it
	"",            // issue #246: `json:",omitempty"` falls back to the field name
	"a,b",         // issue #247: read as name "a" with an option "b"
	"x,omitempty", // issue #247: read as name "x" with the option it looks like
}

// untaggableShadowNames are the fixture's "shadowed" bag: five names that a
// struct tag flattens onto three. "" and "a,b" are both discarded by
// encoding/json, which then falls back to the Go field name -- and the Go field
// names for "" and "X" are minted from the same empty stem. "a,b" and "a" are
// the other collision: the tag `json:"a,b,omitempty"` names "a", which is
// another field's name outright, and encoding/json resolves that conflict by
// dropping *both*. Before the fix this document came back as {"X":"x","X1":""}.
var untaggableShadowNames = []string{"", "X", "a,b", "a", "-"}

// TestUntaggablePropertyNamesRoundTripAtEveryPosition is issues #246 and #247,
// and the two neighbours the same predicate was wrong about.
//
// needsManualJSON decides which property names have to be read and written by
// hand instead of through a `json:"..."` struct tag. It was a hand-written list
// of characters -- quote, backslash, backtick, the control range -- and a list
// like that is complete only up to the last bug report. It admitted:
//
//   - "" (#246), whose tag `json:",omitempty"` makes encoding/json fall back to
//     the Go field name: the key was lost, a key "X" was invented, the value was
//     corrupted to "", and the output no longer validated against its own schema;
//   - "a,b" and "x,omitempty" (#247), whose tag is read as a name plus options,
//     so the key matched nothing on decode and nothing was written on encode --
//     silent total loss that Validate passed in both directions;
//   - "-", whose tag is `json:"-"` whenever no ",omitempty" follows it, which is
//     encoding/json's spelling of "never serialize this field". A *required*
//     property named "-" therefore vanished from the output entirely;
//   - "🎉", and every other non-ASCII rune that is not a letter or a digit:
//     encoding/json's isValidTag admits letters, digits and a fixed set of
//     punctuation and silently discards anything else, falling back to the field
//     name exactly as for "".
//
// The shapes were already in the tree -- testdata/schemas/adversarial/naming
// holds an empty property name and a 🎉 one -- but the adversarial corpus is
// wired to FuzzGenerate, whose only property is that the pipeline does not
// panic. Nothing had ever asked what those schemas did to a document. That is
// what this test is: the assertion is the effect, not the tag, because a golden
// pinning `json:",omitempty"` would have recorded the defect as intended
// behaviour.
func TestUntaggablePropertyNamesRoundTripAtEveryPosition(t *testing.T) {
	runGeneratedMainProgram(t,
		"testdata/schemas/regression/untaggable_property_names.json",
		"untaggable_property_names_test",
		untaggableNamesMain(t))
}

// untaggableNamesMain builds the program body. The documents are built here
// rather than written out as literals so that the name list above is the single
// place a name is spelled: a name added to the fixture and to that list is
// exercised at every position without anyone editing a document by hand.
func untaggableNamesMain(t *testing.T) string {
	t.Helper()

	bag := func(names ...string) map[string]any {
		m := make(map[string]any, len(names))
		for i, n := range names {
			m[n] = fmt.Sprintf("v%d", i)
		}
		return m
	}
	full := bag(untaggableFixtureNames...)
	shadow := bag(untaggableShadowNames...)

	docs := []any{
		// Every name, at every position the fixture provides.
		map[string]any{
			"requiredBag":   full,
			"optionalBag":   full,
			"nested":        map[string]any{"inner": full},
			"mapValues":     map[string]any{"k": full},
			"arrayElements": []any{full, full},
			"shadowed":      shadow,
		},
		// Only what the schema demands. Every optional property has to stay
		// absent: the hand-written marshal has no omitempty to lean on, so a
		// field it writes unconditionally invents a key the document never had.
		map[string]any{"requiredBag": full},
		// Present-but-empty containers, which must not be confused with absent
		// ones in either direction.
		map[string]any{
			"requiredBag":   full,
			"optionalBag":   map[string]any{},
			"nested":        map[string]any{"inner": map[string]any{}},
			"mapValues":     map[string]any{},
			"arrayElements": []any{},
		},
	}
	// One name at a time in an otherwise empty optional bag. A document carrying
	// every name at once cannot tell "this key survived" from "some key with
	// this value survived": before the fix, "" and "a,b" both landed on a field
	// whose tag named something else, and a whole-bag document would still have
	// shown the right number of keys if two of them had swapped.
	for _, n := range untaggableFixtureNames {
		docs = append(docs, map[string]any{
			"requiredBag": full,
			"optionalBag": map[string]any{n: "solo"},
		})
	}

	encoded := make([]string, len(docs))
	for i, d := range docs {
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("encoding document %d: %v", i, err)
		}
		encoded[i] = string(raw)
	}

	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

func main() {
	docs := []string{%s}
	failed := false
	for _, in := range docs {
		var obj UntaggableNames
		if err := json.Unmarshal([]byte(in), &obj); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal %%s: %%v\n", in, err)
			failed = true
			continue
		}
		// The input is a document its own schema accepts. If it is not, the
		// fixture and the documents have drifted apart and every verdict below
		// is measuring the wrong thing.
		if err := obj.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "INPUT REJECTED\n  in:  %%s\n  err: %%v\n", in, err)
			failed = true
			continue
		}
		out, err := json.Marshal(obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal %%s: %%v\n", in, err)
			failed = true
			continue
		}
		var original, result any
		if err := json.Unmarshal([]byte(in), &original); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal original %%s: %%v\n", in, err)
			failed = true
			continue
		}
		if err := json.Unmarshal(out, &result); err != nil {
			fmt.Fprintf(os.Stderr, "output is not JSON %%s: %%v\n", string(out), err)
			failed = true
			continue
		}
		if !reflect.DeepEqual(original, result) {
			fmt.Fprintf(os.Stderr, "ROUND-TRIP MISMATCH\n  in:  %%s\n  out: %%s\n", in, string(out))
			failed = true
			continue
		}
		// And the output is a document the schema still accepts. This is the
		// headline of issue #246: the empty property name was dropped, so the
		// re-decoded output failed its own "required" list. Round-trip equality
		// already implies it, and it is asserted anyway because it is the
		// property the issue reported and the one a reader will look for.
		var again UntaggableNames
		if err := json.Unmarshal(out, &again); err != nil {
			fmt.Fprintf(os.Stderr, "re-unmarshal %%s: %%v\n", string(out), err)
			failed = true
			continue
		}
		if err := again.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "OUTPUT FAILS ITS OWN SCHEMA\n  in:  %%s\n  out: %%s\n  err: %%v\n", in, string(out), err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
`, goStringSliceElems(encoded))
}
