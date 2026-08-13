package schema

import (
	"encoding/json"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// TestCanonicalTextMatchesEncodingJSON is the constraint that keeps this from
// being a rewrite of every enum in the corpus.
//
// The canonical form parts company with encoding/json exactly where float64
// loses information and nowhere else, so for every number float64 holds the two
// must agree byte for byte. They did before this existed -- the comparison was
// a decode into `any` and a re-encode -- and a canonical form that formatted
// even one of them differently would move every baked const and enum member
// with it, silently, and only for documents nobody in the corpus writes.
func TestCanonicalTextMatchesEncodingJSON(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	checked := 0
	for i := 0; i < 50000; i++ {
		var f float64
		switch i % 4 {
		case 0:
			f = math.Float64frombits(r.Uint64())
		case 1:
			f = float64(r.Intn(1000000)) / float64(1+r.Intn(1000))
		case 2:
			f = r.NormFloat64() * math.Pow(10, float64(r.Intn(40)-20))
		case 3:
			f = float64(r.Int63n(1 << 62))
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		want, err := json.Marshal(f)
		if err != nil {
			continue
		}
		lit := strconv.FormatFloat(f, 'g', -1, 64)
		got, ok := Number(lit).CanonicalText()
		if !ok {
			t.Fatalf("CanonicalText refused %s, which encoding/json writes as %s", lit, want)
		}
		if got != string(want) {
			t.Fatalf("CanonicalText(%s) = %s, but encoding/json writes %s", lit, got, want)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no number was checked: this test is watching nothing")
	}
	t.Logf("checked %d float64 values", checked)
}

// TestCanonicalTextGroupsEqualNumbers holds both halves of what a canonical
// form has to be: every spelling of one number reduces to one text, and two
// different numbers never reduce to the same one.
//
// The last pair is issue #272 as reported. Those two integers are one float64
// and two numbers, so a canonical form that folded them together would let a
// const naming the first accept the second -- which is what the fold through
// float64 did.
func TestCanonicalTextGroupsEqualNumbers(t *testing.T) {
	groups := [][]string{
		{"1", "1.0", "1e0", "0.1e1", "1.00", "10e-1", "100e-2", "1E0", "+1"},
		{"1.5", "1.50", "15e-1", "0.15e1"},
		{"0", "-0", "0.0", "-0.0", "0e100", "0e-100"},
		{"100", "1e2", "1.0e2", "0.1e3"},
		{"-1", "-1.0", "-1e0"},
		{"1e308", "10e307"},
		{"5e-324"},
		{"1e-7", "10e-8"},
		{"1e-6", "0.000001"},
		{"1e20", "100000000000000000000"},
		{"1e21", "1000000000000000000000"},
		{"123456789012345678901234567890", "12345678901234567890123456789e1"},
		{"123456789012345678901234567891"},
		{"123456789012345678901234567889"},
	}
	byText := make(map[string]int, len(groups))
	for gi, g := range groups {
		var canonical string
		for i, lit := range g {
			got, ok := Number(lit).CanonicalText()
			if !ok {
				t.Fatalf("group %d: CanonicalText refused %s", gi, lit)
			}
			if i == 0 {
				canonical = got
				continue
			}
			if got != canonical {
				t.Errorf("%s and %s are the same number but canonicalise to %s and %s", g[0], lit, canonical, got)
			}
		}
		if prev, seen := byText[canonical]; seen {
			t.Errorf("groups %d and %d are different numbers and both canonicalise to %s", prev, gi, canonical)
		}
		byText[canonical] = gi
	}
}

// TestCanonicalJSONReducesDocuments covers the walk over the value rather than
// the number: member order settled, and everything else left alone.
func TestCanonicalJSONReducesDocuments(t *testing.T) {
	tests := []struct {
		name string
		docs []string
		want string
	}{
		{"member order", []string{`{"a":1,"b":2}`, `{"b":2,"a":1}`}, `{"a":1,"b":2}`},
		{"nested member order", []string{`{"k":{"y":1,"x":2}}`, `{"k":{"x":2,"y":1}}`}, `{"k":{"x":2,"y":1}}`},
		{"element order is not", []string{`[1,2]`}, `[1,2]`},
		{"numbers inside containers", []string{`{"a":[1.50,1e2]}`}, `{"a":[1.5,100]}`},
		{"null", []string{`null`}, `null`},
		{"booleans", []string{`[true,false]`}, `[true,false]`},
		// json.Marshal escapes <, > and & for HTML safety, and using it means
		// the canonical text carries that escaping. It costs nothing -- both
		// sides of every comparison go through the same encoder -- and taking
		// it out would mean writing a second string encoder to disagree with.
		{"strings carry encoding/json's escaping", []string{`"a<b"`}, `"a\u003cb"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, doc := range tc.docs {
				var v any
				dec := json.NewDecoder(strings.NewReader(doc))
				dec.UseNumber()
				if err := dec.Decode(&v); err != nil {
					t.Fatalf("decoding %s: %v", doc, err)
				}
				got, ok := CanonicalJSON(v)
				if !ok {
					t.Fatalf("CanonicalJSON refused %s", doc)
				}
				if got != tc.want {
					t.Errorf("CanonicalJSON(%s) = %s, want %s", doc, got, tc.want)
				}
			}
		})
	}
}

// TestCanonicalJSONReadsTheKindsAValueCanArriveAs covers the other input to
// CanonicalJSON: a Schema assembled in Go rather than parsed, whose numbers are
// Go numeric kinds and never json.Number. A member such a caller wrote must not
// be read as "not a value" and deduplicated away against another one.
func TestCanonicalJSONReadsTheKindsAValueCanArriveAs(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		{"a float64", 1.5, "1.5"},
		{"a whole float64", 5.0, "5"},
		{"a float32", float32(2.5), "2.5"},
		{"an int", 7, "7"},
		{"an int64", int64(-9223372036854775808), "-9223372036854775808"},
		{"a uint64", uint64(18446744073709551615), "18446744073709551615"},
		{"a Number", Number("1e2"), "100"},
		{"a json.Number", json.Number("1e2"), "100"},
		{"a mixture inside a container", []any{1, 2.5, "x"}, `[1,2.5,"x"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CanonicalJSON(tc.in)
			if !ok {
				t.Fatalf("CanonicalJSON(%#v) could not read the value", tc.in)
			}
			if got != tc.want {
				t.Errorf("CanonicalJSON(%#v) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}

	// And the refusal, which is what a caller falls back from. A Go value no
	// JSON decode produces has no canonical text, and answering one anyway is
	// how two members that are not equal would be deduplicated into one.
	for _, in := range []any{
		struct{ A int }{1},
		map[int]string{1: "a"},
		[]any{struct{}{}},
		Number("not a number"),
		Number("1e99999999999999"),
	} {
		if got, ok := CanonicalJSON(in); ok {
			t.Errorf("CanonicalJSON(%#v) answered %s; nothing a JSON decode produces looks like this", in, got)
		}
	}
}

// TestDedupeEnumKeepsOneOfEachValue pins the normalization pass that closed the
// duplicate-case half of issue #269, including that it is JSON equality and not
// text equality that decides.
func TestDedupeEnumKeepsOneOfEachValue(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want int
	}{
		{"a repeated string", `{"enum":["a","a","a"]}`, 1},
		{"three spellings of one number", `{"enum":[1,1.0,1e0]}`, 1},
		{"two objects written in different member order", `{"enum":[{"a":1,"b":2},{"b":2,"a":1}]}`, 1},
		{"distinct members are kept", `{"enum":["a","b","a"]}`, 2},
		{"a big integer and its float64 neighbour are two members", `{"enum":[123456789012345678901234567890,123456789012345678901234567891]}`, 2},
		{"nulls", `{"enum":[null,null]}`, 1},
		{"arrays differing only in order", `{"enum":[[1,2],[2,1]]}`, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s Schema
			if err := json.Unmarshal([]byte(tc.doc), &s); err != nil {
				t.Fatalf("parsing %s: %v", tc.doc, err)
			}
			s.Normalize()
			if len(s.Enum) != tc.want {
				t.Errorf("%s left %d members (%v), want %d", tc.doc, len(s.Enum), s.Enum, tc.want)
			}
		})
	}

	// Order, and which spelling survives: the constant an enum member is named
	// by is derived from the member, so a pass that kept the last duplicate
	// rather than the first would rename it.
	var s Schema
	if err := json.Unmarshal([]byte(`{"enum":["b",1.50,"b",1.5]}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	got, ok := CanonicalJSON(s.Enum)
	if !ok {
		t.Fatal("the deduplicated enum could not be canonicalised")
	}
	if want := `["b",1.5]`; got != want {
		t.Errorf("deduplicated enum is %s, want %s", got, want)
	}
}
