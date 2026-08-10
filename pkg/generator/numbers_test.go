package generator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// TestGoNumberLiteral pins what reaches the generated source. Every row here is
// a constant the Go compiler will check against the type it is declared with,
// so a rounding at this point is either a build failure or a silently different
// constant -- issue #216 was both, on either side of one threshold.
func TestGoNumberLiteral(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"MaxInt64 as a schema number", schema.Number("9223372036854775807"), "9223372036854775807"},
		{"MaxInt64 as a decoded const", json.Number("9223372036854775807"), "9223372036854775807"},
		{"2^63", json.Number("9223372036854775808"), "9223372036854775808"},
		{"2^53+1", json.Number("9007199254740993"), "9007199254740993"},
		{"an integer written with an exponent", json.Number("1e2"), "100"},
		{"an integer written with a point", json.Number("100.0"), "100"},
		{"a fraction is left as written", json.Number("1.5"), "1.5"},
		{"a magnitude past float64 is written out", json.Number("1e400"), "1" + strings.Repeat("0", 400)},
		{"an exponent past what Rat will build is left as written", json.Number("1e999999"), "1e999999"},
		{"a Go-built float that is whole", 5.0, "5"},
		{"a Go-built int", 7, "7"},
		{"MinInt64 built in Go as a float", -float64(1 << 63), "-9223372036854775808"},
		{"not a number", "x", ""},
		{"a bool", true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := GoNumberLiteral(tc.in); got != tc.want {
				t.Errorf("GoNumberLiteral(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIntegerComparable covers the decision that turns a numeric check from a
// float64 comparison into an int64 one. Answering yes where the instance is not
// an int64 emits source that does not build; answering no where it is leaves
// the defect the flag exists to close.
func TestIntegerComparable(t *testing.T) {
	tests := []struct {
		name  string
		types schema.TypeList
		bound schema.Number
		want  bool
	}{
		{"an integer with an integer bound", schema.TypeList{"integer"}, "5", true},
		{"an integer with a bound at the top of the range", schema.TypeList{"integer"}, "9223372036854775807", true},
		{"an integer with a bound past the range", schema.TypeList{"integer"}, "9223372036854775808", false},
		{"an integer with a fractional bound", schema.TypeList{"integer"}, "1.5", false},
		{"an integer that may also be null", schema.TypeList{"integer", "null"}, "5", true},
		{"a number", schema.TypeList{"number"}, "5", false},
		{"an integer or a string", schema.TypeList{"integer", "string"}, "5", false},
		{"no type at all", nil, "5", false},
		{"an integer bound written with an exponent", schema.TypeList{"integer"}, "1e2", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &schema.Schema{Type: tc.types}
			if got := integerComparable(s, tc.bound); got != tc.want {
				t.Errorf("integerComparable(%v, %q) = %v, want %v", tc.types, tc.bound, got, tc.want)
			}
		})
	}
	if integerComparable(nil, schema.Number("5")) {
		t.Errorf("a nil schema answered yes")
	}
}

// TestCombineMultipleOfIsExact covers the allOf merge. Two divisors are combined
// into their least common multiple, and taking the lcm of two roundings gives a
// divisor neither schema wrote.
func TestCombineMultipleOfIsExact(t *testing.T) {
	num := func(s string) *schema.Number { n := schema.Number(s); return &n }
	tests := []struct {
		name string
		a, b *schema.Number
		want string
	}{
		{"one side absent", nil, num("3"), "3"},
		{"the other absent", num("3"), nil, "3"},
		{"coprime integers", num("3"), num("5"), "15"},
		{"one a multiple of the other", num("4"), num("2"), "4"},
		{"large exact integers", num("4611686018427387904"), num("2"), "4611686018427387904"},
		{"an integer written with an exponent", num("1e2"), num("5"), "100"},
		{"fractions keep the float64 reading", num("0.5"), num("0.25"), "0.5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := combineMultipleOf(tc.a, tc.b)
			if got == nil || string(*got) != tc.want {
				t.Errorf("combineMultipleOf = %v, want %q", got, tc.want)
			}
		})
	}
}

// TestTighterBoundsAreExact covers the other half of the allOf merge. Two bounds
// one apart at the top of the int64 range are one float64, and picking either
// as "the tighter" would be a coin toss.
func TestTighterBoundsAreExact(t *testing.T) {
	num := func(s string) *schema.Number { n := schema.Number(s); return &n }
	if got := tighterLowerFloat(num("9223372036854775806"), num("9223372036854775807")); string(*got) != "9223372036854775807" {
		t.Errorf("tighterLowerFloat picked %q, want the larger lower bound", *got)
	}
	if got := tighterLowerFloat(num("9223372036854775807"), num("9223372036854775806")); string(*got) != "9223372036854775807" {
		t.Errorf("tighterLowerFloat picked %q, want the larger lower bound", *got)
	}
	if got := tighterUpperFloat(num("9223372036854775807"), num("9223372036854775806")); string(*got) != "9223372036854775806" {
		t.Errorf("tighterUpperFloat picked %q, want the smaller upper bound", *got)
	}
	if got := tighterUpperFloat(num("9223372036854775806"), num("9223372036854775807")); string(*got) != "9223372036854775806" {
		t.Errorf("tighterUpperFloat picked %q, want the smaller upper bound", *got)
	}
	if got := tighterLowerFloat(nil, num("1")); string(*got) != "1" {
		t.Errorf("an absent bound did not yield to the one that is there")
	}
}

// TestConstJSONValueMatchesTheRuntimeEncoding pins the one place a number is
// deliberately read through float64: the encoded form the generated check
// compares an instance against. The instance is decoded into `any` and marshaled
// back there, so both sides have to be written the way encoding/json writes a
// float64 -- otherwise an enum of 1.0 stops matching a document of 1.
func TestConstJSONValueMatchesTheRuntimeEncoding(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"a whole number written with a point", json.Number("1.0"), "1"},
		{"a whole number written with an exponent", json.Number("1e2"), "100"},
		{"a fraction", json.Number("1.5"), "1.5"},
		{"a string is untouched", "a", `"a"`},
		{"an array", []any{json.Number("1.0"), "b"}, `[1,"b"]`},
		{"an object", map[string]any{"k": json.Number("2.0")}, `{"k":2}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := constJSONValue(tc.in)
			if err != nil {
				t.Fatalf("constJSONValue(%#v): %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("constJSONValue(%#v) = %s, want %s", tc.in, got, tc.want)
			}
			// The other side of the comparison, produced the way the generated
			// code produces it.
			var decoded any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatalf("re-decoding %s: %v", got, err)
			}
			again, err := json.Marshal(decoded)
			if err != nil {
				t.Fatalf("re-encoding: %v", err)
			}
			if string(again) != string(got) {
				t.Errorf("the encoding is not a fixed point: %s became %s, so a generated check would compare unequal forms", got, again)
			}
		})
	}
}
