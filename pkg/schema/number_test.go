package schema

import (
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// processCPU is the CPU time the process has used so far, user and system.
func processCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		panic(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

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
//
// The refusal is reported through MalformedKeywords rather than as a decode
// error, because whether it *is* a refusal depends on the node's dialect, which
// the decode does not know (see parse.go). minimum is defined in every dialect,
// so the record has to survive Normalize under each of them.
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
			for _, d := range []Draft{DraftUnknown, Draft03, Draft04, Draft06, Draft07, Draft201909, Draft202012, DraftV1} {
				var s Schema
				if err := json.Unmarshal([]byte(tc.doc), &s); err != nil {
					t.Fatalf("%s: a schema object with a malformed keyword is still a schema object: %v", tc.name, err)
				}
				s.NormalizeForDraft(d)
				bad := s.MalformedKeywords()
				if len(bad) != 1 || bad[0].Keyword != "minimum" || s.Minimum != nil {
					t.Errorf("%v: %s was accepted as a minimum of %v (malformed: %v); the float64 field it replaced refused it",
						d, tc.name, s.Minimum, bad)
				}
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

// randomNumberLiteral writes a JSON number in one of the many spellings one
// value has: leading and trailing zeros, a point or none, an exponent of either
// case and sign or none. The values are kept to a few digits and a small
// exponent so that pairs collide often -- equal values under different
// spellings are what the readers below must see through.
func randomNumberLiteral(r *rand.Rand) string {
	var b strings.Builder
	if r.Intn(3) == 0 {
		b.WriteByte('-')
	}
	intDigits := r.Intn(4)
	if intDigits == 0 {
		b.WriteByte('0')
	}
	for i := 0; i < intDigits; i++ {
		c := byte('0' + r.Intn(10))
		if i == 0 && c == '0' {
			c = '1' + byte(r.Intn(9))
		}
		b.WriteByte(c)
	}
	if r.Intn(2) == 0 {
		b.WriteByte('.')
		frac := 1 + r.Intn(4)
		for i := 0; i < frac; i++ {
			b.WriteByte("0012345678900"[r.Intn(13)])
		}
	}
	if r.Intn(2) == 0 {
		b.WriteByte("eE"[r.Intn(2)])
		switch r.Intn(3) {
		case 0:
			b.WriteByte('-')
		case 1:
			b.WriteByte('+')
		}
		fmt.Fprintf(&b, "%d", r.Intn(25))
	}
	return b.String()
}

// TestNumberValueReadersAgreeWithBigRat holds Compare, IsInteger, Decimal and
// Int64 to big.Rat, which reads the whole grammar exactly and is the reference
// these readers stand in for: they answer from the digits in linear time, and
// big.Rat's parse is quadratic in them.
func TestNumberValueReadersAgreeWithBigRat(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	lits := []string{
		"0", "-0", "0.0", "0e5", "1", "1.0", "10e-1", "0.1e1", "-1", "1.5", "15e-1",
		"9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809",
		"922337203685477580.7e1", "92233720368547758070e-1", "9223372036854775807.00",
		"100000000000000000000", "1e19", "1e18", "0.000000000000000000001",
		// Digits that fit, scaled by an exponent to the edge of int64 and past.
		"92233720368547758e2", "92233720368547759e2", "-92233720368547758e2", "-92233720368547759e2",
		"-9223372036854775808e0", "-922337203685477581e1",
	}
	for i := 0; i < 1500; i++ {
		lits = append(lits, randomNumberLiteral(r))
	}
	rats := make([]*big.Rat, len(lits))
	for i, lit := range lits {
		v, ok := new(big.Rat).SetString(lit)
		if !ok {
			t.Fatalf("the literal generator wrote %q, which is not a number", lit)
		}
		rats[i] = v
	}
	failures := 0
	fail := func(format string, args ...any) {
		failures++
		if failures <= 20 {
			t.Errorf(format, args...)
		}
	}
	for i, lit := range lits {
		n, v := Number(lit), rats[i]

		isInt, ok := n.IsInteger()
		if !ok || isInt != v.IsInt() {
			fail("Number(%q).IsInteger() = %v, %v; big.Rat says %v", lit, isInt, ok, v.IsInt())
		}

		digits, scale, neg, ok := n.Decimal()
		if !ok {
			fail("Number(%q).Decimal() refused it", lit)
		} else {
			got := new(big.Rat)
			if digits != "" {
				if strings.HasPrefix(digits, "0") || strings.HasSuffix(digits, "0") {
					fail("Number(%q).Decimal() digits %q carry a leading or trailing zero", lit, digits)
				}
				d, _ := new(big.Int).SetString(digits, 10)
				p := new(big.Int).Exp(big.NewInt(10), big.NewInt(max(scale, -scale)), nil)
				if scale >= 0 {
					got.SetInt(d.Mul(d, p))
				} else {
					got.SetFrac(d, p)
				}
				if neg {
					got.Neg(got)
				}
			}
			if got.Cmp(v) != 0 {
				fail("Number(%q).Decimal() = %s*10^%d (neg %v), which is %s; big.Rat says %s", lit, digits, scale, neg, got.RatString(), v.RatString())
			}
		}

		want, wantOK := int64(0), v.IsInt() && v.Num().IsInt64()
		if wantOK {
			want = v.Num().Int64()
		}
		if got, ok := n.Int64(); ok != wantOK || got != want {
			fail("Number(%q).Int64() = %d, %v; big.Rat says %d, %v", lit, got, ok, want, wantOK)
		}

		for j := range lits {
			c, ok := n.Compare(Number(lits[j]))
			if !ok || c != v.Cmp(rats[j]) {
				fail("Number(%q).Compare(%q) = %d, %v; big.Rat says %d", lit, lits[j], c, ok, v.Cmp(rats[j]))
			}
		}
	}
	if failures > 20 {
		t.Errorf("... and %d more", failures-20)
	}
}

// TestNumberValueReadersAreBoundedOnLongLiterals is the hostile half: a literal
// is as long as the document writes it, and these are asked of every numeric
// keyword the generator emits. A million digits through big.Rat takes
// seconds; read as digits, it takes the time to scan them.
func TestNumberValueReadersAreBoundedOnLongLiterals(t *testing.T) {
	const width = 1000000
	long := []Number{
		Number("1." + strings.Repeat("7", width)),
		Number(strings.Repeat("9", width)),
		Number("0." + strings.Repeat("0", width) + "1"),
		Number("1" + strings.Repeat("0", width) + "e-" + strconv.Itoa(width)),
	}
	// Timed by the CPU the process spends rather than the wall clock, which on
	// a machine running other test binaries counts the time spent waiting for a
	// CPU. No test in this package is parallel, so the process's time is this
	// loop's.
	before := processCPU()
	for _, n := range long {
		n.Int64()
		n.IsInteger()
		n.Decimal()
		n.Compare(n)
	}
	if elapsed := processCPU() - before; elapsed > time.Second {
		t.Errorf("reading four %d-digit literals took %v of CPU", width, elapsed)
	}
	// The last is 1 written long, and must still answer 1.
	if got, ok := long[3].Int64(); !ok || got != 1 {
		t.Errorf("1 written with %d zeros and an exponent to cancel them answered %d, %v", width, got, ok)
	}
}
