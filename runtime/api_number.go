package runtime

import (
	"encoding/json"
	"math/big"
)

// The exact number core: every numeric keyword, "integer", and the
// literal-keeping decode are decided on the number as a mathematical value, read
// from the literal the document wrote, never from the float64 it rounds to.

// IsInteger reports whether v is an integer: from draft 6 on, a number with a
// zero fractional part however it is written; strict reads "integer" off the
// token, as draft 3 and draft 4 do.
func IsInteger(v any, strict bool) bool { return jsonIsInteger(v, strict) }

// IsNumber reports whether v is a JSON number, however it is held.
func IsNumber(v any) bool { return jsonIsNumber(v) }

// NumberOf is the literal v writes as a number, and whether v is one.
func NumberOf(v any) (string, bool) { return jsonNumberOf(v) }

// RawKind is the JSON type the raw value b is, with "integer" read the way the
// draft reads it (see IsInteger).
func RawKind(b []byte, strict bool) string { return jsonRawKind(b, strict) }

// RawNumber is the literal of b, when b is a JSON number.
func RawNumber(b []byte) (string, bool) { return jsonRawNumber(b) }

// DecimalIsIntegral reports whether the number literal s has a zero fractional
// part.
func DecimalIsIntegral(s string) bool { return jsonDecimalIsIntegral(s) }

// BigIntFromLiteral reads a number literal as a big.Int. isInt says the literal
// is an integer at all; tooLarge says its exponent would build more digits than
// the reader allows.
func BigIntFromLiteral(s string) (_v *big.Int, isInt bool, tooLarge bool) {
	return jsonBigIntFromLiteral(s)
}

// NumberCmp compares a number to the bound b, which is a number literal:
// negative, zero or positive as a is less than, equal to or greater than it.
func NumberCmp(a json.Number, b string) int { return jsonNumberCmp(a, b) }

// NumberBelow reports whether v is a number below the bound b. A value that is
// not a number is not below anything.
func NumberBelow(v any, b string) bool { return jsonNumberBelow(v, b) }

// NumberAbove reports whether v is a number above the bound b.
func NumberAbove(v any, b string) bool { return jsonNumberAbove(v, b) }

// NumberAtMost reports whether v is a number at most the bound b.
func NumberAtMost(v any, b string) bool { return jsonNumberAtMost(v, b) }

// NumberAtLeast reports whether v is a number at least the bound b.
func NumberAtLeast(v any, b string) bool { return jsonNumberAtLeast(v, b) }

// NumberEqual reports whether v is the number b.
func NumberEqual(v any, b string) bool { return jsonNumberEqual(v, b) }

// NumberIsMultipleOf reports whether v is a multiple of m.
func NumberIsMultipleOf(v json.Number, m string) bool { return jsonNumberIsMultipleOf(v, m) }

// NumberNotMultipleOf reports whether v is a number that is not a multiple of
// m. A value that is not a number is no violation.
func NumberNotMultipleOf(v any, m string) bool { return jsonNumberNotMultipleOf(v, m) }

// FloatIsMultipleOf reports whether f is a multiple of m, taking the divisor as
// mDigits digits with mFrac of them after the point.
func FloatIsMultipleOf(f float64, m string, mDigits int64, mFrac int) bool {
	return jsonFloatIsMultipleOf(f, m, mDigits, mFrac)
}

// Canonical is the one text every JSON document equal to data reduces to, so
// that an enum or a const can be decided by comparing two strings.
func Canonical(data []byte) (string, error) { return _jsonCanonical(data) }
