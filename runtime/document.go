package runtime

import (
	"encoding/json"
	"reflect"
	"sort"
	"sync/atomic"
	"unicode/utf8"
)

// Doc is one JSON document being decoded by the types schemagen generates, read
// in a single pass however deeply the types nest. Generated code opens one with
// OpenDoc and hands each member's span to the member's own type.
//
// Each generated type decodes the value it is handed in place, at a span of this
// document, and hands each member's span to the member's own type in turn.
// Nothing is decoded twice, nothing is copied on the way down, and a member's
// end is looked up rather than found by scanning: the document is indexed once,
// when it is opened, with the end of every object and array in it. So the whole
// decode costs time and memory in proportion to the document, whatever its
// depth.
//
// It is what replaced decoding each level through encoding/json. A type that
// parses its object into raw members and then decodes it again through an alias
// struct scans its whole subtree twice, and every type below it scans its own
// subtree twice again: for a recursive schema that is depth times size. A
// document nested 8,000 levels deep, 48 KB long, took six seconds to decode; one
// whose deepest member was refused took time exponential in the depth, because
// the refusal was traced by decoding each member again on its own, and each of
// those decodes traced its own refusal the same way.
//
// data is the caller's buffer and is never kept past the decode. What a decoded
// value does keep -- the members a later check reads, the bytes a raw-JSON
// wrapper holds -- is cut from own, one copy of the document taken the first
// time anything asks to keep a part of it. So a value never shares memory with
// a buffer it did not allocate, and the parts it keeps at every depth share one
// copy rather than each holding their own. See keep.
//
// The members are declared in the order the garbage collector scans least of:
// the interface first, both of whose words are pointers, and then the slices,
// whose pointer leads and whose length and capacity follow.
type Doc struct {
	// refusal is --strict-read-write's refusal of a document that sets a
	// readOnly location, recorded by whichever type found it and returned by
	// finish once the whole value is decoded. Never set under any other
	// configuration.
	refusal error
	// ids are the identities of the document's objects and arrays, by where
	// they start, computed the first time a check comparing values asks for
	// one: a value read lazily is a view of the document, which nothing
	// changes, so what was computed once holds. See jsonLazy.jsonDocID.
	// idsExact are the same, read with every number exact (see
	// jsonLazy.exact): one span has two identities where its numbers read two
	// ways, so each reading keeps its own.
	ids      atomic.Pointer[jsonIDCache]
	idsExact atomic.Pointer[jsonIDCache]
	data     []byte
	own      []byte
	starts   []int
	ends     []int
	// trial counts the union branch trials in progress. See copyOf.
	trial int
	// views is set on a document whose values never reach a caller -- one
	// Validate decodes a held value from, see jsonDecodeHeld -- so that every
	// raw part of them is a view of the document rather than a copy. See copyOf.
	views int
	// small is where starts and ends begin, so that indexing a document with
	// few objects and arrays -- most documents -- allocates nothing beyond the
	// jsonDoc itself. See newJSONDoc.
	small [2 * jsonSmallIndex]int
}

// jsonSmallIndex is how many objects and arrays a document can hold before its
// index needs memory of its own. Four keeps a jsonDoc within 192 bytes, one
// allocation where the index and the scan's stack of open containers were
// three more -- more than a flat document costs to decode in all.
const jsonSmallIndex = 4

// newJSONDoc returns a document over data with an empty index. starts and ends
// are cut from small with their capacity capped, so the first append past it
// moves them to an array of their own rather than writing into the other.
func newJSONDoc(data []byte) *jsonDoc {
	d := &jsonDoc{data: data}
	d.starts = d.small[:0:jsonSmallIndex]
	d.ends = d.small[jsonSmallIndex : jsonSmallIndex : 2*jsonSmallIndex]
	return d
}

// finish is what a decode opened by jsonOpenDoc returns: the refusal recorded
// on the document, if one was, and otherwise what the decode itself returned.
//
// A refusal outranks every other answer, and is recorded rather than returned
// so that nothing stops decoding when one is found: a Validate check that has to
// ignore it -- readOnly is an annotation, and constrains no document -- still
// gets the whole value to judge. See _decodeIgnoringReadOnly.
//
// It is also where the document lets go of the caller's buffer. A value that
// judges its held parts later keeps the document (see jsonDecodeHeld), and from
// here on the document reads its own copy, which is all those parts are views
// of: data is the caller's, and a decoded value never keeps it.
//
//go:noinline
func (d *jsonDoc) finish(err error) error {
	d.data = d.own
	if d.refusal != nil {
		return d.refusal
	}
	return err
}

// spanOf reports where raw lies in the document, when raw is one of its values
// as keep hands them out: a view of the document's own copy, from where a value
// starts to where it ends. A value's held parts are such views wherever the
// value was decoded in place, and nothing else is: whatever a caller can reach
// or set is a copy of its own.
func (d *jsonDoc) spanOf(raw []byte) (jsonSpan, bool) {
	if d == nil || len(raw) == 0 || len(raw) > len(d.own) {
		return jsonSpan{}, false
	}
	base := reflect.ValueOf(d.own).Pointer()
	at := reflect.ValueOf(raw).Pointer()
	if at < base || at-base > uintptr(len(d.own)-len(raw)) {
		return jsonSpan{}, false
	}
	start := int(at - base)
	sp := jsonSpan{start, start + len(raw)}
	switch d.own[start] {
	case '{', '[':
		i := sort.SearchInts(d.starts, start)
		if i == len(d.starts) || d.starts[i] != start || d.ends[i] != sp.end {
			return jsonSpan{}, false
		}
	}
	return sp, true
}

// cursor is a document over this one's copy and index for one more decode, which
// writes only to the cursor: a value may be judged from several goroutines at
// once, and the document it keeps is shared by all of them.
func (d *jsonDoc) cursor() *jsonDoc {
	return &jsonDoc{data: d.own, own: d.own, starts: d.starts, ends: d.ends, views: 1}
}

// jsonDecodeHeld decodes a value its owner holds as raw JSON -- a
// patternProperties member, a member no branch accounts for, a type-schema
// alternative's value -- into *p, the way the value's own UnmarshalJSON would.
//
// Validate does this for every such value it judges, and the value's own
// Validate does it again for the values held inside it. Decoded from its bytes
// each time, a value nested d levels deep was scanned, checked and copied once
// per level above it: a patternProperties chain 2,000 levels deep took 363 ms
// and 194 MB to validate. So the decode happens in place wherever the bytes are
// a view of the document the owner keeps, and otherwise opens a document once,
// whose values are then all views of it (see views) -- every level below is
// decoded in place from the one document, and the whole judgement costs what
// the document does.
func jsonDecodeHeld[T any](doc *jsonDoc, raw []byte, p *T, dec At[T]) error {
	if sp, ok := doc.spanOf(raw); ok {
		d := doc.cursor()
		return d.finish(dec(p, d, sp))
	}
	d, sp, err := jsonOpenDoc(raw)
	if err != nil {
		return jsonDecodeRefusal(err)
	}
	d.views = 1
	return d.finish(dec(p, d, sp))
}

// Lazy is a value of a document read one level at a time: the checks that
// judge a held member as decoded JSON -- a conditional's branch, a keyword
// evaluated at run time -- mostly ask about its type or its first level, and
// the member may hold the rest of the document. Decoded whole, it was decoded
// once per level of a recursive document, which made those checks quadratic in
// its depth.
//
// exact says how a number in it reads. A check that judges the document's own
// bytes -- a conditional's branch, the runtime evaluator, a type union over a
// held member -- reads every number as the literal the document wrote, as a
// json.Number: the numeric keywords are defined over numbers as mathematical
// values, and a float64 reads 9007199254740993 as 9007199254740992 and 1e400 as
// nothing at all. An element of a tuple this package holds as []any (see
// jsonLazyItemsOr) reads as the float64 the tuple holds, since that is the
// value the type has.
type Lazy struct {
	d     *jsonDoc
	sp    jsonSpan
	exact bool
}

// jsonHeld is a member its owner holds as raw JSON, for a check that reads it
// as decoded JSON, every number the literal the document wrote: read lazily
// where it is a view of the document the owner keeps, and decoded whole where
// it is not.
func jsonHeld(doc *jsonDoc, raw []byte) (any, error) {
	if sp, ok := doc.spanOf(raw); ok {
		return jsonLazy{doc, sp, true}, nil
	}
	var v any
	err := jsonDecodeNumbers(raw, &v)
	return v, err
}

// jsonReadLazily is raw JSON a value holds, read one level at a time through a
// document opened over it -- the value's own bytes, which it never changes and
// hands out only as copies, so the document reads them where they are -- with
// every number the literal it wrote (see jsonLazy.exact).
func jsonReadLazily(raw []byte) (any, error) {
	d, sp, err := jsonOpenDoc(raw)
	if err != nil {
		return nil, err
	}
	d.own = raw
	d.views = 1
	_ = d.finish(nil)
	return jsonLazy{d, sp, true}, nil
}

// jsonTop is v with its first level read, where it is a jsonLazy: what a check
// that type-switches on a decoded value needs. Anything else is returned as it
// is.
func jsonTop(v any) any {
	if l, ok := v.(jsonLazy); ok {
		return l.jsonLevel()
	}
	return v
}

// jsonLevel is the value's first level as encoding/json decodes it into an any
// -- a map[string]any, a []any, a string, a number, a bool or nil -- with every
// member and element a jsonLazy in turn, read the same way. A number is a
// json.Number where the value is exact and a float64 where it is not. A key
// written twice means its last value, as it does to encoding/json.
func (l jsonLazy) jsonLevel() any {
	d, sp := l.d, l.sp
	switch d.data[sp.start] {
	case '{':
		m := map[string]any{}
		it := d.iter(sp)
		for {
			k, v, ok := it.member()
			if !ok {
				return m
			}
			m[k] = jsonLazy{d, v, l.exact}
		}
	case '[':
		a := []any{}
		it := d.iter(sp)
		for {
			v, ok := it.elem()
			if !ok {
				return a
			}
			a = append(a, jsonLazy{d, v, l.exact})
		}
	}
	var v any
	if l.exact {
		_ = jsonDecodeNumbers(d.raw(sp), &v)
	} else {
		_ = json.Unmarshal(d.raw(sp), &v)
	}
	return v
}

// jsonLazyItemsOr decodes a tuple -- an array held as []any, some position of
// which is a type of this package -- with dec, except where Validate decodes a
// held value (see views): there each element is a jsonLazy, and a position of
// this package's type is decoded from its span rather than from the element's
// re-encoding (see jsonDecodeLazy). Decoded whole, the element held the rest of
// the document as maps and slices, and the position's Validate re-encoded and
// re-decoded it, at every level of a tuple that holds itself.
func jsonLazyItemsOr[S ~[]any](p *S, d *jsonDoc, sp jsonSpan, dec At[S]) error {
	if d.views == 0 || d.data[sp.start] != '[' {
		return dec(p, d, sp)
	}
	d.keep(sp)
	s := S{}
	it := d.iter(sp)
	for {
		e, ok := it.elem()
		if !ok {
			break
		}
		s = append(s, jsonLazy{d, e, false})
	}
	*p = s
	return nil
}

// jsonDecodeLazy decodes a lazily read element into *p in place, as
// jsonDecodeHeld decodes a held value.
func jsonDecodeLazy[T any](l jsonLazy, p *T, dec At[T]) error {
	d := l.d.cursor()
	return d.finish(dec(p, d, l.sp))
}

// MarshalJSON writes the value as encoding/json writes its decoded form, which
// is what a check comparing values by their encoding reads: the same number
// spellings and key order the whole decode would have produced -- every number
// as its literal where the value is exact.
func (l jsonLazy) MarshalJSON() ([]byte, error) {
	var v any
	var err error
	if l.exact {
		err = jsonDecodeNumbers(l.d.raw(l.sp), &v)
	} else {
		err = json.Unmarshal(l.d.raw(l.sp), &v)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// jsonAttempt runs one trial decode of a union branch under
// --strict-read-write. A refusal recorded during the trial is the trial's
// failure, as it was when the refusal was the branch's own decode error, and is
// taken back off the document: a branch the value turns out not to be sets no
// readOnly location of the value.
func jsonAttempt(d *jsonDoc, f func() error) error {
	before := d.refusal
	err := f()
	if d.refusal != before {
		err = d.refusal
		d.refusal = before
	}
	return err
}

// Span is where one value of a Doc lies: data[start:end], with no white space
// at either end.
type Span struct {
	start, end int
}

// jsonOpenDoc checks that data is one JSON value and indexes it, in one pass.
//
// A document that is not JSON is refused with encoding/json's own words for it,
// which are what every decoder here refused one with before. encoding/json never
// hands an UnmarshalJSON anything else, so this is reached only by a caller who
// calls one directly.
//
// The check is scan's rather than json.Valid's, and accepts exactly what that
// accepts. json.Valid is a second pass over the document, and on the
// encoding/json that Go 1.27 ships it costs more than linear time in the depth
// of the nesting -- a check this decode would otherwise pay on top of its own.
// Where the two could ever disagree, encoding/json has the last word: a
// document scan refuses is handed to encoding/json for the refusal, and one that
// encoding/json then accepts is indexed without the check.
func jsonOpenDoc(data []byte) (*jsonDoc, jsonSpan, error) {
	d := newJSONDoc(data)
	if !d.scan() {
		var v json.RawMessage
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, jsonSpan{}, err
		}
		d = newJSONDoc(data)
		d.index()
	}
	start := jsonSkipSpace(data, 0)
	return d, jsonSpan{start, d.valueEnd(start)}, nil
}

// jsonMaxDepth is how deeply encoding/json lets objects and arrays nest.
const jsonMaxDepth = 10000

// scan checks that the document is one JSON value, exactly as json.Valid does,
// and records where every object and array of it starts and ends. The starts
// are recorded in document order, which is the order valueEnd searches them in.
func (d *jsonDoc) scan() bool {
	data := d.data
	// open is the containers not yet closed, innermost last. It starts on
	// the stack and moves to the heap only when the nesting outgrows that.
	var openStack [jsonSmallIndex]int
	open := openStack[:0]
	i := jsonSkipSpace(data, 0)
	for {
		// A value starts at i.
		if i >= len(data) {
			return false
		}
		switch data[i] {
		case '{', '[':
			if len(open) == jsonMaxDepth {
				return false
			}
			open = append(open, len(d.starts))
			d.starts = append(d.starts, i)
			d.ends = append(d.ends, 0)
			closer := byte(']')
			if data[i] == '{' {
				closer = '}'
			}
			i = jsonSkipSpace(data, i+1)
			if i < len(data) && data[i] == closer {
				last := len(open) - 1
				d.ends[open[last]] = i + 1
				open = open[:last]
				i++
				break
			}
			if closer == '}' {
				if i = jsonScanKey(data, i); i < 0 {
					return false
				}
			}
			continue
		case '"':
			if i = jsonScanString(data, i); i < 0 {
				return false
			}
		case 't':
			if i = jsonScanLiteral(data, i, "true"); i < 0 {
				return false
			}
		case 'f':
			if i = jsonScanLiteral(data, i, "false"); i < 0 {
				return false
			}
		case 'n':
			if i = jsonScanLiteral(data, i, "null"); i < 0 {
				return false
			}
		default:
			if i = jsonScanNumber(data, i); i < 0 {
				return false
			}
		}
		// A value ended just before i: what follows it closes containers
		// until one takes another member.
		for {
			i = jsonSkipSpace(data, i)
			if len(open) == 0 {
				return i == len(data)
			}
			if i >= len(data) {
				return false
			}
			last := len(open) - 1
			isObject := data[d.starts[open[last]]] == '{'
			switch {
			case data[i] == ',':
				i = jsonSkipSpace(data, i+1)
				if isObject {
					if i = jsonScanKey(data, i); i < 0 {
						return false
					}
				}
			case (isObject && data[i] == '}') || (!isObject && data[i] == ']'):
				d.ends[open[last]] = i + 1
				open = open[:last]
				i++
				continue
			default:
				return false
			}
			break
		}
	}
}

// jsonScanKey reads a member's key and its colon, returning where the value
// starts, or -1.
func jsonScanKey(data []byte, i int) int {
	if i >= len(data) || data[i] != '"' {
		return -1
	}
	if i = jsonScanString(data, i); i < 0 {
		return -1
	}
	i = jsonSkipSpace(data, i)
	if i >= len(data) || data[i] != ':' {
		return -1
	}
	return jsonSkipSpace(data, i+1)
}

// jsonScanString returns the index just past the string literal opening at i,
// or -1 where it is not one. A byte that is not UTF-8 is accepted, as
// encoding/json accepts it: it is read as U+FFFD.
func jsonScanString(data []byte, i int) int {
	for j := i + 1; j < len(data); j++ {
		switch c := data[j]; {
		case c == '"':
			return j + 1
		case c == '\\':
			j++
			if j >= len(data) {
				return -1
			}
			switch data[j] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				if j+4 >= len(data) {
					return -1
				}
				for k := j + 1; k <= j+4; k++ {
					if !jsonIsHex(data[k]) {
						return -1
					}
				}
				j += 4
			default:
				return -1
			}
		case c < 0x20:
			return -1
		}
	}
	return -1
}

func jsonIsHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// jsonScanLiteral returns the index just past lit at i, or -1.
func jsonScanLiteral(data []byte, i int, lit string) int {
	if len(data)-i < len(lit) || string(data[i:i+len(lit)]) != lit {
		return -1
	}
	return i + len(lit)
}

// jsonScanNumber returns the index just past the number at i, or -1.
func jsonScanNumber(data []byte, i int) int {
	if i < len(data) && data[i] == '-' {
		i++
	}
	switch {
	case i < len(data) && data[i] == '0':
		i++
	case i < len(data) && data[i] >= '1' && data[i] <= '9':
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	default:
		return -1
	}
	if i < len(data) && data[i] == '.' {
		i++
		if i >= len(data) || data[i] < '0' || data[i] > '9' {
			return -1
		}
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	}
	if i < len(data) && (data[i] == 'e' || data[i] == 'E') {
		i++
		if i < len(data) && (data[i] == '+' || data[i] == '-') {
			i++
		}
		if i >= len(data) || data[i] < '0' || data[i] > '9' {
			return -1
		}
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	}
	return i
}

// index records where every object and array of a document encoding/json has
// already accepted starts and ends, without checking it again. See
// jsonOpenDoc.
func (d *jsonDoc) index() {
	data := d.data
	var openStack [jsonSmallIndex]int // as in scan
	open := openStack[:0]
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			i = jsonStringEnd(data, i) - 1
		case '{', '[':
			open = append(open, len(d.starts))
			d.starts = append(d.starts, i)
			d.ends = append(d.ends, 0)
		case '}', ']':
			last := len(open) - 1
			d.ends[open[last]] = i + 1
			open = open[:last]
		}
	}
}

// jsonSkipSpace returns the first index at or after i that is not JSON white
// space.
func jsonSkipSpace(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// jsonIsNullDocument reports whether data is the JSON null and nothing else but
// white space -- the document isNull asks about at a span, asked of a document
// that was not opened. Anything else, including a null followed by more, is left
// to the decode that follows, which refuses what is not JSON.
func jsonIsNullDocument(data []byte) bool {
	i := jsonSkipSpace(data, 0)
	if len(data)-i < 4 || string(data[i:i+4]) != "null" {
		return false
	}
	return jsonSkipSpace(data, i+4) == len(data)
}

// jsonStringEnd returns the index just past the string literal opening at i.
func jsonStringEnd(data []byte, i int) int {
	for j := i + 1; j < len(data); j++ {
		switch data[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return len(data)
}

// valueEnd returns the index just past the value starting at i. An object or an
// array is looked up; a string or a scalar is read to its end, which costs its
// own length and nothing more.
func (d *jsonDoc) valueEnd(i int) int {
	data := d.data
	if i >= len(data) {
		return i
	}
	switch data[i] {
	case '{', '[':
		return d.ends[sort.SearchInts(d.starts, i)]
	case '"':
		return jsonStringEnd(data, i)
	}
	j := i
	for j < len(data) {
		switch data[j] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return j
		}
		j++
	}
	return j
}

// raw returns the document's bytes at sp. They belong to the caller's buffer:
// anything that outlives the decode is taken through keep or copyOf instead.
func (d *jsonDoc) raw(sp jsonSpan) []byte { return d.data[sp.start:sp.end] }

// isNull reports whether the value at sp is a JSON null.
func (d *jsonDoc) isNull(sp jsonSpan) bool { return d.data[sp.start] == 'n' }

// keep returns the bytes at sp as a slice of the document's own copy, made the
// first time anything is kept. The slice's capacity ends where the value does,
// so an append to it reallocates rather than writing over what follows.
//
// Only unexported fields hold what keep returns, and every method that hands
// such bytes to a caller hands a copy of them: the copy is shared by every value
// this document decoded into, so no caller may be given a way to write to it.
// An exported field is given copyOf instead.
//
//go:noinline
func (d *jsonDoc) keep(sp jsonSpan) json.RawMessage {
	if d.own == nil {
		d.own = append([]byte(nil), d.data...)
	}
	return json.RawMessage(d.own[sp.start:sp.end:sp.end])
}

// copyOf returns the bytes at sp in a buffer of their own, for a value a caller
// can reach and write to: an exported json.RawMessage, which encoding/json
// itself always copies, so that keeping one does not keep the document.
//
// Except while a union is trying its branches (see trial). Every branch is
// decoded, and a branch that holds the value's members as raw JSON -- one
// declaring none of them -- would copy them, and with them everything they
// hold; the branch that describes the members decodes the same bytes into the
// next level, whose own union does the same. At every level of a recursive
// document that is a copy of the rest of it: quadratic in the depth. A trial
// takes a view of the document's own copy instead, which costs nothing; the
// branch that is kept keeps the view, and with it the document.
//
//go:noinline
func (d *jsonDoc) copyOf(sp jsonSpan) json.RawMessage {
	if d.trial > 0 || d.views > 0 {
		return d.keep(sp)
	}
	return append(json.RawMessage(nil), d.data[sp.start:sp.end]...)
}

// kindOf names the JSON kind of the value at sp the way encoding/json names it in
// an UnmarshalTypeError.
//
//go:noinline
func (d *jsonDoc) kindOf(sp jsonSpan) string {
	switch d.data[sp.start] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	}
	return "number"
}

// Iter walks the members of an object, or the elements of an array, in the
// order the document writes them.
type Iter struct {
	d    *jsonDoc
	pos  int
	stop int
}

// iter starts a walk over the object or array at sp.
//
//go:noinline
func (d *jsonDoc) iter(sp jsonSpan) jsonIter {
	return jsonIter{d: d, pos: sp.start + 1, stop: sp.end - 1}
}

// member returns the next member of an object: its key, read exactly as
// encoding/json reads one, and the span of its value.
func (it *jsonIter) member() (string, jsonSpan, bool) {
	data := it.d.data
	i := jsonSkipSpace(data, it.pos)
	if i >= it.stop {
		return "", jsonSpan{}, false
	}
	if data[i] == ',' {
		i = jsonSkipSpace(data, i+1)
	}
	keyEnd := jsonStringEnd(data, i)
	key := jsonKey(data[i:keyEnd])
	i = jsonSkipSpace(data, jsonSkipSpace(data, keyEnd)+1)
	end := it.d.valueEnd(i)
	it.pos = end
	return key, jsonSpan{i, end}, true
}

// elem returns the span of the next element of an array.
func (it *jsonIter) elem() (jsonSpan, bool) {
	data := it.d.data
	i := jsonSkipSpace(data, it.pos)
	if i >= it.stop {
		return jsonSpan{}, false
	}
	if data[i] == ',' {
		i = jsonSkipSpace(data, i+1)
	}
	end := it.d.valueEnd(i)
	it.pos = end
	return jsonSpan{i, end}, true
}

// jsonKey reads an object key. A key of plain ASCII with no escape is its own
// bytes; anything else is handed to encoding/json, so that an escape, a
// surrogate pair and a byte that is not UTF-8 read exactly as they always did.
func jsonKey(quoted []byte) string {
	inner := quoted[1 : len(quoted)-1]
	for _, c := range inner {
		if c == '\\' || c >= utf8.RuneSelf {
			var s string
			if json.Unmarshal(quoted, &s) != nil {
				return string(inner)
			}
			return s
		}
	}
	return string(inner)
}

// jsonPlainString reads a JSON string literal that holds plain ASCII text and no
// escape, and reports false for any other value.
func jsonPlainString(b []byte) (string, bool) {
	if len(b) < 2 || b[0] != '"' {
		return "", false
	}
	inner := b[1 : len(b)-1]
	for _, c := range inner {
		if c == '\\' || c >= utf8.RuneSelf {
			return "", false
		}
	}
	return string(inner), true
}
