package runtime

import "encoding/json"

// The in-place decoder generated types decode through. A generated type opens
// the document once (OpenDoc), decodes the value at a Span of it, and hands each
// member's Span to the member's own type; the helpers here compose those steps
// for the containers and the positions holding nothing schemagen generated.

// OpenDoc checks that data is one JSON value and indexes it, in one pass. A
// document that is not JSON is refused with encoding/json's own words for it.
func OpenDoc(data []byte) (*Doc, Span, error) { return jsonOpenDoc(data) }

// Finish is what a decode opened by OpenDoc returns: the refusal recorded on
// the document, if one was, and otherwise err. It is also where the document
// lets go of the caller's buffer.
func (d *Doc) Finish(err error) error { return d.finish(err) }

// IsNull reports whether the value at sp is a JSON null.
func (d *Doc) IsNull(sp Span) bool { return d.isNull(sp) }

// First is the first byte of the value at sp: '{' for an object, '[' for an
// array, and so on.
func (d *Doc) First(sp Span) byte { return d.data[sp.start] }

// Raw is the bytes of the value at sp, a view of the document.
func (d *Doc) Raw(sp Span) []byte { return d.raw(sp) }

// Iter walks the members of the object, or the elements of the array, at sp.
func (d *Doc) Iter(sp Span) Iter { return d.iter(sp) }

// Keep is the bytes of the value at sp, as a value that outlives the decode:
// cut from the document's own copy, which every value the document decoded into
// shares, and never from the caller's buffer.
func (d *Doc) Keep(sp Span) json.RawMessage { return d.keep(sp) }

// CopyOf is the bytes of the value at sp for a member a caller can reach: a
// copy of its own, or, inside a union branch's trial, a view.
func (d *Doc) CopyOf(sp Span) json.RawMessage { return d.copyOf(sp) }

// BeginTrial and EndTrial bracket a union branch trial. A branch that holds the
// value's members as raw JSON takes views of the document during one rather
// than copies of them.
func (d *Doc) BeginTrial() { d.trial++ }

// EndTrial ends the trial BeginTrial began.
func (d *Doc) EndTrial() { d.trial-- }

// Refused reports whether a refusal has been recorded on the document (see
// Refuse).
func (d *Doc) Refused() bool { return d.refusal != nil }

// Refuse records err on the document as --strict-read-write's refusal of it,
// returned by Finish once the whole value is decoded. Recorded rather than
// returned so that nothing stops decoding when one is found: a Validate check
// that has to ignore it still gets the whole value to judge.
func (d *Doc) Refuse(err error) { d.refusal = err }

// Member returns the next member of an object: its key, the Span of its value,
// and false when there are none left.
func (it *Iter) Member() (string, Span, bool) { return it.member() }

// Elem returns the Span of the next element of an array, and false when there
// are none left.
func (it *Iter) Elem() (Span, bool) { return it.elem() }

// Level is the value's first level as encoding/json decodes it into an any --
// a map[string]any, a []any, a string, a number, a bool or nil -- with every
// member and element a Lazy in turn.
func (l Lazy) Level() any { return l.jsonLevel() }

// DecodeRefusal says what has to be written between a decode refusal and the
// path of the value it was raised on, and puts a refusal encoding/json worded
// from the Go type it was filling into the words the schema is written in.
func DecodeRefusal(err error) error { return jsonDecodeRefusal(err) }

// TypeErrorFor is the refusal encoding/json gives for a value of the wrong JSON
// kind decoded into the type ptr points to, built without handing the value to
// encoding/json.
func TypeErrorFor(d *Doc, sp Span, ptr any) error { return jsonTypeErrorFor(d, sp, ptr) }

// KindError is the refusal encoding/json gives a value whose JSON kind a T
// cannot hold, for a T that is a slice, a map or a struct, and nil for a value
// it can.
func KindError[T any](d *Doc, sp Span) error { return jsonKindError[T](d, sp) }

// DecodePtr decodes a value into a pointer, as encoding/json does: a null
// leaves the pointer nil, and anything else is decoded into a newly allocated
// value.
func DecodePtr[P ~*T, T any](p *P, d *Doc, sp Span, inner At[T]) error {
	return jsonDecodePtr[P, T](p, d, sp, inner)
}

// DecodeSlice decodes an array into a newly allocated slice, as encoding/json
// does.
func DecodeSlice[S ~[]E, E any](p *S, d *Doc, sp Span, elem At[E]) error {
	return jsonDecodeSlice[S, E](p, d, sp, elem)
}

// DecodeMap decodes an object into a newly allocated map, as encoding/json
// does.
func DecodeMap[M ~map[string]V, V any](p *M, d *Doc, sp Span, elem At[V]) error {
	return jsonDecodeMap[M, V](p, d, sp, elem)
}

// ProbeSlice is DecodeSlice for a member position, where a refusal is named by
// the index of the element that raised it.
func ProbeSlice[E any](p *[]E, d *Doc, sp Span, elem At[E]) error {
	return jsonProbeSlice[E](p, d, sp, elem)
}

// ProbeMap is DecodeMap for a member position, where a refusal is named by the
// key of the value that raised it.
func ProbeMap[V any](p *map[string]V, d *Doc, sp Span, elem At[V]) error {
	return jsonProbeMap[V](p, d, sp, elem)
}

// DecodeHeld decodes a value its owner holds as raw JSON into *p, the way the
// value's own UnmarshalJSON would, in place wherever raw is a view of the
// document doc.
func DecodeHeld[T any](doc *Doc, raw []byte, p *T, dec At[T]) error {
	return jsonDecodeHeld[T](doc, raw, p, dec)
}

// DecodeLazy decodes a lazily read element into *p in place, as DecodeHeld
// decodes a held value.
func DecodeLazy[T any](l Lazy, p *T, dec At[T]) error { return jsonDecodeLazy[T](l, p, dec) }

// LazyItemsOr decodes a tuple -- an array held as []any, some position of which
// is a type schemagen generated -- with dec, except where Validate decodes a
// held value: there each element is a Lazy.
func LazyItemsOr[S ~[]any](p *S, d *Doc, sp Span, dec At[S]) error {
	return jsonLazyItemsOr[S](p, d, sp, dec)
}

// Held is a member its owner holds as raw JSON, for a check that reads it as
// decoded JSON, every number the literal the document wrote: read lazily where
// it is a view of the document the owner keeps, and decoded whole where it is
// not.
func Held(doc *Doc, raw []byte) (any, error) { return jsonHeld(doc, raw) }

// ReadLazily is raw JSON a value holds, read one level at a time through a
// document opened over it, with every number the literal it wrote.
func ReadLazily(raw []byte) (any, error) { return jsonReadLazily(raw) }

// Top is v with its first level read, where it is a Lazy. Anything else is
// returned as it is.
func Top(v any) any { return jsonTop(v) }

// Attempt runs one trial decode of a union branch under --strict-read-write.
func Attempt(d *Doc, f func() error) error { return jsonAttempt(d, f) }

// IsNullDocument reports whether data, a whole document, is the JSON null.
func IsNullDocument(data []byte) bool { return jsonIsNullDocument(data) }

// CheckNullsAt applies a NullRule to the value at sp, refusing a null where the
// rule forbids one. An object's members are visited in key order, so a document
// with several nulls fails the same way every time.
func CheckNullsAt(d *Doc, sp Span, rule *NullRule) error { return checkJSONNullsAt(d, sp, rule) }

// OneOfHasRequiredFields reports whether the object at sp has every one of
// fields, the test a union puts a branch to before it decodes it.
func OneOfHasRequiredFields(d *Doc, sp Span, fields ...string) bool {
	return oneofHasRequiredFields(d, sp, fields...)
}

// OneOfDiscriminatorValue extracts the string value of a discriminator property
// from the object at sp.
func OneOfDiscriminatorValue(d *Doc, sp Span, prop string) (string, error) {
	return oneofDiscriminatorValue(d, sp, prop)
}

// DecodeNumbers decodes data into *v the way encoding/json decodes into an any,
// except that every number is kept as the json.Number of the literal the
// document wrote.
func DecodeNumbers(data []byte, v *any) error { return jsonDecodeNumbers(data, v) }

// IntegerFromLiteral reads a number literal as an int64, exactly: 1, 1.0 and 1e2
// are integers, 1.5 is not, and 2^63 is not an int64.
func IntegerFromLiteral(s string) (int64, bool) { return jsonIntegerFromLiteral(s) }

// IntegerPtr rebuilds a pointer to an Integer as a pointer to the type the
// schema declares, preserving nil.
func IntegerPtr[E any, T any](p *E, f func(E) T) *T { return jsonIntegerPtr(p, f) }

// IntegerSlice rebuilds a slice of Integers as a slice of the type the schema
// declares, preserving nil.
func IntegerSlice[E any, T any](s []E, f func(E) T) []T { return jsonIntegerSlice(s, f) }

// IntegerMap rebuilds a map of Integers as a map of the type the schema
// declares, preserving nil.
func IntegerMap[E any, T any](m map[string]E, f func(E) T) map[string]T {
	return jsonIntegerMap(m, f)
}
