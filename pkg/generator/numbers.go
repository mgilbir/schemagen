package generator

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/mgilbir/schemagen/pkg/schema"
)

// schemaNumber reads a value the generator has been handed as a number and
// returns it as the literal the schema wrote.
//
// Three spellings reach here and all three are legitimate. schema.Number is
// what a numeric keyword read from a document holds. json.Number is what
// const, enum and default hold, because those keywords are typed `any` and the
// schema decoder keeps their numbers as written. The Go numeric kinds are for a
// Schema assembled in Go rather than parsed -- this package's own callers do
// that, and a caller who wrote Enum: []any{5} must not have the member read as
// a non-number and filtered away.
//
// The literal is what every caller wants, because it is the only form that has
// not yet decided how much of the number to keep. Float64, Int64 and the Go
// renderings are all derived from it.
func schemaNumber(v any) (schema.Number, bool) {
	switch n := v.(type) {
	case schema.Number:
		return n, n != ""
	case *schema.Number:
		if n == nil {
			return "", false
		}
		return *n, *n != ""
	case json.Number:
		return schema.Number(n), n != ""
	case float64:
		return schema.NumberFromFloat(n), true
	case float32:
		return schema.NumberFromFloat(float64(n)), true
	case int:
		return schema.Number(strconv.FormatInt(int64(n), 10)), true
	case int8:
		return schema.Number(strconv.FormatInt(int64(n), 10)), true
	case int16:
		return schema.Number(strconv.FormatInt(int64(n), 10)), true
	case int32:
		return schema.Number(strconv.FormatInt(int64(n), 10)), true
	case int64:
		return schema.Number(strconv.FormatInt(n, 10)), true
	case uint:
		return schema.Number(strconv.FormatUint(uint64(n), 10)), true
	case uint8:
		return schema.Number(strconv.FormatUint(uint64(n), 10)), true
	case uint16:
		return schema.Number(strconv.FormatUint(uint64(n), 10)), true
	case uint32:
		return schema.Number(strconv.FormatUint(uint64(n), 10)), true
	case uint64:
		return schema.Number(strconv.FormatUint(n, 10)), true
	}
	return "", false
}

// numFloat is the float64 reading of a schema-supplied number, for the
// decisions that are genuinely about float64: comparing two bounds to see which
// is tighter, asking whether a multipleOf divides another, and the like.
//
// A literal float64 cannot hold at all -- 1e400 -- answers false, and every
// caller has to decide what to do about that rather than silently working from
// an infinity.
func numFloat(v any) (float64, bool) {
	n, ok := schemaNumber(v)
	if !ok {
		return 0, false
	}
	f, ok := n.Float64()
	if !ok || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// numGoFloatLiteral renders a number as a Go float64 constant.
//
// The literal the schema wrote is already a valid Go floating-point constant --
// the JSON number grammar is a subset of Go's -- so it is used verbatim, which
// is what keeps 9223372036854775807 out of the float64 it does not survive.
// Only a Schema assembled in Go, whose number never had a literal, is formatted
// here.
//
// "Verbatim" stops at the width a Go integer constant has to hold, which is
// what goConstLiteral decides; see it for why writing 1e308 out in full is not
// a constant every compiler accepts.
func numGoFloatLiteral(v any) string {
	n, ok := schemaNumber(v)
	if !ok {
		return "0"
	}
	return goConstLiteral(n)
}

// goConstIntBits is how wide a whole number this generator will write into Go
// source in integer notation.
//
// The Go specification requires an implementation to represent an integer
// constant with at least 256 bits of precision, and gc gives 512. 256 is
// therefore the widest integer literal that is portable Go rather than
// gc-specific Go, and anything past it has to be written as a floating-point
// constant, which every implementation rounds instead of refusing.
//
// The bound is not theoretical. Every number float64 can hold is a legitimate
// numeric keyword, and the largest of them is 1.7976931348623157e308 -- three
// hundred and nine digits once it is written out as the whole number it is,
// which is 1024 bits and which gc rejects outright with "constant overflow".
// {"type":"number","maximum":1e308} generated Go that did not compile, behind a
// zero exit code, and so did {"enum":[1e308]} and {"multipleOf":1e308}. See
// issue #269.
const goConstIntBits = 256

// goConstLiteral renders a schema number as a Go constant that a compiler will
// take, preferring the spelling the document used.
//
// Three arms, in the order they are tried:
//
//   - A whole number narrow enough for an integer constant is written in
//     integer notation, so {"const": 1e2} declares 100 rather than 1e2. The two
//     are the same constant to Go and only one of them reads as the integer it
//     is.
//   - Anything the document already wrote in floating-point notation is used as
//     it stands. A Go floating-point constant is rounded to the
//     implementation's precision rather than refused, so 1e308 and
//     1.7976931348623157e308 are both constants; only the integer *notation*
//     for them is not.
//   - What is left is a whole number written out in full and too wide to be a
//     constant -- a hundred and sixty digits of it, say. It becomes the float64
//     it rounds to, which is exactly the reading every remaining caller makes
//     of it: these render bounds and members that are compared as float64.
//     A magnitude float64 cannot hold at all keeps its digits and moves the
//     decimal point, which is a float constant rather than an integer one and
//     so is refused where it is converted rather than where it is written.
//
// The first arm reads the literal's digits and scale (schema.Number.Decimal),
// never a big.Rat: a literal is as long as the document writes it, big.Rat's
// parse is quadratic in the digits, and this renders every numeric keyword and
// member the emitter writes. Only a number already known to have at most
// goConstIntDigits digits is built as a big.Int.
func goConstLiteral(n schema.Number) string {
	lit := string(n)
	if whole, ok := goConstInteger(n); ok {
		return whole
	}
	if strings.ContainsAny(lit, ".eE") {
		return lit
	}
	if f, ok := n.Float64(); ok {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	neg := ""
	digits := lit
	if len(digits) > 0 && (digits[0] == '-' || digits[0] == '+') {
		if digits[0] == '-' {
			neg = "-"
		}
		digits = digits[1:]
	}
	if len(digits) < 2 {
		return lit
	}
	return neg + digits[:1] + "." + digits[1:] + "e" + strconv.Itoa(len(digits)-1)
}

// goConstIntDigits is the most decimal digits a goConstIntBits-bit integer
// has: 2^256 is a 78-digit number. A whole number with more is past the bound
// whatever its digits, and one with fewer than 78 is within it.
const goConstIntDigits = 78

// goConstInteger writes the number in integer notation when it is a whole
// number of at most goConstIntBits bits, and reports whether it is one. It
// decides on the digit count first, so a long literal costs a scan and nothing
// more.
func goConstInteger(n schema.Number) (string, bool) {
	digits, scale, neg, ok := n.Decimal()
	if !ok || scale < 0 {
		return "", false
	}
	if digits == "" {
		return "0", true
	}
	if int64(len(digits))+scale > goConstIntDigits {
		return "", false
	}
	whole, _ := new(big.Int).SetString(digits+strings.Repeat("0", int(scale)), 10)
	if whole.BitLen() > goConstIntBits {
		return "", false
	}
	if neg {
		whole.Neg(whole)
	}
	return whole.String(), true
}

// GoNumberLiteral renders a schema-supplied number as a Go constant.
//
// A JSON number literal is already a Go floating-point literal -- the JSON
// grammar is a subset of Go's -- so the literal is what gets written, and
// 9223372036854775807 reaches the generated source as itself rather than as the
// float64 it does not survive. Go constants are arbitrary-precision until they
// are assigned, so the same literal serves an int64 constant and a float64 one:
// each conversion is checked by the compiler against the value the schema wrote.
//
// A value that names an integer is written in integer notation, so that
// {"const": 1e2} declares 100 rather than 1e2. The two are the same constant to
// Go, but only one of them reads as the integer it is -- up to the width an
// integer constant has to hold, past which goConstLiteral writes it as a
// floating-point one instead.
//
// The empty string is returned for a value that is not a number, which the
// callers treat as "render it some other way".
func GoNumberLiteral(v any) string {
	n, ok := schemaNumber(v)
	if !ok {
		return ""
	}
	return goConstLiteral(n)
}

// constJSONValue encodes a schema-supplied value as the canonical JSON text the
// generated code compares an instance against: the one text every JSON value
// equal to it reduces to (see schema.CanonicalJSON), with every number kept
// exactly.
//
// Every emitted comparison against it is by identity (jsonConstOf and the
// jsonMatches* readers), which read the instance side by the same rule, so 1.0
// in a document satisfies {"const":1} and "A" satisfies {"const":"A"} in
// whatever form the instance is held. This used to fold numbers through
// float64, because the other side of the comparison was an `any` decoded by
// encoding/json and marshalled back -- which made {"const":9007199254740993}
// accept 9007199254740992, and compared a member read from the document as raw
// bytes, so {"k":1.0} failed a const of 1. Both sides are exact now, and the
// fold is gone.
//
// The emitted reduction and schema.CanonicalJSON are held to one answer by
// tests/canonical_agreement_test.go. A value the reduction cannot read -- a Go
// type no decode produces -- falls back to its marshalled text, which can only
// ever equal itself.
func constJSONValue(v any) ([]byte, error) {
	if text, ok := schema.CanonicalJSON(v); ok {
		return []byte(text), nil
	}
	return json.Marshal(v)
}

// exactJSONValue encodes a schema-supplied value as the JSON the document
// wrote, with every number kept as its literal.
//
// It is for the members baked into generated source as the schema wrote them
// -- an enum held as json.RawMessage -- which the generated code reduces with
// _jsonCanonical at package initialisation, so that the reduction of the
// schema's side and of the instance's is one function. See issue #272.
func exactJSONValue(v any) ([]byte, error) {
	return json.Marshal(v)
}

// JSONNumberLiteral renders a schema-supplied number as the JSON number
// literal it was written as, or "" for a value that is not a number.
//
// It is what a value held as a json.Number is written with -- an enum member,
// a default, the bound a jsonNumberCmp call carries. GoNumberLiteral is the
// wrong renderer there: it writes a whole number in integer notation so that
// {"const":1e2} declares a Go constant of 100, and against a type that keeps
// every digit that turns 1e308 into three hundred and nine of them. Both name
// the same number, and only one of them is what the document said.
func JSONNumberLiteral(v any) string {
	n, ok := schemaNumber(v)
	if !ok {
		return ""
	}
	return string(n)
}

// numCmp compares two schema numbers exactly, returning -1, 0 or 1, and reports
// whether both could be read. It reads the digits rather than a float64, so
// 9223372036854775806 and 9223372036854775807 compare as the distinct numbers
// they are, and it reads an exponent of any size. See schema.Number.Compare.
func numCmp(a, b schema.Number) (int, bool) {
	return a.Compare(b)
}

// NumberRoundTripsFloat64 reports whether a number's value is exactly that of
// the shortest decimal its float64 is written as.
//
// It is what lets a check on a float64 be made in float64 and still be exact.
// A float64 is judged as the number it marshals to -- its shortest decimal, see
// jsonNumberOf in the emitted core -- and float64 rounding is monotonic, so for
// a bound B with float64 b: a value f below b marshals to a number below B, one
// above b to one above B, and f equal to b marshals to b's shortest decimal,
// which is B exactly when this holds. The float64 comparison then gives the
// answer the exact one would, for every f. 0.1, 100, 1e308 and 5e-324 all hold;
// 9007199254740993, whose float64 is 9007199254740992, does not, and neither
// does 1e-400, which has none but zero.
func NumberRoundTripsFloat64(v any) bool {
	n, ok := schemaNumber(v)
	if !ok {
		return false
	}
	f, ok := n.Float64()
	if !ok || math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	c, ok := n.Compare(schema.Number(strconv.FormatFloat(f, 'g', -1, 64)))
	return ok && c == 0
}

// NumberDecimalDivisor writes a multipleOf divisor as digits*10^-frac, for the
// float64 fast path in the emitted jsonFloatIsMultipleOf: digits is positive
// and below 2^53, frac is at most 15, and digits carries no trailing zero once
// frac is above zero. ok is false for a divisor that cannot be written so --
// 1e20, 1e-20, 0.1234567890123456789 -- which the fast path then leaves to the
// literal.
//
// The literal is read as digits and a scale, never as a big.Rat: this is asked
// of every multipleOf the emitter writes, and a divisor is as long as the
// document writes it. The digits carry no trailing zero, so the fewest places
// that make the divisor whole are the negated scale, when it is negative.
func NumberDecimalDivisor(v any) (digits int64, frac int, ok bool) {
	n, ok := schemaNumber(v)
	if !ok {
		return 0, 0, false
	}
	ds, scale, neg, ok := n.Decimal()
	if !ok || neg || ds == "" {
		return 0, 0, false
	}
	if scale < 0 {
		if scale < -15 {
			return 0, 0, false
		}
		frac, scale = int(-scale), 0
	}
	// 2^53 has sixteen digits; anything longer is past it.
	if int64(len(ds))+scale > 16 {
		return 0, 0, false
	}
	digits, err := strconv.ParseInt(ds, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	for ; scale > 0; scale-- {
		digits *= 10
	}
	if digits >= 1<<53 {
		return 0, 0, false
	}
	return digits, frac, true
}

// NumberInt64 is numInt64, for the emitter: a numeric check on an int64 is
// written in int64 against a bound this answers, and through the exact core
// against any other.
func NumberInt64(v any) (int64, bool) { return numInt64(v) }

// numInt64 returns the number as an int64 when it names an integer int64 holds
// exactly. 9223372036854775807 answers itself; 9223372036854775808 answers
// nothing, and neither does 1.5.
func numInt64(v any) (int64, bool) {
	n, ok := schemaNumber(v)
	if !ok {
		return 0, false
	}
	return n.Int64()
}
