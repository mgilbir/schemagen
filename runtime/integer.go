package runtime

import (
	"encoding/json"
	"strconv"
	"strings"
)

// jsonInteger is an int64 that decodes a JSON number the way JSON Schema draft
// 6 and later define "integer": a number with a zero fractional part is an
// integer however it happens to be written, so 1.0 and 1e2 name the same
// integers that 1 and 100 do. encoding/json refuses those for an int64
// destination, and the destination type is the only thing it decides from --
// which is why a named integer type accepted them while an int64 struct field
// rejected them, for one and the same schema.
//
// A JSON string is refused explicitly: json.Number is a string type underneath
// and would otherwise take "1".
type jsonInteger = Integer

// Integer is jsonInteger, under the name generated code refers to it by.
type Integer int64

// SchemagenGenerated marks Integer as a type whose UnmarshalJSON may be handed
// bytes already checked to be one JSON value. See jsonGenerated.
func (*Integer) SchemagenGenerated() {}

// UnmarshalJSON reads a JSON number with a zero fractional part as an integer
// however it is written, and refuses a JSON string. See jsonInteger.
func (j *Integer) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		// In the schema's own words, and not in the destination type's, because
		// this is the one refusal at a scalar position encoding/json does not
		// raise itself: a type that answers for its own decode is handed the
		// bytes, so its error is the whole message. Saying `Go value of type
		// int64` here is what left an integer the odd one out among the scalars.
		// See jsonDecodeRefusal and issue #282.
		return jsonValueErrorf("expected integer, got string")
	}
	var _n json.Number
	if _err := json.Unmarshal(data, &_n); _err != nil {
		return _err
	}
	if _i, _iErr := _n.Int64(); _iErr == nil {
		*j = jsonInteger(_i)
		return nil
	}
	// Float notation with nothing after the point (1.0, 1e2), read exactly.
	if _i, _iOK := jsonIntegerFromLiteral(_n.String()); _iOK {
		*j = jsonInteger(_i)
		return nil
	}
	// In the schema's words: the two ways a number fails here -- a fractional
	// part, and a magnitude no int64 holds -- are one sentence to a caller who
	// asked for an integer, and int64 is a fact about this program rather than
	// about their document. The same sentence is what jsonDecodeRefusal writes
	// where encoding/json raised the refusal instead. Issue #282.
	return jsonValueErrorf("value %s cannot be held as an integer", _n.String())
}

// jsonIntegerFromLiteral reads a JSON number literal as an int64, exactly.
//
// It is decimal arithmetic on the digits, not a parse into float64 and a
// conversion back. float64 cannot be asked this question: it holds 2^63 and
// 2^63-1 as the same number, so a guard written over it either accepts
// 9223372036854775808 -- which no int64 holds, and which int64(float64) turns
// into its own negation -- or rejects 9223372036854775807, which is MaxInt64
// written out. Both were true here in turn, and only the first was visible.
//
// Zero fractional part is what makes a number an integer from draft 6 on, so
// 1.0, 1e2 and 100 all answer, and 1.5 does not. A magnitude no int64 holds
// answers false rather than a wrapped value.
func jsonIntegerFromLiteral(s string) (int64, bool) {
	_mant, _exp := s, 0
	if _i := strings.IndexAny(s, "eE"); _i >= 0 {
		_mant = s[:_i]
		_e, _err := strconv.Atoi(s[_i+1:])
		if _err != nil {
			// An exponent this large names no int64, unless the mantissa is
			// zero -- which is decided below, without building the number.
			_exp = 0
			_mant = strings.TrimLeft(_mant, "+-")
			if strings.Trim(strings.Replace(_mant, ".", "", 1), "0") == "" {
				return 0, true
			}
			return 0, false
		}
		_exp = _e
	}
	_neg := false
	if len(_mant) > 0 && (_mant[0] == '-' || _mant[0] == '+') {
		_neg = _mant[0] == '-'
		_mant = _mant[1:]
	}
	_digits := _mant
	if _i := strings.IndexByte(_mant, '.'); _i >= 0 {
		_digits = _mant[:_i] + _mant[_i+1:]
		_exp -= len(_mant) - _i - 1
	}
	if _digits == "" {
		return 0, false
	}
	for _i := 0; _i < len(_digits); _i++ {
		if _digits[_i] < '0' || _digits[_i] > '9' {
			return 0, false
		}
	}
	if strings.Trim(_digits, "0") == "" {
		return 0, true
	}
	switch {
	case _exp < 0:
		// The dropped digits are fractional: the value is an integer only when
		// every one of them is zero.
		_cut := len(_digits) + _exp
		if _cut < 0 {
			return 0, false
		}
		if strings.Trim(_digits[_cut:], "0") != "" {
			return 0, false
		}
		_digits = _digits[:_cut]
	case _exp > 0:
		// 10^19 is already past int64, so a shift that large cannot land inside
		// it -- the mantissa is known non-zero by here. Refusing before padding
		// also keeps a hostile 1e999999999 from allocating the zeros.
		if _exp > 19 {
			return 0, false
		}
		_digits += strings.Repeat("0", _exp)
	}
	if _neg {
		_digits = "-" + _digits
	}
	_v, _err := strconv.ParseInt(_digits, 10, 64)
	return _v, _err == nil
}

// jsonIntegerSlice, jsonIntegerMap and jsonIntegerPtr rebuild a container of
// jsonInteger as the container of int64 that was declared. A conversion cannot
// do it -- Go requires identical element types, and these differ in exactly the
// element -- so the value is copied through. Each preserves nil rather than
// allocating, which is what keeps an absent property, a JSON null and an empty
// collection as distinguishable after the rebuild as encoding/json left them.
func jsonIntegerSlice[E any, T any](s []E, f func(E) T) []T {
	if s == nil {
		return nil
	}
	out := make([]T, len(s))
	for i, v := range s {
		out[i] = f(v)
	}
	return out
}

func jsonIntegerMap[E any, T any](m map[string]E, f func(E) T) map[string]T {
	if m == nil {
		return nil
	}
	out := make(map[string]T, len(m))
	// maporder: copies members under their own keys, which are distinct.
	for k, v := range m {
		out[k] = f(v)
	}
	return out
}

func jsonIntegerPtr[E any, T any](p *E, f func(E) T) *T {
	if p == nil {
		return nil
	}
	v := f(*p)
	return &v
}
