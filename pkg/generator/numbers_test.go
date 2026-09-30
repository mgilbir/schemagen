package generator

import (
	"encoding/json"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
		// Every row below is a whole number too wide for the integer constant
		// the rows above become. Writing one out in full is what issue #269
		// was: a Go integer constant is only guaranteed 256 bits, gc gives 512
		// and refuses more, and 1e308 -- a perfectly ordinary "maximum" -- is
		// three hundred and nine digits and 1024 bits once expanded. So the
		// spelling the document used is kept where it is already a
		// floating-point one, and a whole number written out in full becomes
		// the float64 it rounds to. See goConstLiteral.
		{"a magnitude past float64 keeps its exponent", json.Number("1e400"), "1e400"},
		{"an exponent past what Rat will build is left as written", json.Number("1e999999"), "1e999999"},
		{"the largest float64 stays in exponent notation", schema.Number("1.7976931348623157e308"), "1.7976931348623157e308"},
		{"a whole number at the top of float64's range keeps its exponent", schema.Number("1e308"), "1e308"},
		{"a whole number just inside the constant bound is written out", json.Number("1e70"), "1" + strings.Repeat("0", 70)},
		{"a whole number just past it keeps the exponent it was written with", json.Number("1e78"), "1e78"},
		{"a wide whole number written out in full is folded to float64", json.Number("1" + strings.Repeat("0", 159)), "1e+159"},
		// And past float64 as well as past the constant bound, which is the one
		// arm with no float64 to fall back on. The digits are kept and the point
		// is moved, so what is written is a floating-point constant -- refused
		// where it is converted to float64, which is a magnitude no float64
		// holds, rather than refused as an integer constant nobody could hold.
		{"a wide whole number past float64 keeps its digits", json.Number("1" + strings.Repeat("1", 399)), "1." + strings.Repeat("1", 399) + "e399"},
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

// TestGoNumberLiteralIntegerArmAgreesWithBigRat holds the integer-notation arm,
// which reads digits and a scale, to the rule it replaced: big.Rat says the
// number is whole and at most goConstIntBits bits wide. The rows sit on the
// edges -- 2^256 has 78 digits, 2^256-1 is the widest that fits -- and the
// random ones spell small wholes and fractions every way JSON allows.
func TestGoNumberLiteralIntegerArmAgreesWithBigRat(t *testing.T) {
	two256 := new(big.Int).Lsh(big.NewInt(1), 256)
	max := new(big.Int).Sub(two256, big.NewInt(1))
	lits := []string{
		max.String(), "-" + max.String(), two256.String(), "-" + two256.String(),
		max.String() + ".000", "0." + max.String() + "e78", two256.String()[:77] + "e1",
		"1e76", "1e77", "1e78", "9e77", "0", "-0", "0.0e5", "1.5", "150e-1", "15e-1",
		"1" + strings.Repeat("0", 77), "1" + strings.Repeat("0", 78),
	}
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 2000; i++ {
		digits := strconv.FormatInt(r.Int63n(1<<(1+r.Intn(62))), 10)
		lit := digits
		switch r.Intn(4) {
		case 0:
			lit += "e" + strconv.Itoa(r.Intn(90))
		case 1:
			lit += "e-" + strconv.Itoa(r.Intn(20))
		case 2:
			lit += "." + strings.Repeat("0", r.Intn(4)) + "e" + strconv.Itoa(r.Intn(80))
		}
		if r.Intn(3) == 0 {
			lit = "-" + lit
		}
		lits = append(lits, lit)
	}
	for _, lit := range lits {
		v, _ := new(big.Rat).SetString(lit)
		want, wantOK := "", false
		if v.IsInt() && v.Num().BitLen() <= goConstIntBits {
			want, wantOK = v.Num().String(), true
		}
		got, ok := goConstInteger(schema.Number(lit))
		if ok != wantOK || got != want {
			t.Errorf("goConstInteger(%s) = %q, %v; big.Rat says %q, %v", lit, got, ok, want, wantOK)
		}
	}
}

// TestGoNumberLiteralIsBoundedOnLongLiterals: every numeric keyword and member
// the emitter writes is rendered through GoNumberLiteral, and a literal is as
// long as the document writes it. It used to go through big.Rat, whose parse
// is quadratic in the digits.
func TestGoNumberLiteralIsBoundedOnLongLiterals(t *testing.T) {
	const width = 1000000
	cases := []struct {
		lit  string
		want string
	}{
		// 1 written long: whole, and read as 1.
		{"1" + strings.Repeat("0", width) + "e-" + strconv.Itoa(width), "1"},
		// A long fraction keeps its spelling.
		{"1." + strings.Repeat("7", width), "1." + strings.Repeat("7", width)},
		// A long whole number past float64 keeps its digits as a float constant.
		{strings.Repeat("9", width), "9." + strings.Repeat("9", width-1) + "e" + strconv.Itoa(width-1)},
	}
	elapsed := cpuTimeOf(func() {
		for _, c := range cases {
			if got := GoNumberLiteral(json.Number(c.lit)); got != c.want {
				t.Errorf("GoNumberLiteral(%.30s...) = %.40s..., want %.40s...", c.lit, got, c.want)
			}
		}
	})
	if elapsed > time.Second {
		t.Errorf("rendering three %d-digit literals took %v of CPU", width, elapsed)
	}
}

// cpuTimeOf is the CPU time, user and system, the process spends running f.
//
// The hostile-literal tests below bound a cost, and the wall clock does not
// measure one on a machine that go test ./... is also running a dozen other
// test binaries on: it counts the time the process waited for a CPU. Under a
// load average of 20 the million-digit lcm took 5.8s of wall clock, and at a
// load average of 67 it measures about a second of CPU. The process's CPU time
// counts only work, its own
// and its garbage collector's. No test in this package is parallel, so the
// process's time is the test's.
func cpuTimeOf(f func()) time.Duration {
	before := processCPU()
	f()
	return processCPU() - before
}

// processCPU is the CPU time the process has used so far, user and system.
func processCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		panic(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// TestExactEnumValueKeyIsOneKeyPerValue: the key an enum intersection compares
// members by is the value's, whatever the spelling and however far the
// exponent reaches, and two values never share one.
func TestExactEnumValueKeyIsOneKeyPerValue(t *testing.T) {
	same := [][]any{
		{json.Number("1"), json.Number("1.0"), json.Number("10e-1"), 1.0, 1},
		{json.Number("1e6000"), json.Number("10e5999"), json.Number("0.1e6001")},
		{json.Number("9007199254740993"), json.Number("9.007199254740993e15")},
	}
	seen := map[string]int{}
	for gi, g := range same {
		k0 := exactEnumValueKey(g[0])
		for _, v := range g[1:] {
			if k := exactEnumValueKey(v); k != k0 {
				t.Errorf("%v and %v are one value but key as %q and %q", g[0], v, k0, k)
			}
		}
		if prev, dup := seen[k0]; dup {
			t.Errorf("groups %d and %d are different values and share the key %q", prev, gi, k0)
		}
		seen[k0] = gi
	}
	if exactEnumValueKey(json.Number("9007199254740992")) == exactEnumValueKey(json.Number("9007199254740993")) {
		t.Error("9007199254740992 and 9007199254740993 share a key")
	}
}

// TestNumberRulesAreMarkedFromTheGoType covers the decision that picks which
// form a numeric check is written in. Every form is exact, so what this guards
// is the fast one: a form written for a type the value is not held as does not
// compile, and a type a form exists for that is left unmarked is only slower.
func TestNumberRulesAreMarkedFromTheGoType(t *testing.T) {
	tests := []struct {
		name string
		t    GoType
		want NumOperandKind
	}{
		{"an int64", &PrimitiveType{Name: "int64"}, NumOperandInt64},
		{"a pointer to one", &PointerType{Inner: &PrimitiveType{Name: "int64"}}, NumOperandInt64},
		{"a float64", &PrimitiveType{Name: "float64"}, NumOperandFloat64},
		{"a json.Number", &PrimitiveType{Name: GoNumberTypeName}, NumOperandJSONNumber},
		{"an any", &PrimitiveType{Name: "any"}, NumOperandAny},
		{"a raw message", &PrimitiveType{Name: GoRawTypeName}, NumOperandAny},
		{"a named type", &NamedType{Name: "Age"}, NumOperandAny},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, rt := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"} {
				r := ValidationRule{RuleType: rt, Value: schema.Number("5")}
				markNumberRule(&r, tc.t)
				if r.NumOperand != tc.want {
					t.Errorf("%s: NumOperand = %v, want %v", rt, r.NumOperand, tc.want)
				}
			}
			numeric := ValidationRule{RuleType: "const", Value: "5", ExactValue: "5"}
			markNumberRule(&numeric, tc.t)
			if numeric.NumOperand != tc.want {
				t.Errorf("numeric const: NumOperand = %v, want %v", numeric.NumOperand, tc.want)
			}
			other := ValidationRule{RuleType: "const", Value: `"x"`}
			markNumberRule(&other, tc.t)
			if other.NumOperand != NumOperandAny {
				t.Errorf("a const that is not a number was marked %v", other.NumOperand)
			}
			length := ValidationRule{RuleType: "minLength", Value: 3}
			markNumberRule(&length, tc.t)
			if length.NumOperand != NumOperandAny {
				t.Errorf("minLength was marked %v", length.NumOperand)
			}
		})
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
		{"an integer written with an exponent", num("1e2"), num("5"), "1e2"},
		{"one fraction a multiple of the other", num("0.5"), num("0.25"), "0.5"},
		// Neither divides the other, and the float64 path kept the first --
		// dropping the second divisor from the merged schema outright.
		{"fractions neither of which divides the other", num("0.3"), num("0.2"), "0.6"},
		{"a fraction and an integer", num("0.5"), num("3"), "3"},
		{"a fraction and an integer it does not divide", num("0.4"), num("3"), "6"},
		// 0.3/0.1 is 2.9999999999999996 in float64, which the old path read as
		// "not a multiple" and so kept 0.1 alone.
		{"a multiple float64 cannot see", num("0.1"), num("0.3"), "0.3"},
		{"large coprime integers", num("9007199254740993"), num("2"), "18014398509481986"},
		// The places come from whichever of the denominator's twos and fives
		// runs longer: 3/16 needs four, 3/25 two.
		{"a denominator of twos alone", num("0.0625"), num("0.09375"), "0.1875"},
		{"a denominator of fives alone", num("0.04"), num("0.06"), "0.12"},
		// A new divisor is written in the canonical spelling, which puts a
		// magnitude this small in exponent form.
		{"long fractions", num("0." + strings.Repeat("0", 400) + "4"), num("0." + strings.Repeat("0", 400) + "6"), "1.2e-400"},
		// Scales far apart: 1 written long, with an exponent past what
		// big.Rat reads, beside 0.3. It used to be dropped and 0.3 kept.
		{"one divisor past big.Rat's exponent limit", num("1" + strings.Repeat("0", 6000) + "e-6000"), num("0.3"), "3"},
		{"divisors ten thousand places apart", num("1e-10000"), num("7"), "7"},
		{"a divisor's own twos and fives", num("0.0625"), num("12.5"), "12.5"},
		{"negative divisors are their magnitudes", num("-0.3"), num("0.2"), "0.6"},
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

// TestCombineMultipleOfAgreesWithBigRat holds the prime-by-prime lcm to the
// one big.Rat computes -- lcm(p1,p2)/gcd(q1,q2) on the fractions in lowest
// terms -- over divisors built to be rich in twos and fives, at scales from
// far apart to equal.
func TestCombineMultipleOfAgreesWithBigRat(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	lit := func() string {
		m := big.NewInt(1 + r.Int63n(1000))
		m.Mul(m, new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(r.Intn(40))), nil))
		m.Mul(m, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(r.Intn(40))), nil))
		s := m.String() + "e" + strconv.Itoa(r.Intn(120)-80)
		if r.Intn(4) == 0 {
			s = "-" + s
		}
		return s
	}
	for i := 0; i < 3000; i++ {
		a, b := schema.Number(lit()), schema.Number(lit())
		ar, _ := new(big.Rat).SetString(string(a))
		br, _ := new(big.Rat).SetString(string(b))
		ar.Abs(ar)
		br.Abs(br)
		num := new(big.Int).Mul(ar.Num(), br.Num())
		num.Quo(num, new(big.Int).GCD(nil, nil, ar.Num(), br.Num()))
		want := new(big.Rat).SetFrac(num, new(big.Int).GCD(nil, nil, ar.Denom(), br.Denom()))
		got := combineMultipleOf(&a, &b)
		gr, ok := new(big.Rat).SetString(strings.TrimPrefix(string(*got), "-"))
		if !ok || gr.Cmp(want) != 0 {
			t.Errorf("combineMultipleOf(%s, %s) = %s, big.Rat says %s", a, b, *got, want.RatString())
		}
	}
}

// TestCombineMultipleOfIsBoundedOnLongLiterals is the hostile half. A divisor
// is a literal of any length the document cares to write, and writing the lcm
// back out once took a division per decimal place over a number that many
// places long: 300000 digits held generation for over a minute. Reading the
// divisors through big.Rat was quadratic as well (two million-digit divisors,
// 4.5s). The lcm is taken on digits and exponents now, and the big.Int work is
// multiplication and division, which math/big does subquadratically.
//
// The first half checks the answer, against big.Rat, on literals short enough
// for big.Rat to read quickly; the second times the long ones, including a
// divisor that is a power of five, the case that has the most factors to
// divide out.
func TestCombineMultipleOfIsBoundedOnLongLiterals(t *testing.T) {
	t.Run("million digits", func(t *testing.T) {
		const width = 1000000
		a := schema.Number("0." + strings.Repeat("0", 100) + strings.Repeat("6", width))
		b := schema.Number("0." + strings.Repeat("0", 99) + strings.Repeat("4", width))
		p := schema.Number(new(big.Int).Exp(big.NewInt(5), big.NewInt(430000), nil).String())
		three := schema.Number("3")
		elapsed := cpuTimeOf(func() {
			combineMultipleOf(&a, &b)
			combineMultipleOf(&p, &three)
		})
		if elapsed > 5*time.Second {
			t.Errorf("combining million-digit divisors and a 300000-digit power of five took %v of CPU", elapsed)
		}
		t.Logf("combined the million-digit divisors in %v of CPU", elapsed)
	})

	// 6R and 40R over the same power of ten, R a hundred thousand ones: the
	// numerators share 2R, so a product that skipped the gcd would still be a
	// common multiple, and only the check that it is the least would see it.
	a := schema.Number("0." + strings.Repeat("0", 100) + strings.Repeat("6", 100000))
	b := schema.Number("0." + strings.Repeat("0", 99) + strings.Repeat("4", 100000))
	var got *schema.Number
	elapsed := cpuTimeOf(func() { got = combineMultipleOf(&a, &b) })
	if got == nil {
		t.Fatal("combineMultipleOf gave nothing for two decimal divisors")
	}
	if elapsed > 3*time.Second {
		t.Errorf("combining two 100000-digit divisors took %v of CPU", elapsed)
	}
	// The result is a common multiple of both, and the least one: it is a
	// whole multiple of each, and the two multiples share no factor -- one they
	// shared could be divided out to leave a smaller common multiple.
	lcm, ok := got.Rat()
	if !ok {
		t.Fatalf("the combined divisor %.40s... is not a number", string(*got))
	}
	var quotients []*big.Int
	for _, d := range []schema.Number{a, b} {
		r, _ := d.Rat()
		q := new(big.Rat).Quo(lcm, r)
		if !q.IsInt() {
			t.Fatalf("the combined divisor is not a multiple of %.40s...", string(d))
		}
		quotients = append(quotients, q.Num())
	}
	if g := new(big.Int).GCD(nil, nil, quotients[0], quotients[1]); g.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("the combined divisor is a common multiple but not the least: both quotients share %.40s...", g.String())
	}
	t.Logf("combined in %v of CPU", elapsed)
}

// TestFastFormQueriesAgreeWithBigRat covers the two questions that decide
// whether a numeric check on a float64 is written in float64: does the bound's
// value survive the float64 round trip, and can a multipleOf divisor be
// written as digits*10^-frac for the float fast path. A wrong yes to either
// is a wrong verdict in the generated code, so each is held to an answer
// worked out on big.Rat.
func TestFastFormQueriesAgreeWithBigRat(t *testing.T) {
	lits := []string{
		"0.1", "100", "1e308", "5e-324", "1.7976931348623157e308", "9007199254740992",
		"9007199254740993", "1e-400", "0.30000000000000004", "0.300000000000000044",
		"0.3", "3e-1", "0.30", "1e23", "1e22", "123456789012345678", "-0.5", "0", "-0",
		"0.000000000000001", "0.0000000000000001", "0.000000000000001000", "1.5e-15",
		"9007199254740991", "900719925474099.1", "90071992547409.92", "9.007199254740991e15",
		"4503599627370496.5", "1.23456789012345e-3", "12345678901234567e-15",
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 3000; i++ {
		lits = append(lits, strconv.FormatInt(1+r.Int63n(1<<(1+r.Intn(55))), 10)+"e-"+strconv.Itoa(r.Intn(20)))
	}
	for _, lit := range lits {
		v, ok := new(big.Rat).SetString(lit)
		if !ok {
			t.Fatalf("%s is not a number", lit)
		}

		wantRT := false
		if f, err := strconv.ParseFloat(lit, 64); err == nil {
			back, _ := new(big.Rat).SetString(strconv.FormatFloat(f, 'g', -1, 64))
			wantRT = back.Cmp(v) == 0
		}
		if got := NumberRoundTripsFloat64(json.Number(lit)); got != wantRT {
			t.Errorf("NumberRoundTripsFloat64(%s) = %v, want %v", lit, got, wantRT)
		}

		// The reference: the fewest places that make the divisor whole, and
		// the whole number it makes, when that is below 2^53 within 15 places.
		var wantDigits int64
		wantFrac, wantOK := 0, false
		if v.Sign() > 0 {
			for frac := 0; frac <= 15; frac++ {
				scaled := new(big.Rat).Mul(v, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(frac)), nil)))
				if scaled.IsInt() {
					if n := scaled.Num(); n.IsInt64() && n.Int64() < 1<<53 {
						wantDigits, wantFrac, wantOK = n.Int64(), frac, true
					}
					break
				}
			}
		}
		d, fr, ok := NumberDecimalDivisor(json.Number(lit))
		if ok != wantOK || d != wantDigits || fr != wantFrac {
			t.Errorf("NumberDecimalDivisor(%s) = %d, %d, %v; want %d, %d, %v", lit, d, fr, ok, wantDigits, wantFrac, wantOK)
		}
	}

	// A Go string is not a number to the generator. This is the mistake that
	// once sent every check in the emitter down the slow path: the query was
	// handed the literal as a string, and answered no.
	if NumberRoundTripsFloat64("0.1") {
		t.Error("NumberRoundTripsFloat64 read a Go string as a number")
	}
	if _, _, ok := NumberDecimalDivisor("0.1"); ok {
		t.Error("NumberDecimalDivisor read a Go string as a number")
	}

	// And a divisor is as long as the document writes it.
	long := json.Number("1" + strings.Repeat("0", 1000000) + "e-1000000")
	var (
		d  int64
		fr int
		ok bool
	)
	if elapsed := cpuTimeOf(func() { d, fr, ok = NumberDecimalDivisor(long) }); elapsed > time.Second {
		t.Errorf("NumberDecimalDivisor on a million-digit literal took %v of CPU", elapsed)
	}
	if !ok || d != 1 || fr != 0 {
		t.Errorf("NumberDecimalDivisor on 1 written long = %d, %d, %v; want 1, 0, true", d, fr, ok)
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
