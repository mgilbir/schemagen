package generator

import (
	"encoding/json"
	"math"
	"strconv"

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
func numGoFloatLiteral(v any) string {
	n, ok := schemaNumber(v)
	if !ok {
		return "0"
	}
	return string(n)
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
// Go, but only one of them reads as the integer it is.
//
// The empty string is returned for a value that is not a number, which the
// callers treat as "render it some other way".
func GoNumberLiteral(v any) string {
	n, ok := schemaNumber(v)
	if !ok {
		return ""
	}
	if r, ok := n.Rat(); ok && r.IsInt() {
		return r.Num().String()
	}
	return string(n)
}

// constJSONValue encodes a schema-supplied value as the JSON the generated code
// compares an instance against.
//
// Numbers are folded through float64 here, deliberately, because that is what
// the other side of the comparison is: the emitted check decodes the instance
// into `any` and marshals it back, and encoding/json makes a float64 of a JSON
// number on the way in. Writing the literal instead would put "1.0" on one side
// of an equality whose other side always says "1", and turn an enum that works
// today into a rejection of the document it was written to admit.
//
// So this is the one place a number is read through float64 on purpose, and the
// limitation belongs to the comparison rather than to the value: two integers
// that share a float64 -- 9223372036854775806 and 9223372036854775807 -- remain
// indistinguishable to it. Making it exact means canonicalising numbers by
// value on both sides, which is a change to the emitted decode as much as to
// this, and it is not made here.
func constJSONValue(v any) ([]byte, error) {
	return json.Marshal(foldNumbersToFloat(v))
}

// foldNumbersToFloat rewrites every number a value holds as its float64
// reading, leaving a number float64 cannot hold as the literal it was.
func foldNumbersToFloat(v any) any {
	switch t := v.(type) {
	case json.Number, schema.Number:
		if f, ok := numFloat(t); ok {
			return f
		}
		return v
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = foldNumbersToFloat(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = foldNumbersToFloat(e)
		}
		return out
	}
	return v
}

// numCmp compares two schema numbers exactly, returning -1, 0 or 1, and reports
// whether both could be read. big.Rat parses the JSON number grammar without
// rounding, so 9223372036854775806 and 9223372036854775807 compare as the
// distinct numbers they are rather than as the one float64 they share.
func numCmp(a, b schema.Number) (int, bool) {
	ra, okA := a.Rat()
	rb, okB := b.Rat()
	if !okA || !okB {
		return 0, false
	}
	return ra.Cmp(rb), true
}

// integerComparable reports whether a numeric bound on this schema can be
// enforced in int64 rather than in float64. See ValidationRule.IntegerCompare
// for why that is a correctness question and not a performance one.
//
// Both halves have to hold. The instance has to be an int64, which is what a
// schema saying "integer" -- and nothing else but "null", which only makes the
// field a pointer -- is held as. And the bound has to name an integer int64
// holds exactly, because it is written into the source as an untyped constant
// and Go will refuse one that the int64 it is compared against cannot take.
func integerComparable(s *schema.Schema, bound any) bool {
	if s == nil {
		return false
	}
	if _, ok := numInt64(bound); !ok {
		return false
	}
	integer := false
	for _, t := range s.Type {
		switch t {
		case "integer":
			integer = true
		case "null":
			// A permitted null makes the Go field a pointer; the value it
			// points at is still the int64 the other entry names.
		default:
			return false
		}
	}
	return integer
}

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
