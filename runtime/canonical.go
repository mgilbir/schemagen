package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// _jsonCanonical returns the one text that every JSON document equal to this
// one reduces to, so that "enum" and "const" can be decided by comparing two
// strings.
//
// Equality here is JSON equality, which is what those keywords are defined
// over and is not equality of the bytes: {"a":1,"b":2} and {"b":2,"a":1} are
// one document, and so are 1, 1.0 and 1e0. Member order goes by sorting the
// keys, and number spelling goes by _jsonCanonicalNumber.
//
// What this replaces was a decode into `any` followed by a re-encode, which
// settled both of those and one thing more that it should not have: a JSON
// number decoded into `any` becomes a float64, so every integer that rounds to
// one float64 came back as the same text. {"const":123456789012345678901234567890}
// accepted ...891, ...889 and every other neighbour inside that rounding, and
// the const list it was compared against had been folded through the same
// float64 on the way in. Decoding with UseNumber keeps the literal, and the
// canonical form is computed from its digits. See issue #272.
//
// An error is returned only for input that is not one JSON value; that is the
// same answer json.Unmarshal gave, and the callers report it the same way.
func _jsonCanonical(data []byte) (string, error) {
	if !json.Valid(data) {
		return "", fmt.Errorf("invalid JSON value: %s", _schemagenClipText(string(data)))
	}
	_dec := json.NewDecoder(bytes.NewReader(data))
	_dec.UseNumber()
	var _v any
	if _err := _dec.Decode(&_v); _err != nil {
		return "", _err
	}
	var _b strings.Builder
	_jsonCanonicalValue(&_b, _v)
	return _b.String(), nil
}

func _jsonCanonicalValue(b *strings.Builder, v any) {
	switch _t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if _t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		b.WriteString(_jsonCanonicalNumber(string(_t)))
	case string:
		if _enc, _err := json.Marshal(_t); _err == nil {
			b.Write(_enc)
		}
	case []any:
		b.WriteByte('[')
		for _i, _e := range _t {
			if _i > 0 {
				b.WriteByte(',')
			}
			_jsonCanonicalValue(b, _e)
		}
		b.WriteByte(']')
	case map[string]any:
		_keys := make([]string, 0, len(_t))
		for _k := range _t {
			_keys = append(_keys, _k)
		}
		sort.Strings(_keys)
		b.WriteByte('{')
		for _i, _k := range _keys {
			if _i > 0 {
				b.WriteByte(',')
			}
			if _enc, _err := json.Marshal(_k); _err == nil {
				b.Write(_enc)
			}
			b.WriteByte(':')
			_jsonCanonicalValue(b, _t[_k])
		}
		b.WriteByte('}')
	default:
		// The six arms above are every kind encoding/json produces when it
		// decodes into `any` with UseNumber, and this function is called on
		// nothing else -- so this arm is the exhaustiveness guard Go does not
		// give a type switch, not a case anything reaches. It is written to
		// degrade rather than to drop the value: a seventh kind that wrote
		// nothing would reduce two different documents to the same text, which
		// is the failure the whole reduction exists to prevent.
		if _enc, _err := json.Marshal(_t); _err == nil {
			b.Write(_enc)
		}
	}
}

// _jsonCanonicalNumber returns a JSON number literal in the one spelling every
// mathematically equal literal shares.
//
// The literal is read by jsonNumberParts, the reader every numeric keyword is
// decided through, so this reduction and the comparisons cannot disagree about
// which literals name one number: two literals canonicalise to one text exactly
// when jsonDecimalCmp calls them equal. Both ends of the digit run are trimmed,
// which is what makes the form canonical -- 1.50 and 1.5 come out identical, and
// so do 100 and 1e2. Every spelling of zero -- 0, -0.0, 0e100 -- comes out "0",
// sign included, because JSON Schema has no -0 distinct from 0.
//
// Where it puts the decimal point is encoding/json's own rule, and copying it
// is deliberate: json writes a float in exponent form below 1e-6 and at or
// above 1e21 and in full in between, so a number float64 holds exactly
// canonicalises to the bytes json would have written for it. That is what keeps
// every enum and const that was already being compared correctly exactly where
// it was.
//
// An exponent of any size is read. This used to refuse one past 2^40 and fall
// back to the literal as written, which kept 1e2000000000000 and
// 10e1999999999999 apart -- one number, two texts. The scale is a big.Int there,
// and the text is written from it.
//
// A literal this cannot read comes back unchanged. Nothing a decode produces
// can take that arm -- encoding/json only fills a json.Number from a number
// token -- so it takes a hand-assembled value to reach, and the value then
// compares equal to itself and to nothing else.
func _jsonCanonicalNumber(s string) string {
	_d, _ok := jsonNumberParts(s)
	if !_ok {
		return s
	}
	if _d.isZero() {
		return "0"
	}
	_digits := _d.hi + _d.lo
	_k := int64(len(_digits))
	var _b strings.Builder
	if _d.neg {
		_b.WriteByte('-')
	}
	if _d.bigScale != nil {
		// The decimal point sits a big.Int away from the digits, which is
		// always outside the range the plain forms below cover: an exponent
		// that took a big.Int to hold cannot be brought back under 21 by a
		// literal that fits in memory. So the exponent form, written from the
		// big.Int.
		_e := new(big.Int).Add(_d.bigScale, big.NewInt(_k-1))
		_b.WriteString(_digits[:1])
		if _k > 1 {
			_b.WriteByte('.')
			_b.WriteString(_digits[1:])
		}
		if _e.Sign() >= 0 {
			_b.WriteString("e+")
		} else {
			_b.WriteString("e-")
			_e.Neg(_e)
		}
		_b.WriteString(_e.String())
		return _b.String()
	}
	// The decimal point sits after this many digits, counting from the left; at
	// or below zero the number is written "0.something".
	_point := _d.scale + _k
	switch {
	case _k <= _point && _point <= 21:
		_b.WriteString(_digits)
		_b.WriteString(strings.Repeat("0", int(_point-_k)))
	case 0 < _point && _point <= 21:
		_b.WriteString(_digits[:int(_point)])
		_b.WriteByte('.')
		_b.WriteString(_digits[int(_point):])
	case -6 < _point && _point <= 0:
		_b.WriteString("0.")
		_b.WriteString(strings.Repeat("0", int(-_point)))
		_b.WriteString(_digits)
	default:
		_b.WriteString(_digits[:1])
		if _k > 1 {
			_b.WriteByte('.')
			_b.WriteString(_digits[1:])
		}
		_e := _point - 1
		if _e >= 0 {
			_b.WriteString("e+")
		} else {
			_b.WriteString("e-")
			_e = -_e
		}
		_b.WriteString(strconv.FormatInt(_e, 10))
	}
	return _b.String()
}
