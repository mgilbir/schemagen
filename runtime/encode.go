package runtime

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
)

// jsonEnc appends the JSON encoding of a value of T to b, as encoding/json
// would write the value, and returns the extended buffer.
//
// It is the encoding half of what jsonAt is to decoding. Every generated type
// that holds another of this package's types has an appendJSON method of this
// shape, and the helpers below compose them, so a value is written into one
// buffer in one pass however deeply its types nest. Each type's MarshalJSON
// used to hand its members to encoding/json, which checked and compacted every
// member type's own MarshalJSON output -- the member's whole subtree, at every
// level -- and a type with members written by hand then parsed its output back
// into a map and marshalled that again: a document nested 8,000 levels deep
// took four and a half seconds to write back.
type Enc[T any] func(T, []byte) ([]byte, error)

// AppendLeaf appends encoding/json's encoding of v, for a value holding
// nothing this package writes itself: a scalar, a leaf type, a container of
// them. What encoding/json writes for it on its own is what it writes for it as
// a member, so the bytes are the ones the whole value always had.
func AppendLeaf[T any](v T, b []byte) ([]byte, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return b, err
	}
	return append(b, out...), nil
}

// jsonLeafOmitEmpty and jsonLeafOmitZero are AppendLeaf for a member tagged
// ",omitempty" or ",omitzero": the member's encoding, or omit where
// encoding/json would leave the member out. Both answers are encoding/json's
// own, read off a one-member struct carrying the same tag -- which values count
// as empty or zero is its rule, and it has changed between releases.
func jsonLeafOmitEmpty[T any](v T) ([]byte, bool, error) {
	out, err := json.Marshal(struct {
		V T `json:"v,omitempty"`
	}{v})
	if err != nil {
		return nil, false, err
	}
	if len(out) == 2 {
		return nil, true, nil
	}
	return out[len(`{"v":`) : len(out)-1], false, nil
}

func jsonLeafOmitZero[T any](v T) ([]byte, bool, error) {
	out, err := json.Marshal(struct {
		V T `json:"v,omitzero"`
	}{v})
	if err != nil {
		return nil, false, err
	}
	if len(out) == 2 {
		return nil, true, nil
	}
	return out[len(`{"v":`) : len(out)-1], false, nil
}

// jsonIsEmpty and jsonIsZero are encoding/json's omitempty and omitzero rules
// for a value holding this package's types -- a struct, or a pointer, slice or
// map of one -- which they answer without encoding it: the leaf probes above
// would encode the whole subtree to learn whether to write it. For these kinds
// encoding/json's rules are fixed: a nil pointer, a nil or empty slice or map is
// empty and a struct never is; a nil one is zero, and a struct is zero when its
// IsZero says so or, without one, when every member is.
func jsonIsEmpty[T any](v T) bool {
	rv := reflect.ValueOf(&v).Elem()
	switch rv.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

func jsonIsZero[T any](v T) bool {
	rv := reflect.ValueOf(&v).Elem()
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		if rv.IsNil() {
			return true
		}
	}
	if z, ok := any(v).(interface{ IsZero() bool }); ok {
		return z.IsZero()
	}
	return rv.IsZero()
}

// jsonAppendKey appends an object key and its colon, spelled as encoding/json
// spells a key.
func jsonAppendKey(b []byte, key string) []byte {
	return append(b, jsonKeyText(key)...)
}

// jsonKeyText is a key and its colon as jsonAppendKey writes them. A struct's
// own member names are spelled once, when the package is initialised.
func jsonKeyText(key string) string {
	out, _ := json.Marshal(key)
	return string(out) + ":"
}

// jsonEncPtr, jsonEncSlice and jsonEncMap write a pointer, a slice and a map
// the way encoding/json does -- null for a nil one, a map's keys sorted -- with
// enc for what they hold. Their type arguments are written out at every call,
// for the reason the decode helpers' are.
//
//go:noinline
func jsonEncPtr[P ~*E, E any](v P, b []byte, enc Enc[E]) ([]byte, error) {
	if v == nil {
		return append(b, "null"...), nil
	}
	return enc(*v, b)
}

func jsonEncSlice[S ~[]E, E any](v S, b []byte, enc Enc[E]) ([]byte, error) {
	if v == nil {
		return append(b, "null"...), nil
	}
	b = append(b, '[')
	for i := range v {
		if i > 0 {
			b = append(b, ',')
		}
		var err error
		if b, err = enc(v[i], b); err != nil {
			return b, err
		}
	}
	return append(b, ']'), nil
}

func jsonEncMap[M ~map[string]V, V any](v M, b []byte, enc Enc[V]) ([]byte, error) {
	if v == nil {
		return append(b, "null"...), nil
	}
	b = append(b, '{')
	for i, k := range jsonSortedKeys(v) {
		if i > 0 {
			b = append(b, ',')
		}
		b = jsonAppendKey(b, k)
		var err error
		if b, err = enc(v[k], b); err != nil {
			return b, err
		}
	}
	return append(b, '}'), nil
}

// jsonSortedKeys is a map's keys in the order encoding/json writes them.
func jsonSortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// jsonEncMarshaler writes a value of one of this package's types that has a
// MarshalJSON, reporting a failure the way encoding/json reports the failure of
// a MarshalJSON it called: "json: error calling MarshalJSON for type T: ...".
// viaPointer says the value was reached through a pointer, which encoding/json
// names the type by.
//
//go:noinline
func jsonEncMarshaler[T any](v T, b []byte, enc Enc[T], viaPointer bool) ([]byte, error) {
	out, err := enc(v, b)
	if err != nil {
		return b, jsonMarshalerErr{typ: reflect.TypeFor[T](), ptr: viaPointer || jsonMarshalerNamesPointer, err: err}
	}
	return out, nil
}

// jsonMarshalerErrFor is jsonEncMarshaler's refusal for a value of the type ptr
// points to. A position holding one of this package's types writes it through
// its appendJSON directly and asks for this only when that fails: a generic
// helper instantiated over each struct type is compiled once per struct, which
// in a package of many types was a large part of its compile time.
//
//go:noinline
func jsonMarshalerErrFor(err error, ptr any, viaPointer bool) error {
	return jsonMarshalerErr{typ: reflect.TypeOf(ptr).Elem(), ptr: viaPointer || jsonMarshalerNamesPointer, err: err}
}

// jsonMarshalerNamesPointer says whether the encoding/json this program links
// names a type whose MarshalJSON failed by its pointer even where the value was
// not reached through one. The encoding/json Go 1.27 ships does; the one before
// it named the value's own type. Asked of encoding/json itself, once.
var jsonMarshalerNamesPointer = func() bool {
	_, err := json.Marshal(struct{ V jsonMarshalProbe }{})
	var me *json.MarshalerError
	return errors.As(err, &me) && me.Type.Kind() == reflect.Pointer
}()

// jsonMarshalProbe is the value jsonMarshalerNamesPointer asks about.
type jsonMarshalProbe struct{}

func (jsonMarshalProbe) MarshalJSON() ([]byte, error) { return nil, errors.New("probe") }

// jsonMarshalerErr is a json.MarshalerError whose text is written once, when it
// is asked for: a failure at the bottom of a document nested d levels deep puts
// d prefixes in front of it, and each level writing out its own copy of the text
// below it cost the depth squared. errors.As finds the *json.MarshalerError it
// stands for.
type jsonMarshalerErr struct {
	err error
	typ reflect.Type
	ptr bool
}

func (e jsonMarshalerErr) Error() string {
	var b []byte
	var cur error = e
	for {
		m, ok := cur.(jsonMarshalerErr)
		if !ok {
			return string(append(b, cur.Error()...))
		}
		b = append(b, "json: error calling MarshalJSON for type "...)
		b = append(b, m.typeName()...)
		b = append(b, ": "...)
		cur = m.err
	}
}

func (e jsonMarshalerErr) typeName() string {
	if e.ptr {
		return reflect.PointerTo(e.typ).String()
	}
	return e.typ.String()
}

func (e jsonMarshalerErr) Unwrap() error { return e.err }

func (e jsonMarshalerErr) As(target any) bool {
	t, ok := target.(**json.MarshalerError)
	if !ok {
		return false
	}
	typ := e.typ
	if e.ptr {
		typ = reflect.PointerTo(typ)
	}
	*t = &json.MarshalerError{Type: typ, Err: e.err}
	return true
}

// jsonMember is one member of an object whose members are gathered before any
// is written, because they are written in key order (see jsonObj): its key, and
// either its encoding, or the raw bytes a caller holds for it, or the index of
// the member the value's appendMemberJSON writes -- the members that hold this
// package's types, which are written straight into the output when their turn
// comes, never into a buffer of their own that would then be copied.
type jsonMember struct {
	key  string
	val  []byte
	idx  int
	raw  bool
	gone bool
}

// MemberEnc writes a deferred member: the value's appendMemberJSON, told the
// member's index and key -- the key for an additionalProperties value, which
// all share one index.
type MemberEnc func(idx int, key string, b []byte) ([]byte, error)

// Obj is an object written the way encoding/json writes a map: its members
// in key order, a member set twice holding the last value set.
type Obj struct {
	at map[string]int
	ms []jsonMember
}

func (o *jsonObj) find(key string) int {
	if o.at != nil {
		if i, ok := o.at[key]; ok {
			return i
		}
		return -1
	}
	for i := range o.ms {
		if o.ms[i].key == key {
			return i
		}
	}
	return -1
}

func (o *jsonObj) set(m jsonMember) {
	if i := o.find(m.key); i >= 0 {
		o.ms[i] = m
		return
	}
	o.ms = append(o.ms, m)
	if o.at == nil && len(o.ms) > 16 {
		o.at = make(map[string]int, 2*len(o.ms))
		for i := range o.ms {
			o.at[o.ms[i].key] = i
		}
	} else if o.at != nil {
		o.at[m.key] = len(o.ms) - 1
	}
}

// encoded sets a member to its encoding.
func (o *jsonObj) encoded(key string, val []byte) {
	o.set(jsonMember{key: key, val: val, idx: -1})
}

// held sets a member to raw JSON a caller holds.
//
//go:noinline
func (o *jsonObj) held(key string, raw []byte) {
	o.set(jsonMember{key: key, val: raw, idx: -1, raw: true})
}

// deferred sets a member to the one appendMemberJSON writes as idx.
//
//go:noinline
func (o *jsonObj) deferred(key string, idx int) {
	o.set(jsonMember{key: key, idx: idx})
}

func (o *jsonObj) del(key string) {
	if i := o.find(key); i >= 0 {
		o.ms[i].gone = true
	}
}

// memberBytes is the member's bytes as they stand in the object: a deferred member is
// written out for the asking, and the member now holds them.
func (o *jsonObj) memberBytes(key string, member jsonMemberEnc) ([]byte, bool, error) {
	i := o.find(key)
	if i < 0 || o.ms[i].gone {
		return nil, false, nil
	}
	m := &o.ms[i]
	if m.idx >= 0 {
		v, err := member(m.idx, m.key, nil)
		if err != nil {
			return nil, true, err
		}
		m.val, m.idx = v, -1
	}
	return m.val, true, nil
}

// write appends the object: its members in key order, each written the way
// encoding/json writes a member of a map[string]json.RawMessage.
func (o *jsonObj) write(b []byte, member jsonMemberEnc) ([]byte, error) {
	live := make([]*jsonMember, 0, len(o.ms))
	for i := range o.ms {
		if !o.ms[i].gone {
			live = append(live, &o.ms[i])
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].key < live[j].key })
	b = append(b, '{')
	for i, m := range live {
		if i > 0 {
			b = append(b, ',')
		}
		b = jsonAppendKey(b, m.key)
		var err error
		switch {
		case m.idx >= 0:
			b, err = member(m.idx, m.key, b)
		case m.raw:
			b, err = AppendLeaf(json.RawMessage(m.val), b)
		default:
			b = append(b, m.val...)
		}
		if err != nil {
			return b, err
		}
	}
	return append(b, '}'), nil
}
