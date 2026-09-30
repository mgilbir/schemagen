package runtime

import (
	"encoding/json"
	"reflect"
	"strconv"
)

// At decodes the value at a span of a document into *T.
//
// Every generated type that decodes in place has a decodeJSONAt method of this
// shape, and the helpers below compose them: a slice of a type is decoded by
// jsonDecodeSlice with that type's method for its elements, and so on down.
type At[T any] func(*T, *Doc, Span) error

// AtJSON decodes a value that holds nothing this package decodes in place,
// the way encoding/json decodes it -- and through encoding/json, apart from the
// commonest scalars, which are read directly where their spelling leaves
// nothing for a decoder to decide. Anything else, including every value one of
// those readings would refuse, goes to json.Unmarshal, so the result and the
// refusal are always the ones encoding/json gives.
func AtJSON[T any](p *T, d *Doc, sp Span) error {
	b := d.data[sp.start:sp.end]
	switch q := any(p).(type) {
	case *string:
		if s, ok := jsonPlainString(b); ok {
			*q = s
			return nil
		}
	case **string:
		if b[0] == 'n' {
			*q = nil
			return nil
		}
		if s, ok := jsonPlainString(b); ok {
			*q = &s
			return nil
		}
	case *bool:
		switch string(b) {
		case "true":
			*q = true
			return nil
		case "false":
			*q = false
			return nil
		}
	case **bool:
		switch string(b) {
		case "null":
			*q = nil
			return nil
		case "true", "false":
			v := b[0] == 't'
			*q = &v
			return nil
		}
	case *float64:
		if b[0] == '-' || (b[0] >= '0' && b[0] <= '9') {
			if f, err := strconv.ParseFloat(string(b), 64); err == nil {
				*q = f
				return nil
			}
		}
	case *int64:
		if b[0] == '-' || (b[0] >= '0' && b[0] <= '9') {
			if n, err := strconv.ParseInt(string(b), 10, 64); err == nil {
				*q = n
				return nil
			}
		}
	case json.Unmarshaler:
		// A type schemagen generated, or one of this package's own, that
		// decodes itself is handed the value's bytes, which is all
		// encoding/json does for one once it has checked them -- and they were
		// checked when the document was opened. Handed to json.Unmarshal
		// instead, they are checked again, which for a union branch trying an
		// object is a scan of everything the object holds, at every level.
		// Only those: encoding/json reads some types without their
		// UnmarshalJSON -- the one Go 1.27 ships decodes a time.Time itself --
		// and its reading is the one to give. What is generated is told by the
		// SchemagenGenerated method every such type carries (see jsonGenerated),
		// not by the package it is declared in: the generated types of any
		// number of packages share this one.
		if _, own := q.(jsonGenerated); own {
			return q.UnmarshalJSON(b)
		}
	}
	if t := reflect.TypeFor[T](); t.Kind() == reflect.Pointer && reflect.PointerTo(t.Elem()).Implements(jsonGeneratedType) {
		// A pointer to such a type, the same way, after encoding/json's
		// reading of a pointer: a null leaves it nil, and anything else is
		// decoded into a newly allocated value.
		v := reflect.ValueOf(p).Elem()
		if b[0] == 'n' {
			v.SetZero()
			return nil
		}
		v.Set(reflect.New(t.Elem()))
		return v.Interface().(json.Unmarshaler).UnmarshalJSON(b)
	}
	if b[0] == '{' || b[0] == '[' {
		// A container where T holds a scalar: refused as encoding/json refuses
		// it, without a scan of the container to find that out.
		if err, refused := jsonScalarRefusal[T](p, d, sp); refused {
			return err
		}
	}
	return json.Unmarshal(b, p)
}

// jsonScalarRefusal is the refusal encoding/json gives an object or an array
// decoded into a T that is a boolean, a number or a string, or a pointer to one
// -- which it allocates first, as encoding/json does. It reports false for
// every other T, which decides for itself what it holds.
func jsonScalarRefusal[T any](p *T, d *jsonDoc, sp jsonSpan) (error, bool) {
	t := reflect.TypeFor[T]()
	v := reflect.ValueOf(p).Elem()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
		if !jsonIsScalarKind(t) {
			return nil, false
		}
		v.Set(reflect.New(t))
	} else if !jsonIsScalarKind(t) {
		return nil, false
	}
	return &json.UnmarshalTypeError{Value: d.kindOf(sp), Type: t, Offset: int64(sp.start)}, true
}

// jsonIsScalarKind reports whether encoding/json reads a JSON scalar into t and
// nothing else: t has a scalar kind, and no method of its own that would decode
// a container into it.
func jsonIsScalarKind(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
	default:
		return false
	}
	pt := reflect.PointerTo(t)
	return !pt.Implements(jsonUnmarshalerType) && !pt.Implements(jsonTextUnmarshalerType) &&
		!t.Implements(jsonUnmarshalerType) && !t.Implements(jsonTextUnmarshalerType)
}

// jsonGenerated is a type that decodes itself the way this package's decoders
// do, and may be handed the bytes of a value the document has already been
// checked to hold. Every type schemagen generates with an UnmarshalJSON has the
// method, and so do this package's own decode destinations (Integer, Number and
// the others); nothing else does, so it tells a type this package can call
// directly from one encoding/json has to read.
type jsonGenerated interface {
	json.Unmarshaler
	SchemagenGenerated()
}

var (
	jsonGeneratedType       = reflect.TypeFor[jsonGenerated]()
	jsonUnmarshalerType     = reflect.TypeFor[json.Unmarshaler]()
	jsonTextUnmarshalerType = reflect.TypeFor[interface{ UnmarshalText([]byte) error }]()
)

// jsonTypeError is the refusal encoding/json gives for a value of the wrong
// JSON kind decoded into a T, built without handing the value to encoding/json
// to find that out: the value may be an object the size of the rest of the
// document, and asking would scan it.
//
//go:noinline
func jsonTypeError[T any](d *jsonDoc, sp jsonSpan) error {
	return &json.UnmarshalTypeError{Value: d.kindOf(sp), Type: reflect.TypeFor[T](), Offset: int64(sp.start)}
}

// jsonTypeErrorFor is jsonTypeError for the type ptr points to, for a struct's
// own decode: not generic, so it is compiled once rather than once per struct
// type of the package.
//
//go:noinline
func jsonTypeErrorFor(d *jsonDoc, sp jsonSpan, ptr any) error {
	return &json.UnmarshalTypeError{Value: d.kindOf(sp), Type: reflect.TypeOf(ptr).Elem(), Offset: int64(sp.start)}
}

// jsonKindError is the refusal encoding/json gives a value whose JSON kind a T
// cannot hold, for a T that is a slice, a map or a struct, and nil for a value
// it can -- and for every other T, which is left to the decode itself. A null
// is one every such T holds: it leaves the value as it is.
func jsonKindError[T any](d *jsonDoc, sp jsonSpan) error {
	c := d.data[sp.start]
	if c == 'n' {
		return nil
	}
	switch t := reflect.TypeFor[T](); t.Kind() {
	case reflect.Slice:
		// A []byte is a JSON string, in base64.
		if c != '[' && !(c == '"' && t.Elem().Kind() == reflect.Uint8) {
			return jsonTypeError[T](d, sp)
		}
	case reflect.Map, reflect.Struct:
		if c != '{' {
			return jsonTypeError[T](d, sp)
		}
	}
	return nil
}

// jsonDecodePtr decodes a value into a pointer, as encoding/json does: a null
// leaves the pointer nil without consulting the type it points to, and anything
// else is decoded into a newly allocated value.
//
//go:noinline
func jsonDecodePtr[P ~*T, T any](p *P, d *jsonDoc, sp jsonSpan, inner At[T]) error {
	if d.data[sp.start] == 'n' {
		*p = nil
		return nil
	}
	v := new(T)
	*p = v
	return inner(v, d, sp)
}

// jsonDecodeSlice decodes an array into a newly allocated slice, as
// encoding/json does: a null leaves it nil, an empty array makes it empty and
// not nil, and the first element that refuses ends the decode with that
// element's own refusal.
func jsonDecodeSlice[S ~[]E, E any](p *S, d *jsonDoc, sp jsonSpan, elem At[E]) error {
	switch d.data[sp.start] {
	case 'n':
		*p = nil
		return nil
	case '[':
	default:
		return jsonTypeError[S](d, sp)
	}
	out := S{}
	it := d.iter(sp)
	for {
		esp, ok := it.elem()
		if !ok {
			break
		}
		var e E
		if err := elem(&e, d, esp); err != nil {
			*p = out
			return err
		}
		out = append(out, e)
	}
	*p = out
	return nil
}

// jsonDecodeMap decodes an object into a newly allocated map, as encoding/json
// does: members in document order, a key written twice holding its last value,
// and the first value that refuses ending the decode with its own refusal.
func jsonDecodeMap[M ~map[string]V, V any](p *M, d *jsonDoc, sp jsonSpan, elem At[V]) error {
	switch d.data[sp.start] {
	case 'n':
		*p = nil
		return nil
	case '{':
	default:
		return jsonTypeError[M](d, sp)
	}
	out := M{}
	*p = out
	it := d.iter(sp)
	for {
		k, vsp, ok := it.member()
		if !ok {
			break
		}
		var v V
		if err := elem(&v, d, vsp); err != nil {
			return err
		}
		out[k] = v
	}
	return nil
}

// ProbeLeaf decodes a member holding nothing this package decodes in place,
// and names the position inside it that refused. The member is decoded whole
// first, which is the fast route and the one that almost always succeeds; only a
// refusal puts it to probe, the member decode that opens it up (see
// decodepath_helpers). Nothing below such a member decodes in place, so the
// second decode costs the member's size once, and only on a refusal.
func ProbeLeaf[T any](p *T, d *Doc, sp Span, probe func([]byte) error) error {
	err := AtJSON(p, d, sp)
	if err == nil {
		return nil
	}
	if perr := probe(d.raw(sp)); perr != nil {
		return perr
	}
	return jsonDecodeRefusal(err)
}

// jsonProbeSlice is jsonDecodeSlice for a member position, where a refusal is
// named by the index of the element that raised it. elem answers in the words
// the member decode gives, and is itself a probe where the element is a
// container.
func jsonProbeSlice[E any](p *[]E, d *jsonDoc, sp jsonSpan, elem At[E]) error {
	switch d.data[sp.start] {
	case 'n':
		*p = nil
		return nil
	case '[':
	default:
		return jsonDecodeRefusal(jsonTypeError[[]E](d, sp))
	}
	out := []E{}
	it := d.iter(sp)
	for i := 0; ; i++ {
		esp, ok := it.elem()
		if !ok {
			break
		}
		var e E
		if err := elem(&e, d, esp); err != nil {
			*p = out
			return jsonElemPathf(err, "[%d]", i)
		}
		out = append(out, e)
	}
	*p = out
	return nil
}

// jsonProbeMap is jsonDecodeMap for a member position, where a refusal is named
// by the key of the value that raised it. Every value is decoded; where more
// than one refuses, the least key is the one reported, for the reason
// DecodeValues sorts: a document with two faults fails the same way every
// time. A key written twice is judged by its last value only.
func jsonProbeMap[V any](p *map[string]V, d *jsonDoc, sp jsonSpan, elem At[V]) error {
	switch d.data[sp.start] {
	case 'n':
		*p = nil
		return nil
	case '{':
	default:
		return jsonDecodeRefusal(jsonTypeError[map[string]V](d, sp))
	}
	out := map[string]V{}
	*p = out
	var failed map[string]error
	it := d.iter(sp)
	for {
		k, vsp, ok := it.member()
		if !ok {
			break
		}
		var v V
		if err := elem(&v, d, vsp); err != nil {
			if failed == nil {
				failed = make(map[string]error, 1)
			}
			failed[k] = err
			continue
		}
		delete(failed, k)
		out[k] = v
	}
	if len(failed) == 0 {
		return nil
	}
	least := ""
	first := true
	// maporder: finds the least key, which no order of the loop changes.
	for k := range failed {
		if first || k < least {
			least, first = k, false
		}
	}
	return jsonElemPathf(failed[least], "[%s]", _schemagenQuote(least))
}
