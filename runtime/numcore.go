package runtime

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"math/bits"
	"reflect"
	"strconv"
)

// jsonDecimal is a JSON number literal read as the exact decimal it names: a
// sign, a run of significant digits, and the power of ten the last of those
// digits stands for.
//
// The digit run is held as the two pieces of the literal it came from -- what
// stood before the point and what stood after it -- rather than joined, so that
// reading a literal allocates nothing. Both ends of the run are trimmed: a
// leading zero carries no value and a trailing one moves into the scale. That is
// what makes two spellings of one number -- 1.50 and 1.5, 100 and 1e2, 0.1 and
// 1e-1 -- come out as the same fields, and what lets multipleOf rely on neither
// run being divisible by ten.
//
// The scale is an int64 wherever the literal's exponent has at most fifteen
// digits, and a big.Int past that. The JSON grammar puts no bound on an
// exponent, and 1e99999999999999999999 is a legal document; saturating it, as
// this reader once did, made it compare equal to 1e99999999999999999998. Held
// as a big.Int it costs arithmetic on a number as long as the exponent the
// document wrote, which is still a cost set by the literal's length.
type jsonDecimal struct {
	bigScale *big.Int // the scale, when the exponent written does not fit scale
	hi, lo   string   // the significant digits are hi followed by lo
	scale    int64    // value = digits * 10^scale, when bigScale is nil
	neg      bool     // false for every zero
}

func (d *jsonDecimal) width() int { return len(d.hi) + len(d.lo) }

func (d *jsonDecimal) digit(i int) byte {
	if i < len(d.hi) {
		return d.hi[i]
	}
	return d.lo[i-len(d.hi)]
}

// isZero reports a zero of any spelling -- 0, -0.0, 0e100 -- which has no
// digits left once they are trimmed. Its sign goes with them: JSON Schema has
// no -0 distinct from 0, and {"minimum":0} admits -0.0.
func (d *jsonDecimal) isZero() bool { return len(d.hi) == 0 && len(d.lo) == 0 }

// scaleBig is the scale as a big.Int, for the arithmetic a huge exponent takes
// out of int64.
func (d *jsonDecimal) scaleBig() *big.Int {
	if d.bigScale != nil {
		return new(big.Int).Set(d.bigScale)
	}
	return big.NewInt(d.scale)
}

// jsonExponentDigits is the widest exponent, in digits, held in an int64. The
// arithmetic on a scale adds and subtracts digit counts, which a literal in
// memory cannot make large enough to carry an int64 of fifteen digits past its
// range.
const jsonExponentDigits = 15

// jsonNumberParts reads a JSON number literal as the decimal it names. ok is
// false for a string that is not a JSON number.
//
// Nothing a decode produces can take that arm -- a number is only ever held
// from a number token -- so it takes a value assembled by hand, which
// json.Number's own encoder refuses to write in turn. The empty string is
// json.Number's zero value, which encoding/json writes as 0, and reads as 0
// here.
func jsonNumberParts(s string) (jsonDecimal, bool) {
	var _d jsonDecimal
	if s == "" {
		return _d, true
	}
	_i := 0
	if s[0] == '-' || s[0] == '+' {
		_d.neg = s[0] == '-'
		_i++
	}
	_intStart := _i
	for _i < len(s) && s[_i] >= '0' && s[_i] <= '9' {
		_i++
	}
	_int := s[_intStart:_i]
	_frac := ""
	if _i < len(s) && s[_i] == '.' {
		_i++
		_fracStart := _i
		for _i < len(s) && s[_i] >= '0' && s[_i] <= '9' {
			_i++
		}
		_frac = s[_fracStart:_i]
	}
	if _int == "" && _frac == "" {
		return jsonDecimal{}, false
	}
	var _exp int64
	var _bigExp *big.Int
	if _i < len(s) && (s[_i] == 'e' || s[_i] == 'E') {
		_i++
		_eNeg := false
		if _i < len(s) && (s[_i] == '+' || s[_i] == '-') {
			_eNeg = s[_i] == '-'
			_i++
		}
		_eStart := _i
		for _i < len(s) && s[_i] >= '0' && s[_i] <= '9' {
			_i++
		}
		if _eStart == _i {
			return jsonDecimal{}, false
		}
		_eDigits := s[_eStart:_i]
		for len(_eDigits) > 1 && _eDigits[0] == '0' {
			_eDigits = _eDigits[1:]
		}
		if len(_eDigits) <= jsonExponentDigits {
			_exp, _ = strconv.ParseInt(_eDigits, 10, 64)
			if _eNeg {
				_exp = -_exp
			}
		} else {
			_bigExp, _ = new(big.Int).SetString(_eDigits, 10)
			if _eNeg {
				_bigExp.Neg(_bigExp)
			}
		}
	}
	if _i != len(s) {
		return jsonDecimal{}, false
	}
	_scale := -int64(len(_frac))
	for len(_int) > 0 && _int[0] == '0' {
		_int = _int[1:]
	}
	if _int == "" {
		for len(_frac) > 0 && _frac[0] == '0' {
			_frac = _frac[1:]
		}
	}
	for len(_frac) > 0 && _frac[len(_frac)-1] == '0' {
		_frac = _frac[:len(_frac)-1]
		_scale++
	}
	if _frac == "" {
		for len(_int) > 0 && _int[len(_int)-1] == '0' {
			_int = _int[:len(_int)-1]
			_scale++
		}
	}
	if _int == "" && _frac == "" {
		return jsonDecimal{}, true
	}
	_d.hi, _d.lo = _int, _frac
	if _bigExp != nil {
		_d.bigScale = _bigExp.Add(_bigExp, big.NewInt(_scale))
	} else {
		_d.scale = _exp + _scale
	}
	return _d, true
}

// jsonRawNumber is the number literal a JSON text holds, when it holds one.
func jsonRawNumber(b []byte) (string, bool) {
	_lo, _hi := 0, len(b)
	for _lo < _hi && (b[_lo] == ' ' || b[_lo] == '\t' || b[_lo] == '\n' || b[_lo] == '\r') {
		_lo++
	}
	for _hi > _lo && (b[_hi-1] == ' ' || b[_hi-1] == '\t' || b[_hi-1] == '\n' || b[_hi-1] == '\r') {
		_hi--
	}
	if _lo == _hi || (b[_lo] != '-' && (b[_lo] < '0' || b[_lo] > '9')) {
		return "", false
	}
	_s := string(b[_lo:_hi])
	if _, _ok := jsonNumberParts(_s); !_ok {
		return "", false
	}
	return _s, true
}

// jsonNumberOf returns the decimal literal of a number, in whichever of the Go
// types generated code holds a number in, and reports whether the value is a
// number at all.
//
// The literal is the number the value stands for in JSON. For a json.Number or
// a json.RawMessage that is the text the document wrote; for a type with a
// MarshalJSON of its own -- the --big-int wrapper, a named json.Number -- it is
// what that writes; for an integer kind it is the integer.
//
// For a float64 it is the shortest decimal that reads back as the same float64,
// which is the number encoding/json writes for it. That is a choice, and the
// only coherent one: a float64 does not remember the literal it was decoded
// from, and of the numbers it could stand for, the one it marshals to is the
// one any document built from it carries. So a float64 holding 0.3 is judged as
// 0.3 -- a multiple of 0.1, as the document that wrote it said -- and not as the
// binary fraction it holds, which is not. A document number float64 cannot hold
// was changed when it was decoded into one, before any keyword saw it; a
// position that must judge every digit has to hold the literal instead, which is
// what --exact-numbers and --raw-untyped are for.
//
// An infinity and a NaN are not numbers JSON can write, and answer false.
func jsonNumberOf(v any) (string, bool) {
	switch _n := v.(type) {
	case json.Number:
		if _n == "" {
			return "0", true
		}
		if _, _ok := jsonNumberParts(string(_n)); !_ok {
			return "", false
		}
		return string(_n), true
	case float64:
		if math.IsInf(_n, 0) || math.IsNaN(_n) {
			return "", false
		}
		return strconv.FormatFloat(_n, 'g', -1, 64), true
	case float32:
		if math.IsInf(float64(_n), 0) || math.IsNaN(float64(_n)) {
			return "", false
		}
		return strconv.FormatFloat(float64(_n), 'g', -1, 32), true
	case int:
		return strconv.FormatInt(int64(_n), 10), true
	case int8:
		return strconv.FormatInt(int64(_n), 10), true
	case int16:
		return strconv.FormatInt(int64(_n), 10), true
	case int32:
		return strconv.FormatInt(int64(_n), 10), true
	case int64:
		return strconv.FormatInt(_n, 10), true
	case uint:
		return strconv.FormatUint(uint64(_n), 10), true
	case uint8:
		return strconv.FormatUint(uint64(_n), 10), true
	case uint16:
		return strconv.FormatUint(uint64(_n), 10), true
	case uint32:
		return strconv.FormatUint(uint64(_n), 10), true
	case uint64:
		return strconv.FormatUint(_n, 10), true
	case json.RawMessage:
		return jsonRawNumber(_n)
	case jsonLazy:
		// A value read lazily from a document: the literal the document wrote
		// where the value is exact, and otherwise the float64 it is read as,
		// which is the number the value stands for there (see jsonLazy.exact).
		_lit, _ok := jsonRawNumber(_n.d.raw(_n.sp))
		if !_ok || _n.exact {
			return _lit, _ok
		}
		_f, _err := strconv.ParseFloat(_lit, 64)
		if _err != nil {
			return "", false
		}
		return jsonNumberOf(_f)
	case nil, bool, string, []any, map[string]any:
		return "", false
	case json.Marshaler:
		_b, _err := _n.MarshalJSON()
		if _err != nil {
			return "", false
		}
		return jsonRawNumber(_b)
	}
	// A named type over a Go number kind: a $defs alias, an enum. Its kind is
	// what it is held as, so the same readings apply.
	_rv := reflect.ValueOf(v)
	switch _rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(_rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(_rv.Uint(), 10), true
	case reflect.Float32:
		return jsonNumberOf(float32(_rv.Float()))
	case reflect.Float64:
		return jsonNumberOf(_rv.Float())
	case reflect.Pointer:
		if _rv.IsNil() {
			return "", false
		}
		return jsonNumberOf(_rv.Elem().Interface())
	}
	return "", false
}

// jsonIsNumber reports whether a value is a JSON number, in any of the Go types
// one is held in.
func jsonIsNumber(v any) bool {
	_, _ok := jsonNumberOf(v)
	return _ok
}

// jsonDecimalCmp compares two JSON number literals exactly, answering -1, 0 or
// 1 as the first is less than, equal to or greater than the second.
//
// Exactly is the point: a comparison made in float64 gives the value and the
// bound one reading between them wherever they differ past the 53rd bit, so
// {"maximum":9007199254740992} admitted 9007199254740993. It is also what makes
// 1.50 equal to 1.5 and 1e2 equal to 100, which comparing the literals as text
// would not.
//
// A literal either side cannot read compares equal, so no check fires on it.
// That costs nothing a decode can produce; see jsonNumberParts.
func jsonDecimalCmp(a, b string) int {
	_a, _aOK := jsonNumberParts(a)
	_b, _bOK := jsonNumberParts(b)
	if !_aOK || !_bOK {
		return 0
	}
	return jsonDecimalOrder(&_a, &_b)
}

// jsonDecimalOrder is jsonDecimalCmp over two literals already read.
func jsonDecimalOrder(a, b *jsonDecimal) int {
	if a.isZero() || b.isZero() {
		switch {
		case a.isZero() && b.isZero():
			return 0
		case a.isZero():
			if b.neg {
				return 1
			}
			return -1
		default:
			if a.neg {
				return -1
			}
			return 1
		}
	}
	if a.neg != b.neg {
		if a.neg {
			return -1
		}
		return 1
	}
	// The position of the leading digit settles it whenever the two differ: a
	// number with more digits before the point is the larger one, whatever
	// those digits are. Both runs are free of leading and trailing zeros, so
	// the position is the scale plus the digit count.
	_c := 0
	if a.bigScale == nil && b.bigScale == nil {
		_aLead := a.scale + int64(a.width())
		_bLead := b.scale + int64(b.width())
		switch {
		case _aLead < _bLead:
			_c = -1
		case _aLead > _bLead:
			_c = 1
		}
	} else {
		_aLead := a.scaleBig()
		_aLead.Add(_aLead, big.NewInt(int64(a.width())))
		_bLead := b.scaleBig()
		_bLead.Add(_bLead, big.NewInt(int64(b.width())))
		_c = _aLead.Cmp(_bLead)
	}
	if _c == 0 {
		// The same leading position: the digits decide, the shorter run read
		// as if padded with the zeros that were trimmed from it.
		_aw, _bw := a.width(), b.width()
		for _k := 0; _k < _aw || _k < _bw; _k++ {
			_x, _y := byte('0'), byte('0')
			if _k < _aw {
				_x = a.digit(_k)
			}
			if _k < _bw {
				_y = b.digit(_k)
			}
			if _x != _y {
				if _x < _y {
					_c = -1
				} else {
					_c = 1
				}
				break
			}
		}
	}
	if a.neg {
		return -_c
	}
	return _c
}

// jsonNumberCmp is jsonDecimalCmp for a number held as a json.Number, which is
// what --exact-numbers holds a "number" as.
func jsonNumberCmp(a json.Number, b string) int {
	return jsonDecimalCmp(string(a), b)
}

// jsonNumberOrder compares a number held in any representation against a
// literal, and reports whether the value is a number at all. A value that is
// not one satisfies every numeric keyword, which is what the callers below
// read the false as.
//
// An infinity is not a JSON number but it is ordered: it lies past every
// literal, so a bound refuses it rather than being satisfied by it.
func jsonNumberOrder(v any, b string) (int, bool) {
	switch _f := v.(type) {
	case float64:
		if math.IsInf(_f, 0) {
			if _f > 0 {
				return 1, true
			}
			return -1, true
		}
	case float32:
		if math.IsInf(float64(_f), 0) {
			if _f > 0 {
				return 1, true
			}
			return -1, true
		}
	}
	_lit, _ok := jsonNumberOf(v)
	if !_ok {
		return 0, false
	}
	return jsonDecimalCmp(_lit, b), true
}

// jsonNumberBelow, jsonNumberAbove, jsonNumberAtMost and jsonNumberAtLeast are
// the four orderings the bounds keywords are broken by -- minimum, maximum,
// exclusiveMinimum and exclusiveMaximum in that order -- and jsonNumberEqual is
// const. Each is false for a value that is not a number, so that a check
// written as "if broken, refuse" passes over a string or an object as the
// keyword says it must.
func jsonNumberBelow(v any, b string) bool {
	_c, _ok := jsonNumberOrder(v, b)
	return _ok && _c < 0
}

func jsonNumberAbove(v any, b string) bool {
	_c, _ok := jsonNumberOrder(v, b)
	return _ok && _c > 0
}

func jsonNumberAtMost(v any, b string) bool {
	_c, _ok := jsonNumberOrder(v, b)
	return _ok && _c <= 0
}

func jsonNumberAtLeast(v any, b string) bool {
	_c, _ok := jsonNumberOrder(v, b)
	return _ok && _c >= 0
}

func jsonNumberEqual(v any, b string) bool {
	_c, _ok := jsonNumberOrder(v, b)
	return _ok && _c == 0
}

// jsonDecimalIsIntegral reports whether a literal names an integer: a number
// with a zero fractional part, however it is written. 1.0, 1e2 and 1e308 all
// do; 1.5 and 1e-1 do not. This is "integer" from draft 6 on.
func jsonDecimalIsIntegral(s string) bool {
	_d, _ok := jsonNumberParts(s)
	if !_ok {
		return false
	}
	if _d.isZero() {
		return true
	}
	if _d.bigScale != nil {
		return _d.bigScale.Sign() >= 0
	}
	return _d.scale >= 0
}

// jsonDecimalIsIntegerToken reports whether a literal is written as an
// integer: without a fraction and without an exponent. Draft 3 and draft 4
// define "integer" by the token rather than by its value -- the official
// suite's zeroTerminatedFloats says 1.0 is not one there -- so this is what
// those drafts ask.
func jsonDecimalIsIntegerToken(s string) bool {
	if _, _ok := jsonNumberParts(s); !_ok {
		return false
	}
	for _i := 0; _i < len(s); _i++ {
		if s[_i] == '.' || s[_i] == 'e' || s[_i] == 'E' {
			return false
		}
	}
	return true
}

// jsonIsInteger reports whether a value is a number JSON Schema calls an
// integer: one with a zero fractional part from draft 6 on, and under strict --
// draft 3 and draft 4 -- one written without a fraction or an exponent.
//
// The token can only be read where it was kept. A float64 or an int64 has
// forgotten how the document wrote it, so strict is answered from the value for
// those, which is the answer every such position has always given.
func jsonIsInteger(v any, strict bool) bool {
	_lit, _ok := jsonNumberOf(v)
	if !_ok {
		return false
	}
	if strict && jsonHoldsLiteral(v) {
		return jsonDecimalIsIntegerToken(_lit)
	}
	return jsonDecimalIsIntegral(_lit)
}

// jsonHoldsLiteral reports whether a number is held as text a document wrote
// -- or, for a type with a MarshalJSON of its own, as the text it writes.
func jsonHoldsLiteral(v any) bool {
	switch v.(type) {
	case json.Number, json.RawMessage:
		return true
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return false
	case jsonLazy:
		// Read as the float64 it stands for where it is not exact, which has
		// no token left to read.
		return v.(jsonLazy).exact
	case json.Marshaler:
		return true
	}
	return false
}

// jsonRawKind names the JSON type a raw value is, for the checks that read a
// member from the document rather than from the Go value it was decoded into.
// A number is "integer" when jsonIsInteger says so under the same strictness,
// and "number" otherwise, so every arm that accepts "number" also accepts
// "integer".
func jsonRawKind(b []byte, strict bool) string {
	_lo := 0
	for _lo < len(b) && (b[_lo] == ' ' || b[_lo] == '\t' || b[_lo] == '\n' || b[_lo] == '\r') {
		_lo++
	}
	if _lo == len(b) {
		return "unknown"
	}
	switch b[_lo] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	}
	if _, _ok := jsonRawNumber(b); !_ok {
		return "unknown"
	}
	if jsonIsInteger(json.RawMessage(b), strict) {
		return "integer"
	}
	return "number"
}

// jsonNumberIsMultipleOf is jsonDecimalIsMultipleOf for a number held as a
// json.Number.
func jsonNumberIsMultipleOf(v json.Number, m string) bool {
	return jsonDecimalIsMultipleOf(string(v), m)
}

// jsonDecimalIsMultipleOf reports whether v divided by m is an integer, exactly.
//
// Both are decimals, so the question is whether one integer divides another
// once the scales are lined up: v is Dv*10^sv and m is Dm*10^sm, and v/m is
// (Dv/Dm)*10^k with k = sv-sm. The float64 quotient this replaces was compared
// against a tolerance of 1e-9, which called 1.0000000001 a multiple of 1 and
// 1e-10 one too, and called 4611686018427387905 a multiple of
// 4611686018427387904 because the two share a float64; the math.Mod it replaced
// elsewhere called 0.3 no multiple of 0.1, because neither is one in binary.
//
// Neither digit run ends in a zero -- jsonNumberParts moved those into the
// scale -- so neither is divisible by ten. A negative k asks Dm*10^-k to divide
// Dv, which asks ten to divide Dv, so it answers no without any arithmetic. A
// k of zero or more asks Dm to divide Dv*10^k, which holds exactly when
// (Dv mod Dm)*(10^k mod Dm) is a multiple of Dm -- and that is computed without
// building either Dv or 10^k: the first is reduced eighteen digits at a time,
// the second by modular exponentiation. The cost follows the length of the two
// literals, so a million-digit instance or an exponent of 10^20 is answered in
// the time it takes to read it.
//
// A multipleOf of zero divides nothing, and the keyword forbids it; the answer
// here is to accept, which is what every reading of it has given. Zero is a
// multiple of every number.
func jsonDecimalIsMultipleOf(v, m string) bool {
	_v, _vOK := jsonNumberParts(v)
	_m, _mOK := jsonNumberParts(m)
	if !_vOK || !_mOK || _m.isZero() || _v.isZero() {
		return true
	}
	var _k *big.Int
	_kSmall := int64(0)
	if _v.bigScale == nil && _m.bigScale == nil {
		_kSmall = _v.scale - _m.scale
		if _kSmall < 0 {
			return false
		}
	} else {
		_k = _v.scaleBig()
		_k.Sub(_k, _m.scaleBig())
		if _k.Sign() < 0 {
			return false
		}
	}
	if _m.width() <= 18 {
		_dm := jsonDigitsModUint(&_m, 0)
		_r := jsonDigitsModUint(&_v, _dm)
		if _r == 0 {
			return true
		}
		var _p uint64
		if _k == nil {
			_p = jsonPowModUint(10, uint64(_kSmall), _dm)
		} else {
			_p = new(big.Int).Exp(big.NewInt(10), _k, new(big.Int).SetUint64(_dm)).Uint64()
		}
		_hi, _lo := bits.Mul64(_r, _p)
		return bits.Rem64(_hi, _lo, _dm) == 0
	}
	_dm := jsonDigitsModBig(&_m, nil)
	_r := jsonDigitsModBig(&_v, _dm)
	if _r.Sign() == 0 {
		return true
	}
	if _k == nil {
		_k = big.NewInt(_kSmall)
	}
	_r.Mul(_r, new(big.Int).Exp(big.NewInt(10), _k, _dm))
	_r.Mod(_r, _dm)
	return _r.Sign() == 0
}

// jsonDigitsModUint is a decimal's digit run modulo m, read eighteen digits at
// a time so that no intermediate outgrows the 128 bits bits.Mul64 gives: the
// running remainder is below m and a chunk below 10^18, so their combination is
// below m*2^64 and bits.Rem64 can take it. An m of zero reads the run as the
// number it is, which is only asked of a run of eighteen digits or fewer.
func jsonDigitsModUint(d *jsonDecimal, m uint64) uint64 {
	var _r uint64
	_w := d.width()
	for _i := 0; _i < _w; {
		_n := _w - _i
		if _n > 18 {
			_n = 18
		}
		var _chunk uint64
		for _j := 0; _j < _n; _j++ {
			_chunk = _chunk*10 + uint64(d.digit(_i+_j)-'0')
		}
		if m == 0 {
			_r = _r*jsonPow10[_n] + _chunk
		} else {
			_hi, _lo := bits.Mul64(_r, jsonPow10[_n])
			var _carry uint64
			_lo, _carry = bits.Add64(_lo, _chunk, 0)
			_r = bits.Rem64(_hi+_carry, _lo, m)
		}
		_i += _n
	}
	return _r
}

// jsonDigitsModBig is jsonDigitsModUint for a divisor past uint64, and a nil m
// reads the run as the number it is. Reducing as it goes keeps the work linear
// in the run's length; parsing the run whole into a big.Int would not be.
func jsonDigitsModBig(d *jsonDecimal, m *big.Int) *big.Int {
	_r := new(big.Int)
	_t := new(big.Int)
	_w := d.width()
	for _i := 0; _i < _w; {
		_n := _w - _i
		if _n > 18 {
			_n = 18
		}
		var _chunk uint64
		for _j := 0; _j < _n; _j++ {
			_chunk = _chunk*10 + uint64(d.digit(_i+_j)-'0')
		}
		_r.Mul(_r, _t.SetUint64(jsonPow10[_n]))
		_r.Add(_r, _t.SetUint64(_chunk))
		if m != nil {
			_r.Mod(_r, m)
		}
		_i += _n
	}
	return _r
}

// jsonPowModUint is b^e mod m by repeated squaring, in 128-bit products.
func jsonPowModUint(b, e, m uint64) uint64 {
	if m == 1 {
		return 0
	}
	_r := uint64(1)
	_b := b % m
	for e > 0 {
		if e&1 == 1 {
			_hi, _lo := bits.Mul64(_r, _b)
			_r = bits.Rem64(_hi, _lo, m)
		}
		_hi, _lo := bits.Mul64(_b, _b)
		_b = bits.Rem64(_hi, _lo, m)
		e >>= 1
	}
	return _r
}

var jsonPow10 = [...]uint64{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10,
	1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18}

// jsonNumberNotMultipleOf is multipleOf broken, for a number held in any
// representation. It is false for a value that is not a number, so that a
// check written as "if broken, refuse" passes over a string or an object as
// the keyword says it must.
func jsonNumberNotMultipleOf(v any, m string) bool {
	_lit, _ok := jsonNumberOf(v)
	return _ok && !jsonDecimalIsMultipleOf(_lit, m)
}

// jsonFloatIsMultipleOf is multipleOf for a float64, judged on the number it
// marshals to (see jsonNumberOf) with a fast path for the divisors a schema
// usually writes.
//
// The generator passes the divisor twice: as its literal, and as
// mDigits*10^-mFrac where it can be written that way with mDigits below 2^53
// and mFrac at most 15 -- 3 is (3, 0), 0.01 is (1, 2) -- and mDigits is 0
// otherwise. With x = f*10^mFrac below 2^51 in magnitude, the float64 spacing at
// f is under half of 10^-mFrac, so at most one decimal with mFrac places reads
// back as f, and it is the shortest one exactly when it exists: round(x)
// names it, dividing back by 10^mFrac -- a correctly rounded operation on two
// exact operands -- checks that it does read back as f, and then the question
// is whether mDigits divides that integer. No decimal with mFrac places reading
// back as f means the number f marshals to has more places than the divisor,
// which no multiple of it has. Everything else is answered on the literal.
func jsonFloatIsMultipleOf(f float64, m string, mDigits int64, mFrac int) bool {
	if mDigits > 0 {
		_x := f * jsonFloatPow10[mFrac]
		if _x > -(1<<51) && _x < 1<<51 {
			_n := math.Round(_x)
			if _n/jsonFloatPow10[mFrac] == f {
				return int64(_n)%mDigits == 0
			}
			return false
		}
	}
	return !jsonNumberNotMultipleOf(f, m)
}

var jsonFloatPow10 = [...]float64{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10,
	1e11, 1e12, 1e13, 1e14, 1e15}

// jsonBigIntExponentLimit is how many zeros an exponent may add to an integer
// jsonBigIntFromLiteral builds. The digits a literal writes out in full cost
// what the document cost to send; the ones an exponent stands for do not, and
// 1e1000000000 is ten bytes naming a number a big.Int would need 400MB to
// hold. Ten thousand is far past any integer a schema means -- float64 stops
// at 308, and a 4096-bit key is 1234 digits -- and costs microseconds.
const jsonBigIntExponentLimit = 10000

// jsonBigIntFromLiteral reads a JSON number literal as the integer it names,
// exactly: 1.0, 1e2 and 12345678901234567891 all answer, 1.5 and
// 12345678901234567891.5 do not. ok is false for a literal that names no
// integer, and tooLarge is set with it when the literal does name one, but one
// whose exponent adds more zeros than jsonBigIntExponentLimit.
//
// This replaces a parse into a big.Float of 64 bits, which is not arbitrary
// precision: it read 12345678901234567891.5 as the integer
// 12345678901234567892, and 1e100 as a different integer than 10^100.
func jsonBigIntFromLiteral(s string) (_v *big.Int, ok bool, tooLarge bool) {
	_d, _ok := jsonNumberParts(s)
	if !_ok {
		return nil, false, false
	}
	if _d.isZero() {
		return new(big.Int), true, false
	}
	if _d.bigScale != nil {
		if _d.bigScale.Sign() < 0 {
			return nil, false, false
		}
		return nil, false, true
	}
	if _d.scale < 0 {
		return nil, false, false
	}
	if _d.scale > jsonBigIntExponentLimit {
		return nil, false, true
	}
	_v = jsonDigitsToBig(_d.hi + _d.lo)
	if _d.scale > 0 {
		_v.Mul(_v, new(big.Int).Exp(big.NewInt(10), big.NewInt(_d.scale), nil))
	}
	if _d.neg {
		_v.Neg(_v)
	}
	return _v, true, false
}

// jsonDigitsToBig reads a run of decimal digits as a big.Int by halves:
// big.Int's own SetString multiplies in eighteen digits at a time, which is
// quadratic in the length and took ten seconds over a million-digit number. The
// halves are joined with one multiplication by a power of ten, so the whole is
// the cost of the multiplications -- a small fraction of a second there.
func jsonDigitsToBig(digits string) *big.Int {
	if len(digits) <= 1024 {
		_z, _ := new(big.Int).SetString(digits, 10)
		return _z
	}
	_mid := len(digits) / 2
	_hi := jsonDigitsToBig(digits[:len(digits)-_mid])
	_lo := jsonDigitsToBig(digits[len(digits)-_mid:])
	_hi.Mul(_hi, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(_mid)), nil))
	return _hi.Add(_hi, _lo)
}

// jsonDecodeNumbers decodes a JSON text into an `any` with every number held
// as the json.Number the document wrote, rather than as the float64
// json.Unmarshal would make of it.
//
// It is what every check that judges a value decoded from the document's own
// bytes decodes through -- a schema kept as data and evaluated at run time, a
// wrapper over a value the schema gives no type, a branch of an if/then/else --
// because those are positions where the literal is still in hand, and a float64
// would throw away exactly the digits the keywords are about: 9007199254740993
// read as 9007199254740992 passed {"maximum":9007199254740992}, and 1e400 read
// as nothing at all refused a document every keyword it met would have
// admitted.
//
// A text that is not one JSON value is refused with the error json.Unmarshal
// gives for it, so a caller reports the same failure it always did.
func jsonDecodeNumbers(data []byte, v *any) error {
	if !json.Valid(data) {
		var _discard any
		return json.Unmarshal(data, &_discard)
	}
	_dec := json.NewDecoder(bytes.NewReader(data))
	_dec.UseNumber()
	return _dec.Decode(v)
}
