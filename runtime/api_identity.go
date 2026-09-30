package runtime

import "encoding/json"

// The identity of a JSON value: what uniqueItems, const and enum compare values
// by, read off the value as it is held rather than off an encoding of it, and
// the tree a match is confirmed on. A type schemagen generates reads its own by
// SchemagenJSONIdentity, and the helpers here read everything else by its Go
// kind.

// The kinds an identity is tagged with, and that KindAt and KindAny report.
const (
	IDNullKind   = jsonIDNullKind
	IDTrueKind   = jsonIDTrueKind
	IDFalseKind  = jsonIDFalseKind
	IDStringKind = jsonIDStringKind
	IDNumberKind = jsonIDNumberKind
	IDArrayKind  = jsonIDArrayKind
	IDObjectKind = jsonIDObjectKind
)

// NewTreeValidation is a Validation that reads trees rather than identities:
// the value as encoding/json decodes the JSON it writes into an any, read off
// the value by the same rules.
func NewTreeValidation() *Validation { return &jsonValidation{tree: true} }

// Reading reports whether m reads trees.
func (m *Validation) Reading() bool { return m.reading() }

// Push leaves t, a tree just read, for the caller to take.
func (m *Validation) Push(t any) { m.push(t) }

// Pop takes the tree the last read left.
func (m *Validation) Pop() any { return m.pop() }

// NewIDObj is an IDObj for m: the identity of the object a struct gathers its
// members into, read by the rules Obj writes them by.
func NewIDObj(m *Validation) IDObj { return jsonIDObj{m: m} }

// Computed sets the member key to the identity just read -- and, reading trees,
// to the tree that reading left.
func (o *IDObj) Computed(key string, id ID) { o.computed(key, id) }

// Held sets the member key to raw JSON the value holds.
func (o *IDObj) Held(key string, raw []byte) { o.idHeld(key, raw) }

// Deferred sets the member key to be read by the member function Of is given,
// under index idx, once the object is complete.
func (o *IDObj) Deferred(key string, idx int) { o.idDeferred(key, idx) }

// Del takes the member key out, as though it was never set.
func (o *IDObj) Del(key string) { o.idDel(key) }

// Nulled sets the member key to null.
func (o *IDObj) Nulled(key string) { o.nulled(key) }

// Value is the tree of the member key, whether it is there, and whether it is
// raw JSON of no bytes; m reads trees.
func (o *IDObj) Value(key string, member IDMemberFunc, m *Validation) (any, bool, bool, error) {
	return o.idValue(key, member, m)
}

// Of is the object's identity, its deferred and raw members read now.
func (o *IDObj) Of(member IDMemberFunc, m *Validation) (ID, error) { return o.idOf(member, m) }

// StripWriteOnly takes out of the members already set the locations rules mark
// writeOnly, below the members themselves.
func (o *IDObj) StripWriteOnly(rules []AccessRule, member IDMemberFunc, m *Validation) error {
	return o.idStripWriteOnly(rules, member, m)
}

// NewIDObject is an IDObject for m: the identity of an object written member by
// member, summed over its members.
func NewIDObject(m *Validation) IDObject { return jsonIDObject{m: m} }

// Add adds the member key with the identity val.
func (o *IDObject) Add(key string, val ID) { o.add(key, val) }

// ID is the identity of the object.
func (o *IDObject) ID() ID { return o.id() }

// IDNull is the identity of null, or its tree.
func IDNull(m *Validation) (ID, error) { return jsonIDNull(m) }

// IDAny is the identity of a value held as an any -- what encoding/json decodes
// the JSON it writes for the value into -- read off the value.
func IDAny(v any, m *Validation) (ID, error) { return jsonIDAny(v, m) }

// IDRawIn is the identity of raw JSON, or its tree.
func IDRawIn(b []byte, m *Validation) (ID, error) { return jsonIDRawIn(b, m) }

// IDNumberIn is the identity of a number kept as its literal, or its tree.
func IDNumberIn(n json.Number, m *Validation) (ID, error) { return jsonIDNumberIn(n, m) }

// IDPtr is the identity of a pointer: null for a nil one, and the identity of
// what it points to otherwise.
func IDPtr[P ~*E, E any](p P, m *Validation, f Identify[E]) (ID, error) {
	return jsonIDPtr[P, E](p, m, f)
}

// IDSlice is the identity of a slice, as an array; null for a nil one.
func IDSlice[S ~[]E, E any](s S, m *Validation, f Identify[E]) (ID, error) {
	return jsonIDSlice[S, E](s, m, f)
}

// IDSliceKept is IDSlice for an array whose uniqueItems check reads its
// elements' identities back, and so keeps them.
func IDSliceKept[S ~[]E, E any](s S, m *Validation, f Identify[E]) (ID, error) {
	return jsonIDSliceKept[S, E](s, m, f)
}

// IDMap is the identity of a map, as an object; null for a nil one.
func IDMap[M ~map[string]V, V any](mp M, m *Validation, f Identify[V]) (ID, error) {
	return jsonIDMap[M, V](mp, m, f)
}

// IDsOf is the identities of the elements of s, and, when reading one fails, the
// index of the element it failed on.
func IDsOf[S ~[]E, E any](s S, m *Validation, f Identify[E]) ([]ID, int, error) {
	return jsonIDsOf[S, E](s, m, f)
}

// FirstDuplicate is the index of the first element of s with the identity of an
// earlier one, confirmed on the elements' trees, and -1 if there is none.
func FirstDuplicate[S ~[]E, E any](s S, ids []ID, f Identify[E]) int {
	return jsonFirstDuplicate[S, E](s, ids, f)
}

// MarshalError names, in the words of a check that has already read the value, a
// failure to write *p as JSON.
func MarshalError[T any](p *T, err error) error { return jsonMarshalError(p, err) }

// MarshalText is the value at p as encoding/json writes it, for the message of a
// check that has already refused it.
func MarshalText[T any](p *T) string { return jsonMarshalText(p) }

// ConstOf is the const or enum the texts write, read once per process. floats
// says a number is read as a float64 rather than as the literal the schema
// wrote.
func ConstOf(floats bool, texts ...string) *Const { return jsonConstOf(floats, texts...) }

// IsWrittenAs reports whether c holds exactly one member and t, a tree, is
// written as it is.
func (c *Const) IsWrittenAs(t any) bool {
	return len(c.trees) == 1 && jsonTreeWritten(t, c.trees[0])
}

// MatchesConst reports whether *p is a member of c, read by f.
func MatchesConst[T any](p *T, f Identify[T], c *Const) (bool, error) {
	return jsonMatchesConst[T](p, f, c)
}

// MatchesConstAt is MatchesConst for a value read by its Go kind.
func MatchesConstAt[T any](p *T, c *Const) (bool, error) { return jsonMatchesConstAt[T](p, c) }

// MatchesConstRaw reports whether the raw JSON b is a member of c.
func MatchesConstRaw(b []byte, c *Const) (bool, error) { return jsonMatchesConstRaw(b, c) }

// TreeOf is the tree of the value at p, read by f.
func TreeOf[T any](p *T, f Identify[T]) (any, error) { return jsonTreeOf[T](p, f) }

// TreeRaw is the tree of raw JSON: what encoding/json decodes it into an any,
// every number a json.Number, or a float64 when floats is set.
func TreeRaw(b []byte, floats bool) (any, error) { return jsonTreeRaw(b, floats) }

// TreeView is v as the runtime evaluator reads a value: v itself where it is
// already what a decode into an any produces, and its tree wherever it, or
// anything it holds, is some other Go value.
func TreeView(v any) (any, error) { return jsonTreeView(v) }

// TreeWritten reports whether two trees are written as the same JSON.
func TreeWritten(a, b any) bool { return jsonTreeWritten(a, b) }

// KindAt is the kind of the value at p (one of the ID...Kind constants, or 0 for
// a value that is none of them) and, for a string or a number, its text.
func KindAt[T any](p *T) (byte, string) { return jsonKindAt[T](p) }

// KindAny is KindAt for a value held as an any.
func KindAny(v any) (byte, string) { return jsonKindAny(v) }

// OmitEmptyAt reports whether encoding/json would leave the value at p out
// under ",omitempty", by the rules it keeps for its kind.
func OmitEmptyAt[T any](p *T) bool { return jsonOmitEmptyAt[T](p) }

// OmitZeroAt is OmitEmptyAt for ",omitzero".
func OmitZeroAt[T any](p *T) bool { return jsonOmitZeroAt[T](p) }
