package schema

import (
	"encoding/json"
	"testing"
)

// TestNumberKeepsTheLiteral pins the property the type exists for: a numeric
// keyword is the number the schema wrote, not the float64 nearest to it.
func TestNumberKeepsTheLiteral(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want Number
	}{
		{"MaxInt64", `{"minimum":9223372036854775807}`, "9223372036854775807"},
		{"2^63", `{"minimum":9223372036854775808}`, "9223372036854775808"},
		{"2^53+1", `{"maximum":9007199254740993}`, "9007199254740993"},
		{"an exponent", `{"multipleOf":1e30}`, "1e30"},
		{"a fraction", `{"minimum":1.5}`, "1.5"},
		{"negative zero", `{"minimum":-0.0}`, "-0.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s Schema
			if err := json.Unmarshal([]byte(tc.doc), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := s.Minimum
			if got == nil {
				got = s.Maximum
			}
			if got == nil {
				got = s.MultipleOf
			}
			if got == nil {
				t.Fatalf("no numeric keyword was read from %s", tc.doc)
			}
			if *got != tc.want {
				t.Errorf("kept %q, want %q -- the literal went through float64", *got, tc.want)
			}
		})
	}
}

// TestNumberInt64IsExact is the question every int64 position asks. float64
// answers it wrongly in both directions at the top of the range, which is what
// makes an exact reading load-bearing rather than tidy.
func TestNumberInt64IsExact(t *testing.T) {
	tests := []struct {
		lit  Number
		want int64
		ok   bool
	}{
		{"9223372036854775807", 9223372036854775807, true},
		{"9223372036854775806", 9223372036854775806, true},
		{"9223372036854775808", 0, false},
		{"9223372036854775809", 0, false},
		{"-9223372036854775808", -9223372036854775808, true},
		{"-9223372036854775809", 0, false},
		{"9007199254740993", 9007199254740993, true},
		{"9223372036854775807.0", 9223372036854775807, true},
		{"1e2", 100, true},
		{"100.00", 100, true},
		{"1.5", 0, false},
		{"-0.0", 0, true},
		{"1e400", 0, false},
	}
	for _, tc := range tests {
		got, ok := tc.lit.Int64()
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("Number(%q).Int64() = %d, %v; want %d, %v", tc.lit, got, ok, tc.want, tc.ok)
		}
	}
}

// TestNumberRefusesWhatFloat64DidToo keeps the type's contract the same as the
// *float64 field it replaced: it holds any number float64's range covers,
// exactly, and refuses everything that field refused.
func TestNumberRefusesWhatFloat64DidToo(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"a JSON string", `{"minimum":"5"}`},
		{"a magnitude float64 has no room for", `{"minimum":1e400}`},
		{"an object", `{"minimum":{}}`},
		{"an array", `{"minimum":[1]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s Schema
			if err := json.Unmarshal([]byte(tc.doc), &s); err == nil {
				t.Errorf("%s was accepted as a minimum of %v; the float64 field it replaced refused it", tc.name, s.Minimum)
			}
		})
	}
}

// TestConstAndEnumKeepTheirLiterals covers the other half: the keywords typed
// `any`, whose numbers the schema decoder keeps as json.Number so that the
// generator can write them into Go source as the constants they are.
func TestConstAndEnumKeepTheirLiterals(t *testing.T) {
	var s Schema
	doc := `{"const":9223372036854775807,"enum":[9007199254740993,1.5,"s",null,{"a":9223372036854775807}]}`
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Const == nil {
		t.Fatalf("const was not read")
	}
	n, ok := (*s.Const).(json.Number)
	if !ok {
		t.Fatalf("const is %T, want json.Number: a float64 has already lost the value by this point", *s.Const)
	}
	if n.String() != "9223372036854775807" {
		t.Errorf("const = %s, want 9223372036854775807", n)
	}
	if got, ok := s.Enum[0].(json.Number); !ok || got.String() != "9007199254740993" {
		t.Errorf("enum[0] = %#v, want json.Number 9007199254740993", s.Enum[0])
	}
	if got, ok := s.Enum[1].(json.Number); !ok || got.String() != "1.5" {
		t.Errorf("enum[1] = %#v, want json.Number 1.5 -- a fraction is a number too", s.Enum[1])
	}
	if _, ok := s.Enum[2].(string); !ok {
		t.Errorf("enum[2] = %#v, want a string: UseNumber must not touch anything but numbers", s.Enum[2])
	}
	if s.Enum[3] != nil {
		t.Errorf("enum[3] = %#v, want nil", s.Enum[3])
	}
	obj, ok := s.Enum[4].(map[string]any)
	if !ok {
		t.Fatalf("enum[4] = %#v, want an object", s.Enum[4])
	}
	if got, ok := obj["a"].(json.Number); !ok || got.String() != "9223372036854775807" {
		t.Errorf("enum[4].a = %#v, want json.Number 9223372036854775807: a nested number is a number", obj["a"])
	}
}

// TestNumberRoundTripsThroughMarshal keeps a schema that is read and written
// again carrying the number it arrived with. The resolver re-marshals schemas,
// and a bound that changed on the way through would change what is enforced.
func TestNumberRoundTripsThroughMarshal(t *testing.T) {
	doc := `{"maximum":9223372036854775807,"minimum":1e30,"multipleOf":1.5}`
	var s Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again Schema
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal of %s: %v", out, err)
	}
	if *again.Maximum != "9223372036854775807" || *again.Minimum != "1e30" || *again.MultipleOf != "1.5" {
		t.Errorf("round trip gave %s", out)
	}
}
