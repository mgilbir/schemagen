package runtime

// The one-pass encoder generated types write themselves out through. Every
// struct, and every alias over one, has an appendJSON that writes the value into
// one buffer and calls the appendJSON of what it holds directly; encoding/json
// writes only the leaves -- scalars and the types the generated code does not
// write itself -- so the bytes are the ones encoding/json would have written.

// LeafOmitEmpty is AppendLeaf for a member tagged ",omitempty": it reports
// whether encoding/json would leave the member out, and if not, its bytes.
func LeafOmitEmpty[T any](v T) ([]byte, bool, error) { return jsonLeafOmitEmpty(v) }

// LeafOmitZero is LeafOmitEmpty for a member tagged ",omitzero".
func LeafOmitZero[T any](v T) ([]byte, bool, error) { return jsonLeafOmitZero(v) }

// IsEmpty reports whether encoding/json would leave v out under ",omitempty",
// by the rules it keeps for v's kind, without writing v.
func IsEmpty[T any](v T) bool { return jsonIsEmpty(v) }

// IsZero reports whether encoding/json would leave v out under ",omitzero".
func IsZero[T any](v T) bool { return jsonIsZero(v) }

// KeyText is the key as encoding/json spells it in an object, quotes and colon
// included.
func KeyText(key string) string { return jsonKeyText(key) }

// EncPtr writes a pointer: null for a nil one, and the value's own writing
// otherwise.
func EncPtr[P ~*E, E any](v P, b []byte, enc Enc[E]) ([]byte, error) {
	return jsonEncPtr[P, E](v, b, enc)
}

// EncSlice writes a slice as an array, null for a nil one.
func EncSlice[S ~[]E, E any](v S, b []byte, enc Enc[E]) ([]byte, error) {
	return jsonEncSlice[S, E](v, b, enc)
}

// EncMap writes a map as an object with its keys in order, null for a nil one.
func EncMap[M ~map[string]V, V any](v M, b []byte, enc Enc[V]) ([]byte, error) {
	return jsonEncMap[M, V](v, b, enc)
}

// SortedKeys is the keys of m in order, for a walk whose failure is reported for
// the least key whatever order the map is ranged in.
func SortedKeys[M ~map[string]V, V any](m M) []string { return jsonSortedKeys[M, V](m) }

// MarshalerErrFor is the error encoding/json reports for a value whose
// MarshalJSON failed with err, naming the type ptr points to (and the pointer
// type when viaPointer says the value was reached through one).
func MarshalerErrFor(err error, ptr any, viaPointer bool) error {
	return jsonMarshalerErrFor(err, ptr, viaPointer)
}

// Encoded sets the member key to val, the bytes encoding/json wrote for it.
func (o *Obj) Encoded(key string, val []byte) { o.encoded(key, val) }

// Held sets the member key to raw, bytes the value holds as they were written.
func (o *Obj) Held(key string, raw []byte) { o.held(key, raw) }

// Deferred sets the member key to be written by the member function Write is
// given, under index idx, once the object is complete.
func (o *Obj) Deferred(key string, idx int) { o.deferred(key, idx) }

// Del takes the member key out, as though it was never set.
func (o *Obj) Del(key string) { o.del(key) }

// MemberBytes is the bytes the member key would be written as, whether it is
// there, and any error writing it raised.
func (o *Obj) MemberBytes(key string, member MemberEnc) ([]byte, bool, error) {
	return o.memberBytes(key, member)
}

// Write appends the object to b: its members in key order, a deferred member
// written by member when its turn comes.
func (o *Obj) Write(b []byte, member MemberEnc) ([]byte, error) { return o.write(b, member) }

// StripWriteOnly takes out of the members already set the locations rules mark
// writeOnly, the members themselves included.
func (o *Obj) StripWriteOnly(rules AccessRules, member MemberEnc) error {
	return o.stripWriteOnly(rules, member)
}
