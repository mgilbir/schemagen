package numbers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/schemagen/internal/testgo"
	"github.com/mgilbir/schemagen/pkg/generator"
)

// numberCoreCase is one question put to the emitted exact-number core, and
// the answer big.Rat gives for it.
type numberCoreCase struct {
	Op     string // cmp, mult, integral, token, floatmult, bigint
	A, B   string
	Digits int64 // floatmult: the divisor as Digits*10^-Frac, or 0
	Frac   int
	Want   string
}

// TestEmittedNumberCoreIsExact compiles the exact-number core the generated
// code decides every numeric keyword through, and holds it to big.Rat on a
// corpus of literals: random ones, the boundaries a float64 cannot tell apart,
// and exponents past int64.
//
// big.Rat is the oracle because it is exact and independent: it reads the
// literal whole into a numerator and a denominator, where the core reads digit
// runs and a scale and never builds the number. The two agreeing on a hundred
// thousand random literals is what says the digit arithmetic -- the lead
// position, the padded digit comparison, the modular reduction multipleOf is
// decided by -- has no case it gets wrong. The float64 fast path is held both
// to big.Rat and to the core's own slow path, since the fast path is only
// allowed to be faster, never different.
//
// The hostile literals are timed: a million-digit mantissa and an exponent of
// 10^20 have to be answered in the time it takes to read them, which is what
// the digit arithmetic is for.
func TestEmittedNumberCoreIsExact(t *testing.T) {
	cases := numberCoreCases(t)
	// The core is the runtime module's, called through its exported API as
	// generated code calls it; the canonical reduction is in it too, its number
	// reduction being read by the same core, and the hostile literals below put
	// it through a huge exponent.
	dir := t.TempDir()
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(numberCoreMain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeTestGoMod(dir, "number_core_test"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, runErr := testgo.Command(ctx, dir, "run", ".").CombinedOutput()
	if runErr != nil {
		t.Fatalf("running the emitted number core: %v\n%s", runErr, out)
	}
	var result struct {
		Answers []string
		Hostile float64 // seconds
	}
	if err := json.Unmarshal([]byte(programOutput(out)), &result); err != nil {
		t.Fatalf("reading the emitted core's answers: %v\n%s", err, out)
	}
	if len(result.Answers) != len(cases) {
		t.Fatalf("got %d answers for %d cases", len(result.Answers), len(cases))
	}
	failures := 0
	for i, c := range cases {
		if result.Answers[i] != c.Want {
			failures++
			if failures <= 20 {
				t.Errorf("%s(%s, %s; %d, %d) = %s, big.Rat says %s", c.Op, c.A, c.B, c.Digits, c.Frac, result.Answers[i], c.Want)
			}
		}
	}
	if failures > 20 {
		t.Errorf("... and %d more", failures-20)
	}
	if result.Hostile > 2 {
		t.Errorf("the hostile literals took %.2fs of CPU; the core is meant to answer them in the time it takes to read them", result.Hostile)
	}
	t.Logf("%d cases agree with big.Rat; hostile literals answered in %.3fs of CPU", len(cases), result.Hostile)
}

const numberCoreMain = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	rt "github.com/mgilbir/schemagen/runtime"
)

type numberCoreCase struct {
	Op     string
	A, B   string
	Digits int64
	Frac   int
}

func main() {
	raw, err := os.ReadFile("cases.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var cases []numberCoreCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	answers := make([]string, len(cases))
	for i, c := range cases {
		switch c.Op {
		case "cmp":
			answers[i] = strconv.Itoa(rt.NumberCmp(json.Number(c.A), c.B))
		case "mult":
			answers[i] = strconv.FormatBool(rt.NumberIsMultipleOf(json.Number(c.A), c.B))
		case "integral":
			answers[i] = strconv.FormatBool(rt.DecimalIsIntegral(c.A))
		case "token":
			// "integer" read off the token, as draft 3 and 4 read it.
			answers[i] = strconv.FormatBool(rt.IsInteger(json.Number(c.A), true))
		case "floatmult":
			f, _ := strconv.ParseFloat(c.A, 64)
			fast := rt.FloatIsMultipleOf(f, c.B, c.Digits, c.Frac)
			slow := rt.FloatIsMultipleOf(f, c.B, 0, 0)
			if fast != slow {
				answers[i] = "fast and slow paths disagree"
			} else {
				answers[i] = strconv.FormatBool(fast)
			}
		case "bigint":
			v, ok, tooLarge := rt.BigIntFromLiteral(c.A)
			switch {
			case tooLarge:
				answers[i] = "too large"
			case !ok:
				answers[i] = "not an integer"
			default:
				answers[i] = v.String()
			}
		default:
			answers[i] = "unknown op " + c.Op
		}
	}

	// Timed by the CPU this process spends, user and system, not the wall
	// clock: the test binaries go test ./... runs beside this one make the wall
	// clock count the time spent waiting for a CPU. The process does nothing
	// else while the literals are answered.
	long := "1" + strings.Repeat("7", 1<<20)
	start := processCPU()
	rt.NumberIsMultipleOf(json.Number(long), "7")
	rt.NumberIsMultipleOf(json.Number(long), "123456789012345678901234567890")
	rt.NumberIsMultipleOf(json.Number(long+"e99999999999999999999"), "3.3")
	rt.NumberCmp(json.Number(long), long+"1")
	rt.DecimalIsIntegral(long + "e-99999999999999999999")
	rt.Canonical([]byte(long + "0000e99999999999999999999"))
	rt.BigIntFromLiteral(long)
	rt.BigIntFromLiteral("1e1000000000000")
	rt.NumberIsMultipleOf(json.Number("1e1000000000000"), "7")
	hostile := (processCPU() - start).Seconds()

	enc, err := json.Marshal(struct {
		Answers []string
		Hostile float64
	}{answers, hostile})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(enc))
}

func processCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		panic(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
`

// numberCoreCases builds the corpus and its big.Rat answers.
func numberCoreCases(t *testing.T) []numberCoreCase {
	t.Helper()
	r := rand.New(rand.NewSource(20260928))
	rat := func(s string) *big.Rat {
		v, ok := new(big.Rat).SetString(s)
		if !ok {
			t.Fatalf("big.Rat cannot read %q", s)
		}
		return v
	}
	var cases []numberCoreCase
	cmpCase := func(a, b string) {
		cases = append(cases, numberCoreCase{Op: "cmp", A: a, B: b, Want: strconv.Itoa(rat(a).Cmp(rat(b)))})
	}
	multCase := func(v, m string) {
		mr := rat(m)
		if mr.Sign() == 0 {
			return
		}
		cases = append(cases, numberCoreCase{Op: "mult", A: v, B: m, Want: strconv.FormatBool(new(big.Rat).Quo(rat(v), mr).IsInt())})
	}
	for i := 0; i < 40000; i++ {
		a, b := randomNumberLiteral(r), randomNumberLiteral(r)
		cmpCase(a, b)
		// The same number spelled another way, which must compare equal.
		cmpCase(a, respell(r, a))
		multCase(a, b)
		// A genuine multiple, which the random pair above almost never is.
		mr := rat(b)
		if mr.Sign() != 0 {
			multCase(new(big.Rat).Mul(mr, big.NewRat(int64(r.Intn(2001)-1000), 1)).FloatString(45), b)
		}
		cases = append(cases,
			numberCoreCase{Op: "integral", A: a, Want: strconv.FormatBool(rat(a).IsInt())},
			numberCoreCase{Op: "token", A: a, Want: strconv.FormatBool(!strings.ContainsAny(a, ".eE"))},
		)
	}
	// The boundaries a float64 cannot tell apart, and the audit's examples.
	for _, p := range [][2]string{
		{"9007199254740993", "9007199254740992"}, {"9007199254740992", "9007199254740993"},
		{"9223372036854775807", "9223372036854775808"}, {"1e99", "1000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001"},
		{"0.1", "0.1000000000000000055511151231257827"}, {"-0", "0"}, {"-0.0e5", "0e-5"},
		{"1.0", "1"}, {"1e0", "1"}, {"100", "1e2"}, {"1.50", "1.5"}, {"1e-10", "0"},
		{"12345678901234567891.5", "12345678901234567891"}, {"1e100", "1e99"},
	} {
		cmpCase(p[0], p[1])
	}
	for _, p := range [][2]string{
		{"0.3", "0.1"}, {"1.0000000001", "1"}, {"1e-10", "1"}, {"1e99", "7"}, {"7e98", "7"},
		{"4611686018427387905", "4611686018427387904"}, {"0.0075", "0.0001"}, {"10", "0.3"},
		{"1e308", "3"}, {"0", "0.7"}, {"-21", "7"}, {"1e-400", "1e-401"},
		{"123456789012345678901234567890", "123456789012345678901234567891"},
		{"246913578024691357802469135780", "123456789012345678901234567890"},
	} {
		multCase(p[0], p[1])
	}
	// Exponents past int64. big.Rat cannot build these, so the answers are
	// written out: each follows from the leading-digit position, or from
	// 10^k mod d, which is periodic.
	for _, c := range []numberCoreCase{
		{Op: "cmp", A: "1e99999999999999999999", B: "10e99999999999999999998", Want: "0"},
		{Op: "cmp", A: "1e99999999999999999999", B: "1e99999999999999999998", Want: "1"},
		{Op: "cmp", A: "1e99999999999999999998", B: "1e99999999999999999999", Want: "-1"},
		{Op: "cmp", A: "-1e99999999999999999999", B: "1", Want: "-1"},
		{Op: "cmp", A: "1e-99999999999999999999", B: "0", Want: "1"},
		{Op: "cmp", A: "1e-99999999999999999999", B: "2e-99999999999999999999", Want: "-1"},
		{Op: "cmp", A: "1e-99999999999999999999", B: "1e-1000", Want: "-1"},
		{Op: "integral", A: "1e99999999999999999999", Want: "true"},
		{Op: "integral", A: "1e-99999999999999999999", Want: "false"},
		{Op: "integral", A: "0e-99999999999999999999", Want: "true"},
		// 10^k mod 7 cycles with period 6, and 10^20 mod 6 is 4, so
		// 10^(10^20) mod 7 is 10^4 mod 7 = 4: not a multiple.
		{Op: "mult", A: "1e100000000000000000000", B: "7", Want: "false"},
		{Op: "mult", A: "7e100000000000000000000", B: "7", Want: "true"},
		{Op: "mult", A: "1e100000000000000000000", B: "2", Want: "true"},
		{Op: "mult", A: "1e100000000000000000000", B: "1e99999999999999999999", Want: "true"},
		{Op: "mult", A: "1e99999999999999999999", B: "1e100000000000000000000", Want: "false"},
		{Op: "mult", A: "1e-99999999999999999999", B: "1", Want: "false"},
		{Op: "mult", A: "3e-99999999999999999999", B: "1e-99999999999999999999", Want: "true"},
	} {
		cases = append(cases, c)
	}
	// The float64 multipleOf fast path, over floats of every shape and
	// divisors it can and cannot take.
	for i := 0; i < 20000; i++ {
		var f float64
		switch r.Intn(3) {
		case 0:
			f = math.Float64frombits(r.Uint64())
		case 1:
			f = float64(r.Int63n(1<<40)) / math.Pow(10, float64(r.Intn(12)))
		default:
			f, _ = strconv.ParseFloat(randomNumberLiteral(r), 64)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		frac := r.Intn(16)
		digits := 1 + r.Int63n(1000)
		m := new(big.Rat).SetFrac(big.NewInt(digits), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(frac)), nil))
		ms := m.FloatString(frac)
		// A json.Number, which the generator reads as a number; a plain
		// string is not one to it, and would leave the fast path untried.
		d, fr, ok := generator.NumberDecimalDivisor(json.Number(ms))
		if !ok {
			t.Fatalf("the generator could not write %s as digits and places", ms)
		}
		lit := strconv.FormatFloat(f, 'g', -1, 64)
		want := new(big.Rat).Quo(rat(lit), m).IsInt()
		cases = append(cases, numberCoreCase{Op: "floatmult", A: lit, B: ms, Digits: d, Frac: fr, Want: strconv.FormatBool(want)})
	}
	for _, c := range []struct{ f, m string }{{"0.3", "0.1"}, {"1.0000000001", "1"}, {"1e-10", "1"}, {"4611686018427387904", "4611686018427387904"}, {"12.34", "0.01"}, {"-0", "0.5"}} {
		d, fr, ok := generator.NumberDecimalDivisor(json.Number(c.m))
		if !ok {
			d, fr = 0, 0
		}
		f, _ := strconv.ParseFloat(c.f, 64)
		lit := strconv.FormatFloat(f, 'g', -1, 64)
		cases = append(cases, numberCoreCase{Op: "floatmult", A: c.f, B: c.m, Digits: d, Frac: fr,
			Want: strconv.FormatBool(new(big.Rat).Quo(rat(lit), rat(c.m)).IsInt())})
	}
	// The big-int reading: exact, and bounded in the zeros an exponent adds.
	for _, c := range []numberCoreCase{
		{Op: "bigint", A: "12345678901234567891.5", Want: "not an integer"},
		{Op: "bigint", A: "12345678901234567891.0", Want: "12345678901234567891"},
		{Op: "bigint", A: "1e100", Want: "1" + strings.Repeat("0", 100)},
		{Op: "bigint", A: "-1.5e1", Want: "-15"},
		{Op: "bigint", A: "1e10000", Want: "1" + strings.Repeat("0", 10000)},
		{Op: "bigint", A: "1e10001", Want: "too large"},
		{Op: "bigint", A: "1e99999999999999999999", Want: "too large"},
		{Op: "bigint", A: "1e-99999999999999999999", Want: "not an integer"},
		{Op: "bigint", A: "0e99999999999999999999", Want: "0"},
		{Op: "bigint", A: "-0.0", Want: "0"},
	} {
		cases = append(cases, c)
	}
	long := "1" + strings.Repeat("3", 3000)
	cases = append(cases, numberCoreCase{Op: "bigint", A: long, Want: long})
	return cases
}

// randomNumberLiteral writes a JSON number with a sign, leading zeros where
// the grammar allows them, runs of zeros inside and at the ends of both digit
// runs, and an exponent in either case with either sign -- the spellings the
// core has to line up.
func randomNumberLiteral(r *rand.Rand) string {
	var b strings.Builder
	if r.Intn(3) == 0 {
		b.WriteByte('-')
	}
	if r.Intn(4) == 0 {
		b.WriteByte('0')
	} else {
		b.WriteByte(byte('1' + r.Intn(9)))
		for i, n := 0, r.Intn(25); i < n; i++ {
			if r.Intn(4) == 0 {
				b.WriteByte('0')
			} else {
				b.WriteByte(byte('0' + r.Intn(10)))
			}
		}
	}
	if r.Intn(2) == 0 {
		b.WriteByte('.')
		for i, n := 0, 1+r.Intn(20); i < n; i++ {
			if r.Intn(3) == 0 {
				b.WriteByte('0')
			} else {
				b.WriteByte(byte('0' + r.Intn(10)))
			}
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
		b.WriteString(strconv.Itoa(r.Intn(40)))
	}
	return b.String()
}

// respell writes the same number another way: its value times a power of ten,
// with the exponent moved to compensate.
func respell(r *rand.Rand, lit string) string {
	k := r.Intn(5)
	v, ok := new(big.Rat).SetString(lit)
	if !ok {
		return lit
	}
	scaled := new(big.Rat).Mul(v, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil)))
	return fmt.Sprintf("%se-%d", scaled.FloatString(40), k)
}
