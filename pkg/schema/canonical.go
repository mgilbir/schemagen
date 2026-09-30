package schema

import (
	"encoding/json"
	"math/big"
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
// False is the answer for anything else -- a Go type no decode produces, or a
// Number that is not a JSON number -- which leaves the caller to fall back on
// whatever it did before rather than inventing an answer.
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

// decimal is a JSON number literal read as the exact decimal it names: the
// value is (neg ? -1 : 1) * digits * 10^scale.
//
// Both ends of the digit run are trimmed, and that is what makes this a
// canonical form rather than a reading. Leading zeros carry no value at all;
// trailing ones move into the scale, so 1.50 and 1.5 come out as the same two
// fields and 100 and 1e2 as the same one. A zero of any spelling -- 0, -0.0,
// 0e100 -- has no digits left, and its sign goes with them, because JSON Schema
// has no -0 distinct from 0.
//
// It is the generator's copy of jsonNumberParts, the reader the generated code
// decides every numeric keyword through; the generated code is standalone and
// cannot import this one. tests/canonical_agreement_test.go holds the two to
// one answer.
type decimal struct {
	digits string
	// scale is the power of ten, when the exponent the literal wrote has at
	// most decimalExponentDigits digits; bigScale holds it past that.
	scale    int64
	bigScale *big.Int
	neg      bool
}

// decimalExponentDigits is the widest exponent, in digits, held in an int64.
// The arithmetic on a scale adds and subtracts digit counts, which no literal
// in memory can make large enough to carry an int64 of fifteen digits past its
// range.
const decimalExponentDigits = 15

// numberParts reads the literal as a decimal, and reports whether it is a JSON
// number at all.
//
// The exponent is read whatever its size. It used to be refused past 2^40 and
// the literal kept as written, which put 1e2000000000000 and 10e1999999999999
// -- one number -- on two canonical texts. A clamp would have been worse, two
// numbers on one text, and a const accepting a value it forbids; holding the
// scale as a big.Int is neither, and costs arithmetic on a number as long as
// the exponent the document wrote.
func (n Number) numberParts() (decimal, bool) {
	s := string(n)
	var d decimal
	if s == "" {
		return d, true
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		d.neg = s[0] == '-'
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
		return decimal{}, false
	}
	var exp int64
	var bigExp *big.Int
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		eNeg := false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			eNeg = s[i] == '-'
			i++
		}
		start = i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if start == i {
			return decimal{}, false
		}
		eDigits := strings.TrimLeft(s[start:i], "0")
		if eDigits == "" {
			eDigits = "0"
		}
		if len(eDigits) <= decimalExponentDigits {
			exp, _ = strconv.ParseInt(eDigits, 10, 64)
			if eNeg {
				exp = -exp
			}
		} else {
			bigExp, _ = new(big.Int).SetString(eDigits, 10)
			if eNeg {
				bigExp.Neg(bigExp)
			}
		}
	}
	if i != len(s) {
		return decimal{}, false
	}
	digits := intPart + fracPart
	scale := -int64(len(fracPart))
	for len(digits) > 0 && digits[0] == '0' {
		digits = digits[1:]
	}
	for len(digits) > 0 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
		scale++
	}
	if digits == "" {
		return decimal{}, true
	}
	d.digits = digits
	if bigExp != nil {
		d.bigScale = bigExp.Add(bigExp, big.NewInt(scale))
	} else {
		d.scale = exp + scale
	}
	return d, true
}

// Decimal reads the number as digits*10^scale, with digits free of leading
// and trailing zeros -- 1.50, 15e-1 and 0.15e1 all answer "15", -1 -- and
// reports whether it could: false for a literal that is not a JSON number, and
// for one whose exponent is too wide for an int64 to carry the scale. Zero
// answers no digits. It reads the literal once, in time linear in its length,
// which is what a question about a divisor's or bound's shape wants before it
// builds a big.Rat, whose parse is quadratic in the digits.
func (n Number) Decimal() (digits string, scale int64, neg, ok bool) {
	d, ok := n.numberParts()
	if !ok || d.bigScale != nil {
		return "", 0, false, false
	}
	return d.digits, d.scale, d.neg, true
}

// scaleBig is the scale as a big.Int.
func (d decimal) scaleBig() *big.Int {
	if d.bigScale != nil {
		return new(big.Int).Set(d.bigScale)
	}
	return big.NewInt(d.scale)
}

// Compare compares two numbers by value, answering -1, 0 or 1, and reports
// whether both are JSON numbers. It reads the digits and never a float64, so it
// is exact for every literal the grammar allows, whatever its exponent.
func (n Number) Compare(other Number) (int, bool) {
	a, okA := n.numberParts()
	b, okB := other.numberParts()
	if !okA || !okB {
		return 0, false
	}
	switch {
	case a.digits == "" && b.digits == "":
		return 0, true
	case a.digits == "":
		if b.neg {
			return 1, true
		}
		return -1, true
	case b.digits == "":
		if a.neg {
			return -1, true
		}
		return 1, true
	}
	if a.neg != b.neg {
		if a.neg {
			return -1, true
		}
		return 1, true
	}
	c := 0
	if a.bigScale == nil && b.bigScale == nil {
		aLead := a.scale + int64(len(a.digits))
		bLead := b.scale + int64(len(b.digits))
		switch {
		case aLead < bLead:
			c = -1
		case aLead > bLead:
			c = 1
		}
	} else {
		aLead := a.scaleBig()
		aLead.Add(aLead, big.NewInt(int64(len(a.digits))))
		bLead := b.scaleBig()
		bLead.Add(bLead, big.NewInt(int64(len(b.digits))))
		c = aLead.Cmp(bLead)
	}
	if c == 0 {
		for k := 0; k < len(a.digits) || k < len(b.digits); k++ {
			x, y := byte('0'), byte('0')
			if k < len(a.digits) {
				x = a.digits[k]
			}
			if k < len(b.digits) {
				y = b.digits[k]
			}
			if x != y {
				if x < y {
					c = -1
				} else {
					c = 1
				}
				break
			}
		}
	}
	if a.neg {
		c = -c
	}
	return c, true
}

// IsInteger reports whether the number has a zero fractional part, and
// whether it is a JSON number at all. 1.0, 1e2 and 1e99999999999999999999 are
// integers; 1.5 and 1e-1 are not.
func (n Number) IsInteger() (bool, bool) {
	d, ok := n.numberParts()
	if !ok {
		return false, false
	}
	if d.digits == "" {
		return true, true
	}
	if d.bigScale != nil {
		return d.bigScale.Sign() >= 0, true
	}
	return d.scale >= 0, true
}

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
	d, ok := n.numberParts()
	if !ok {
		return "", false
	}
	digits := d.digits
	if digits == "" {
		return "0", true
	}
	k := int64(len(digits))
	var b strings.Builder
	if d.neg {
		b.WriteByte('-')
	}
	if d.bigScale != nil {
		// An exponent that took a big.Int to hold puts the decimal point
		// further from the digits than a literal that fits in memory can bring
		// back into the plain forms, so the text is the exponent form, written
		// from the big.Int.
		e := new(big.Int).Add(d.bigScale, big.NewInt(k-1))
		b.WriteString(digits[:1])
		if k > 1 {
			b.WriteByte('.')
			b.WriteString(digits[1:])
		}
		if e.Sign() >= 0 {
			b.WriteString("e+")
		} else {
			b.WriteString("e-")
			e.Neg(e)
		}
		b.WriteString(e.String())
		return b.String(), true
	}
	// The decimal point sits after this many of the digits, counting from the
	// left; a value at or below zero means the number starts with "0.".
	point := d.scale + k

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
