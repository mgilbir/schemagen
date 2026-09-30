package runtime

import (
	"encoding/json"
	"errors"
	"hash/maphash"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

// jsonID is the identity of a JSON value: what uniqueItems, const and enum
// compare values by, read off the value as it is held rather than off an
// encoding of it.
//
// They used to marshal the value and compare the text, at every level of the
// document: a check on an array of objects wrote out each object's whole
// subtree, the same check one level down wrote out the same subtrees again, and
// a document whose arrays nest d deep was written d times over to be judged
// once. Two values equal as JSON -- the same members in any order, the same
// numbers however spelled, the same characters however escaped -- have one
// identity by construction, because every rule below that could tell them
// apart is a rule of spelling and the identity is taken after it. Two values
// that differ share one only by a collision of two independently seeded 64-bit
// hashes, seeded afresh in every process; the one caller that has to be certain
// -- uniqueItems, about to refuse an array for a duplicate -- confirms it (see
// jsonSameValue).
type ID struct{ a, b uint64 }

var jsonIDSeeds = [2]maphash.Seed{maphash.MakeSeed(), maphash.MakeSeed()}

// The kinds an identity is tagged with, so that no value of one kind shares an
// identity with a value of another: the string "1" and the number 1, the array
// [] and the object {}. jsonIDLiteralKind is a number whose exponent is past
// what jsonNumberDigits reads into an int64, identified by its canonical text
// (see jsonIDNumber).
const (
	jsonIDNullKind    = 'n'
	jsonIDTrueKind    = 't'
	jsonIDFalseKind   = 'f'
	jsonIDStringKind  = 's'
	jsonIDNumberKind  = 'd'
	jsonIDLiteralKind = 'l'
	jsonIDArrayKind   = 'a'
	jsonIDObjectKind  = 'o'
	jsonIDMemberKind  = 'm'
)

// jsonIDMix combines two identities and a tag into one, under both seeds.
func jsonIDMix(x jsonID, tag byte, y jsonID) jsonID {
	var buf [33]byte
	jsonPutUint64(buf[0:], x.a)
	jsonPutUint64(buf[8:], x.b)
	jsonPutUint64(buf[16:], y.a)
	jsonPutUint64(buf[24:], y.b)
	buf[32] = tag
	return jsonID{maphash.Bytes(jsonIDSeeds[0], buf[:]), maphash.Bytes(jsonIDSeeds[1], buf[:])}
}

func jsonPutUint64(b []byte, v uint64) {
	_ = b[7]
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	b[4], b[5], b[6], b[7] = byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56)
}

// jsonIDOfKind is the identity of null, true or false.
func jsonIDOfKind(kind byte) jsonID {
	return jsonIDMix(jsonID{}, kind, jsonID{})
}

func jsonIDBool(b bool) jsonID {
	if b {
		return jsonIDOfKind(jsonIDTrueKind)
	}
	return jsonIDOfKind(jsonIDFalseKind)
}

// jsonIDText and jsonIDBytes are the identity of a text of the given kind: a
// string's characters, a number's canonical digits. The two agree on equal
// contents, as maphash's String and Bytes do.
func jsonIDText(kind byte, s string) jsonID {
	return jsonIDMix(jsonID{maphash.String(jsonIDSeeds[0], s), maphash.String(jsonIDSeeds[1], s)}, kind, jsonID{uint64(len(s)), 0})
}

func jsonIDBytes(kind byte, b []byte) jsonID {
	return jsonIDMix(jsonID{maphash.Bytes(jsonIDSeeds[0], b), maphash.Bytes(jsonIDSeeds[1], b)}, kind, jsonID{uint64(len(b)), 0})
}

// jsonIDString is the identity of a Go string as encoding/json writes it, which
// writes every byte that is not UTF-8 as U+FFFD, each on its own: two strings
// that differ only there are one JSON string.
func jsonIDString(s string) jsonID {
	return jsonIDText(jsonIDStringKind, jsonValidString(s))
}

// jsonIDNumber is the identity of a number literal: its canonical reading, so
// that 1, 1.0 and 1e0 are one number. It reads the literal as jsonNumberParts
// does -- the two are held together by the tests -- without building its digit
// string. A literal whose exponent is past what jsonNumberDigits holds in an
// int64 is identified by its canonical text instead (see _jsonCanonicalNumber,
// which reads any exponent): 1e2000000000000 and 10e1999999999999 are one
// number, and no literal that fits in memory reaches such a value from a small
// exponent, so the two kinds of identity never name one number.
func jsonIDNumber[S ~string | ~[]byte](s S) jsonID {
	neg, lo, hi, scale, ok := jsonNumberDigits(s)
	if !ok {
		return jsonIDText(jsonIDLiteralKind, _jsonCanonicalNumber(string(s)))
	}
	if lo == hi {
		return jsonIDText(jsonIDNumberKind, "0")
	}
	var small [64]byte
	digits := small[:0]
	if hi-lo > len(small) {
		digits = make([]byte, 0, hi-lo)
	}
	digits = jsonAppendDigits(digits, s, lo, hi)
	sign := uint64(0)
	if neg {
		sign = 1
	}
	return jsonIDMix(jsonIDBytes(jsonIDNumberKind, digits), jsonIDNumberKind, jsonID{uint64(scale), sign})
}

// jsonIDExponentLimit is the widest exponent jsonNumberDigits reads into an
// int64 scale: the scale adds and subtracts digit counts, which no literal in
// memory makes large enough to carry an exponent this size past int64's range.
// A literal past it is read by jsonIDNumber through its canonical text.
const jsonIDExponentLimit = 1 << 40

// jsonNumberDigits reads a number literal into its sign, its significant digits
// and the power of ten the last of them stands for. The digits are positions lo
// to hi of the literal's digits with its decimal point taken out; lo == hi is
// zero, whatever its sign.
func jsonNumberDigits[S ~string | ~[]byte](s S) (neg bool, lo, hi int, scale int64, ok bool) {
	if len(s) == 0 {
		return false, 0, 0, 0, true
	}
	i := 0
	if s[i] == '-' || s[i] == '+' {
		neg = s[i] == '-'
		i++
	}
	intStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	intEnd := i
	fracStart, fracEnd := i, i
	if i < len(s) && s[i] == '.' {
		i++
		fracStart = i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		fracEnd = i
	}
	if intStart == intEnd && fracStart == fracEnd {
		return false, 0, 0, 0, false
	}
	exp := int64(0)
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		expStart := i
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		digitsStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if digitsStart == i {
			return false, 0, 0, 0, false
		}
		e, err := strconv.ParseInt(string(s[expStart:i]), 10, 64)
		if err != nil || e > jsonIDExponentLimit || e < -jsonIDExponentLimit {
			return false, 0, 0, 0, false
		}
		exp = e
	}
	if i != len(s) {
		return false, 0, 0, 0, false
	}
	nInt := intEnd - intStart
	digitAt := func(k int) byte {
		if k < nInt {
			return s[intStart+k]
		}
		return s[fracStart+k-nInt]
	}
	lo, hi = 0, nInt+(fracEnd-fracStart)
	for lo < hi && digitAt(lo) == '0' {
		lo++
	}
	scale = exp - int64(fracEnd-fracStart)
	for hi > lo && digitAt(hi-1) == '0' {
		hi--
		scale++
	}
	if lo == hi {
		return false, 0, 0, 0, true
	}
	return neg, lo, hi, scale, true
}

// jsonAppendDigits appends the digits lo to hi of a literal jsonNumberDigits
// read.
func jsonAppendDigits[S ~string | ~[]byte](b []byte, s S, lo, hi int) []byte {
	i, k := 0, 0
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		i = 1
	}
	for ; i < len(s) && k < hi; i++ {
		c := s[i]
		if c == '.' {
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		if k >= lo {
			b = append(b, c)
		}
		k++
	}
	return b
}

// jsonIDInt, jsonIDUint and jsonIDFloat are the identity of a Go number as
// encoding/json writes it. A float is written in the shortest spelling that
// reads back as the same float, at the float's own size, and that is the
// number the document holds; encoding/json refuses NaN and the infinities.
func jsonIDInt(v int64) jsonID {
	var buf [24]byte
	return jsonIDNumber(strconv.AppendInt(buf[:0], v, 10))
}

func jsonIDUint(v uint64) jsonID {
	var buf [24]byte
	return jsonIDNumber(strconv.AppendUint(buf[:0], v, 10))
}

func jsonIDFloat(f float64, bits int) (jsonID, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return jsonID{}, &json.UnsupportedValueError{Str: strconv.FormatFloat(f, 'g', -1, bits)}
	}
	var buf [32]byte
	return jsonIDNumber(strconv.AppendFloat(buf[:0], f, 'e', -1, bits)), nil
}

// jsonIDNumberLiteral is the identity of a json.Number, which encoding/json
// writes as the literal it holds -- an empty one as 0 -- and refuses when it is
// not a number.
func jsonIDNumberLiteral(n json.Number) (jsonID, error) {
	if n == "" {
		return jsonIDNumber("0"), nil
	}
	if !jsonIsNumberLiteral(string(n)) {
		return jsonID{}, errors.New("json: invalid number literal " + strconv.Quote(string(n)))
	}
	return jsonIDNumber(string(n)), nil
}

// jsonIsNumberLiteral reports whether s is a JSON number.
func jsonIsNumberLiteral[S ~string | ~[]byte](s S) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	return i == len(s)
}

// IDArray folds the identities of an array's elements, in order.
type IDArray struct {
	acc jsonID
	n   uint64
}

func (a *jsonIDArray) add(id jsonID) {
	a.acc = jsonIDMix(a.acc, jsonIDArrayKind, id)
	a.n++
}

func (a *jsonIDArray) id() jsonID {
	return jsonIDMix(a.acc, jsonIDArrayKind, jsonID{a.n, 1})
}

func jsonIDMemberOf(key, val jsonID) jsonID {
	return jsonIDMix(key, jsonIDMemberKind, val)
}

// jsonIDObjectOf is the identity of an object whose n members' identities (see
// jsonIDMemberOf) sum to sum: a sum, because a member's place in the object is
// no part of the value.
func jsonIDObjectOf(sum jsonID, n uint64) jsonID {
	return jsonIDMix(sum, jsonIDObjectKind, jsonID{n, 2})
}

// jsonKeyMayShare reports whether a key of a Go map can be read as the same name
// as another key: whether it holds a byte that is not UTF-8, read as U+FFFD, or
// U+FFFD itself. Ranging over a string yields utf8.RuneError for both, so one
// pass over the key answers, and a key that answers no has a name of its own.
func jsonKeyMayShare(k string) bool {
	for _, r := range k {
		if r == utf8.RuneError {
			return true
		}
	}
	return false
}

// jsonIDShared is what the sum of an object's members keeps of the members
// whose keys may share a name (see jsonKeyMayShare): for each such name, the key
// of the member counted under it and that member's identity. See jsonIDKeep.
type jsonIDShared map[string]jsonIDSharedMember

type jsonIDSharedMember struct {
	key string
	mem jsonID
}

// jsonIDKeep decides, for the member under key k of an object whose identity
// sums its members, whether it counts -- k being a key that may share a name
// with another. encoding/json writes every member, in the order of the keys,
// each under its key read as UTF-8, and a reader of what it writes keeps the
// last member of a name: so of the members sharing a name the one with the
// greatest key is the one that counts, whatever order the map is ranged in.
// mem is the member's identity (see jsonIDMemberOf); replaced is the identity of
// a member counted before under the same name that this one replaces, when
// replaces says there is one, to be taken back out of the sum.
func jsonIDKeep(shared *jsonIDShared, k string, mem jsonID) (keep bool, replaced jsonID, replaces bool) {
	name := jsonValidString(k)
	if prev, ok := (*shared)[name]; ok {
		if prev.key > k {
			return false, jsonID{}, false
		}
		replaced, replaces = prev.mem, true
	}
	if *shared == nil {
		*shared = make(jsonIDShared, 1)
	}
	(*shared)[name] = jsonIDSharedMember{key: k, mem: mem}
	return true, replaced, replaces
}

// jsonIDIn reports whether id is one of ids.
func jsonIDIn(id jsonID, ids []jsonID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// jsonValidString is s as encoding/json writes it and reads it back: every
// byte that is not UTF-8 read as U+FFFD.
func jsonValidString(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b []byte
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			b = utf8.AppendRune(b, utf8.RuneError)
		} else {
			b = append(b, s[i:i+n]...)
		}
		i += n
	}
	return string(b)
}

// jsonIDRaw is the identity of a JSON text -- raw JSON a value holds -- read
// with every number exact and a key written twice meaning its last value, which
// is how _jsonCanonical reads one.
func jsonIDRaw(b []byte) (jsonID, error) {
	return jsonIDReadAll(jsonIDReader{data: b})
}

// jsonIDRawMessage is jsonIDRaw for a json.RawMessage, which encoding/json
// writes as null when it is nil.
func jsonIDRawMessage(b []byte) (jsonID, error) {
	if b == nil {
		return jsonIDOfKind(jsonIDNullKind), nil
	}
	return jsonIDRaw(b)
}

// jsonIDRawAsDecoded is jsonIDRaw for a value read the way json.Unmarshal reads
// it into an any: every number a float64.
func jsonIDRawAsDecoded(b []byte) (jsonID, error) {
	return jsonIDReadAll(jsonIDReader{data: b, floats: true})
}

func jsonIDReadAll(r jsonIDReader) (jsonID, error) {
	r.skip()
	id, err := r.readValue(0)
	if err != nil {
		return jsonID{}, err
	}
	r.skip()
	if r.pos != len(r.data) {
		return jsonID{}, r.fail()
	}
	return id, nil
}

// jsonIDReader reads the identity of a JSON text in one pass.
type jsonIDReader struct {
	data   []byte
	pos    int
	floats bool
}

// jsonIDMaxDepth is the deepest nesting jsonIDReader reads, which is
// encoding/json's own limit.
const jsonIDMaxDepth = 10000

func (r *jsonIDReader) fail() error {
	return errors.New("json: invalid JSON at offset " + strconv.Itoa(r.pos))
}

func (r *jsonIDReader) skip() {
	for r.pos < len(r.data) {
		switch r.data[r.pos] {
		case ' ', '\t', '\n', '\r':
			r.pos++
		default:
			return
		}
	}
}

func (r *jsonIDReader) literal(s string, kind byte) (jsonID, error) {
	if len(r.data)-r.pos < len(s) || string(r.data[r.pos:r.pos+len(s)]) != s {
		return jsonID{}, r.fail()
	}
	r.pos += len(s)
	return jsonIDOfKind(kind), nil
}

func (r *jsonIDReader) readValue(depth int) (jsonID, error) {
	if depth > jsonIDMaxDepth {
		return jsonID{}, errors.New("json: exceeded max depth")
	}
	if r.pos >= len(r.data) {
		return jsonID{}, r.fail()
	}
	switch c := r.data[r.pos]; {
	case c == 'n':
		return r.literal("null", jsonIDNullKind)
	case c == 't':
		return r.literal("true", jsonIDTrueKind)
	case c == 'f':
		return r.literal("false", jsonIDFalseKind)
	case c == '"':
		var small [64]byte
		s, err := r.str(small[:0])
		if err != nil {
			return jsonID{}, err
		}
		return jsonIDBytes(jsonIDStringKind, s), nil
	case c == '-' || c >= '0' && c <= '9':
		start := r.pos
		for r.pos < len(r.data) {
			if c := r.data[r.pos]; c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				break
			}
			r.pos++
		}
		text := r.data[start:r.pos]
		if !jsonIsNumberLiteral(text) {
			return jsonID{}, r.fail()
		}
		if r.floats {
			f, err := strconv.ParseFloat(string(text), 64)
			if err != nil {
				return jsonID{}, errors.New("json: cannot unmarshal number " + string(text) + " into Go value of type float64")
			}
			return jsonIDFloat(f, 64)
		}
		return jsonIDNumber(text), nil
	case c == '[':
		r.pos++
		var a jsonIDArray
		r.skip()
		if r.pos < len(r.data) && r.data[r.pos] == ']' {
			r.pos++
			return a.id(), nil
		}
		for {
			r.skip()
			id, err := r.readValue(depth + 1)
			if err != nil {
				return jsonID{}, err
			}
			a.add(id)
			r.skip()
			if r.pos >= len(r.data) {
				return jsonID{}, r.fail()
			}
			switch r.data[r.pos] {
			case ',':
				r.pos++
			case ']':
				r.pos++
				return a.id(), nil
			default:
				return jsonID{}, r.fail()
			}
		}
	case c == '{':
		r.pos++
		return r.object(depth)
	}
	return jsonID{}, r.fail()
}

// jsonIDMember is one member of an object being read: its key's identity and
// its own, kept until the object ends so that a key written twice is counted
// once, with its last value.
type jsonIDMember struct {
	key jsonID
	mem jsonID
}

func (r *jsonIDReader) object(depth int) (jsonID, error) {
	var small [16]jsonIDMember
	ms := small[:0]
	r.skip()
	if r.pos < len(r.data) && r.data[r.pos] == '}' {
		r.pos++
		return jsonIDObjectOf(jsonID{}, 0), nil
	}
	for {
		r.skip()
		if r.pos >= len(r.data) || r.data[r.pos] != '"' {
			return jsonID{}, r.fail()
		}
		var kbuf [64]byte
		k, err := r.str(kbuf[:0])
		if err != nil {
			return jsonID{}, err
		}
		kid := jsonIDBytes(jsonIDStringKind, k)
		r.skip()
		if r.pos >= len(r.data) || r.data[r.pos] != ':' {
			return jsonID{}, r.fail()
		}
		r.pos++
		r.skip()
		vid, err := r.readValue(depth + 1)
		if err != nil {
			return jsonID{}, err
		}
		ms = append(ms, jsonIDMember{key: kid, mem: jsonIDMemberOf(kid, vid)})
		r.skip()
		if r.pos >= len(r.data) {
			return jsonID{}, r.fail()
		}
		if r.data[r.pos] == ',' {
			r.pos++
			continue
		}
		if r.data[r.pos] != '}' {
			return jsonID{}, r.fail()
		}
		r.pos++
		return jsonIDMembers(ms), nil
	}
}

// jsonIDMembers is the identity of an object read as a list of members, a key
// written twice meaning its last value.
func jsonIDMembers(ms []jsonIDMember) jsonID {
	if len(ms) > 16 {
		// Sorted by key, stably, so that the last of a run of equal keys is the
		// one written last.
		sort.SliceStable(ms, func(i, j int) bool {
			if ms[i].key.a != ms[j].key.a {
				return ms[i].key.a < ms[j].key.a
			}
			return ms[i].key.b < ms[j].key.b
		})
	}
	var sum jsonID
	n := uint64(0)
	for i := range ms {
		if jsonIDKeyAgain(ms, i) {
			continue
		}
		sum.a += ms[i].mem.a
		sum.b += ms[i].mem.b
		n++
	}
	return jsonIDObjectOf(sum, n)
}

// jsonIDKeyAgain reports whether the key of member i is written again later.
func jsonIDKeyAgain(ms []jsonIDMember, i int) bool {
	if len(ms) > 16 {
		return i+1 < len(ms) && ms[i+1].key == ms[i].key
	}
	for j := i + 1; j < len(ms); j++ {
		if ms[j].key == ms[i].key {
			return true
		}
	}
	return false
}

// str reads a string literal into b, unescaped as encoding/json unescapes one:
// a surrogate that is not half of a pair, and a byte that is not UTF-8, read as
// U+FFFD.
func (r *jsonIDReader) str(b []byte) ([]byte, error) {
	r.pos++
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		switch {
		case c == '"':
			r.pos++
			return b, nil
		case c == '\\':
			if r.pos+1 >= len(r.data) {
				return nil, r.fail()
			}
			e := r.data[r.pos+1]
			r.pos += 2
			switch e {
			case '"', '\\', '/':
				b = append(b, e)
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'u':
				cp, ok := r.hex4()
				if !ok {
					return nil, r.fail()
				}
				switch {
				case cp >= 0xD800 && cp < 0xDC00:
					// Half of a pair only where the other half follows.
					save := r.pos
					if r.pos+1 < len(r.data) && r.data[r.pos] == '\\' && r.data[r.pos+1] == 'u' {
						r.pos += 2
						if lo, ok := r.hex4(); ok && lo >= 0xDC00 && lo < 0xE000 {
							b = utf8.AppendRune(b, ((cp-0xD800)<<10|(lo-0xDC00))+0x10000)
							continue
						}
					}
					r.pos = save
					b = utf8.AppendRune(b, utf8.RuneError)
				case cp >= 0xDC00 && cp < 0xE000:
					b = utf8.AppendRune(b, utf8.RuneError)
				default:
					b = utf8.AppendRune(b, cp)
				}
			default:
				return nil, r.fail()
			}
		case c < 0x20:
			return nil, r.fail()
		case c < utf8.RuneSelf:
			b = append(b, c)
			r.pos++
		default:
			cr, n := utf8.DecodeRune(r.data[r.pos:])
			if cr == utf8.RuneError && n == 1 {
				b = utf8.AppendRune(b, utf8.RuneError)
			} else {
				b = append(b, r.data[r.pos:r.pos+n]...)
			}
			r.pos += n
		}
	}
	return nil, r.fail()
}

func (r *jsonIDReader) hex4() (rune, bool) {
	if len(r.data)-r.pos < 4 {
		return 0, false
	}
	var v rune
	for _, c := range r.data[r.pos : r.pos+4] {
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	r.pos += 4
	return v, true
}
