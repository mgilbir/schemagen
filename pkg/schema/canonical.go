package schema

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// This file holds one question: when are two JSON values the same value?
//
// Every draft answers it the same way, and none of them answers it in terms of
// an encoding. Two objects are equal when they have the same members whatever
// order they were written in; two numbers are equal when they are
// mathematically equal, so 1, 1.0 and 1e0 are one number and so are 1.50 and
// 1.5. That is the equality "enum", "const" and "uniqueItems" are defined over.
//
// The answer is given as a canonical text rather than as a comparison, because
// the callers need it in that shape: an enum bakes one string per member into
// generated source and the generated code compares the instance's own canonical
// text against them. A comparison would have to be emitted as a function over
// two decoded documents instead, which is a great deal more generated code for
// the same verdict.
//
// The canonical text of a value is itself valid JSON, and for every value a
// float64 holds exactly it is byte for byte what encoding/json would have
// written -- which is what keeps this from moving the numbers that were already
// being compared correctly. It parts company with encoding/json exactly where
// float64 loses information: 123456789012345678901234567890 canonicalises to
// itself here and to 1.2345678901234568e+29 through a float64, and the second
// is equally the canonical form of 123456789012345678901234567891. See issue
// #272.

// CanonicalJSON returns the canonical text of a decoded JSON value, and reports
// whether the value could be read as one.
//
// The value is what a JSON decode produces: nil, bool, string, a number held as
// json.Number or Number, []any or map[string]any. The numeric Go kinds are
// accepted too, for a schema assembled in Go rather than parsed.
//
// False is the answer for anything else -- a Go type no decode produces -- and
// for a number whose exponent will not fit in an int64. Both leave the caller
// to fall back on whatever it did before rather than inventing an answer.
func CanonicalJSON(v any) (string, bool) {
	var b strings.Builder
	if !appendCanonicalJSON(&b, v) {
		return "", false
	}
	return b.String(), true
}

func appendCanonicalJSON(b *strings.Builder, v any) bool {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
		return true
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return true
	case string:
		enc, err := json.Marshal(t)
		if err != nil {
			return false
		}
		b.Write(enc)
		return true
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if !appendCanonicalJSON(b, e) {
				return false
			}
		}
		b.WriteByte(']')
		return true
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			enc, err := json.Marshal(k)
			if err != nil {
				return false
			}
			b.Write(enc)
			b.WriteByte(':')
			if !appendCanonicalJSON(b, t[k]) {
				return false
			}
		}
		b.WriteByte('}')
		return true
	}
	n, ok := canonicalNumberOf(v)
	if !ok {
		return false
	}
	text, ok := n.CanonicalText()
	if !ok {
		return false
	}
	b.WriteString(text)
	return true
}

// canonicalNumberOf reads the number kinds a decoded or hand-built value can
// hold a number as. It is deliberately narrower than the generator's own
// schemaNumber: this package cannot see that one, and the kinds a JSON decode
// produces are the two string-backed ones.
func canonicalNumberOf(v any) (Number, bool) {
	switch t := v.(type) {
	case Number:
		return t, t != ""
	case json.Number:
		return Number(t), t != ""
	case float64:
		return NumberFromFloat(t), true
	case float32:
		return NumberFromFloat(float64(t)), true
	case int:
		return Number(strconv.FormatInt(int64(t), 10)), true
	case int64:
		return Number(strconv.FormatInt(t, 10)), true
	case uint64:
		return Number(strconv.FormatUint(t, 10)), true
	}
	return "", false
}

// numberParts splits a JSON number literal into the sign, the significant
// digits and the power of ten the last of those digits stands for: the value is
// (neg ? -1 : 1) * digits * 10^scale.
//
// Both ends of the digit run are trimmed, and that is what makes this a
// canonical form rather than a reading. Leading zeros carry no value at all;
// trailing ones move into the scale, so 1.50 and 1.5 come out as the same two
// fields and 100 and 1e2 as the same one. A zero of any spelling -- 0, -0.0,
// 0e100 -- has no digits left, and its sign goes with them, because JSON Schema
// has no -0 distinct from 0.
//
// The exponent is parsed rather than clamped. A clamp would put two genuinely
// different numbers on the same canonical text and so make a const accept a
// value it forbids, which is the defect this whole file exists to close; a
// literal whose exponent will not fit in an int64 is refused instead, and the
// caller falls back.
func (n Number) numberParts() (neg bool, digits string, scale int64, ok bool) {
	s := string(n)
	if s == "" {
		return false, "", 0, true
	}
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		neg = s[i] == '-'
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	intPart := s[start:i]
	fracPart := ""
	if i < len(s) && s[i] == '.' {
		i++
		start = i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		fracPart = s[start:i]
	}
	if intPart == "" && fracPart == "" {
		return false, "", 0, false
	}
	exp := int64(0)
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		start = i
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		digitsStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if digitsStart == i {
			return false, "", 0, false
		}
		e, err := strconv.ParseInt(s[start:i], 10, 64)
		if err != nil {
			return false, "", 0, false
		}
		// Bounded well below the int64 range so that the scale arithmetic
		// below -- subtracting the fraction's length, then walking upwards
		// once per trailing zero, then adding the digit count back -- cannot
		// wrap. A literal past this is refused rather than clamped, for the
		// reason the doc comment gives.
		if e > canonicalExponentLimit || e < -canonicalExponentLimit {
			return false, "", 0, false
		}
		exp = e
	}
	if i != len(s) {
		return false, "", 0, false
	}
	digits = intPart + fracPart
	scale = exp - int64(len(fracPart))
	for len(digits) > 0 && digits[0] == '0' {
		digits = digits[1:]
	}
	for len(digits) > 0 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
		scale++
	}
	if digits == "" {
		return false, "", 0, true
	}
	return neg, digits, scale, true
}

// canonicalExponentLimit bounds the exponent a literal may state. It is far
// past anything a document means -- float64 stops at 308 -- and far short of
// where the scale arithmetic in numberParts could wrap an int64.
const canonicalExponentLimit = 1 << 40

// canonicalPlainFormLimit and canonicalSmallFormLimit are where the canonical
// text stops writing a number out in full and starts using an exponent.
//
// They are encoding/json's own thresholds, and copying them is the point: a
// number float64 holds exactly must canonicalise to the bytes encoding/json
// would have written for it, or every enum and const that was being compared
// correctly through a float64 would move. json's floatEncoder picks 'e' format
// when the magnitude is below 1e-6 or at or above 1e21, and those two are the
// same rule ECMAScript's Number-to-String gives, which is why the same
// thresholds also describe what a JavaScript implementation would write.
const (
	canonicalPlainFormLimit = 21
	canonicalSmallFormLimit = -6
)

// CanonicalText returns the number in the one spelling every mathematically
// equal literal shares, and reports whether the literal could be read.
//
// 1, 1.0, 1e0 and 0.1e1 all answer "1"; 1.50 and 1.5 both answer "1.5"; every
// spelling of zero answers "0".
func (n Number) CanonicalText() (string, bool) {
	neg, digits, scale, ok := n.numberParts()
	if !ok {
		return "", false
	}
	if digits == "" {
		return "0", true
	}
	k := int64(len(digits))
	// The decimal point sits after this many of the digits, counting from the
	// left; a value at or below zero means the number starts with "0.".
	point := scale + k

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	switch {
	case k <= point && point <= canonicalPlainFormLimit:
		b.WriteString(digits)
		b.WriteString(strings.Repeat("0", int(point-k)))
	case 0 < point && point <= canonicalPlainFormLimit:
		b.WriteString(digits[:int(point)])
		b.WriteByte('.')
		b.WriteString(digits[int(point):])
	case canonicalSmallFormLimit < point && point <= 0:
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", int(-point)))
		b.WriteString(digits)
	default:
		b.WriteString(digits[:1])
		if k > 1 {
			b.WriteByte('.')
			b.WriteString(digits[1:])
		}
		e := point - 1
		if e >= 0 {
			b.WriteString("e+")
		} else {
			b.WriteString("e-")
			e = -e
		}
		b.WriteString(strconv.FormatInt(e, 10))
	}
	return b.String(), true
}
