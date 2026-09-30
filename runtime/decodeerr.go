package runtime

import (
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"sort"
	"strings"
)

// The decoders below are what a member refusing at decode time is traced to the
// position at fault by, and in whose words.
//
// A struct decodes each declared member on its own and in the order the schema
// declares them (see jsonDoc), so the member that refuses is known, and its
// refusal is joined to that member's name by the same rule every other message
// is joined by (see jsonPathError). What these add is the position *inside* a
// member: an array or a map held in a member is opened up and its elements put
// to their own decode, so the element at fault is named by its index or its key
// -- the step encoding/json's own message has never carried. A caller with a
// 200-element array learns nothing from "Go struct field .Alias.items". Issue
// #282.
//
// A member holding nothing this package decodes for itself -- scalars, and
// containers of them -- is decoded whole by encoding/json, which is the fast
// route, and put to these only once that decode has failed; see ProbeLeaf.
// A member that does hold a type of this package is opened up as it is decoded,
// by jsonProbeSlice and jsonProbeMap, and never decoded twice.

// DecodeValue is the decode of a position that holds a T, which is what the
// member decode of a scalar, a named type or a pointer to either is made of.
func DecodeValue[T any](data []byte) error {
	var v T
	return jsonDecodeRefusal(json.Unmarshal(data, &v))
}

// DecodeItems is the decode of a position that holds an array, run one
// element at a time so that the element at fault is named by its index. The
// index is what encoding/json's own message has never carried: a caller with a
// 200-element array learns nothing from "Go struct field .Alias.items".
func DecodeItems(elem func([]byte) error) func([]byte) error {
	return func(data []byte) error {
		var elems []json.RawMessage
		if err := json.Unmarshal(data, &elems); err != nil {
			return jsonDecodeRefusal(err)
		}
		for i, e := range elems {
			if err := elem(e); err != nil {
				return jsonElemPathf(err, "[%d]", i)
			}
		}
		return nil
	}
}

// DecodeValues is DecodeItems for a position that holds a map, naming
// the key at fault. Keys are visited in order for the reason checkJSONNullsAt
// sorts them: range order over a map is unspecified, and a document carrying two
// bad values would otherwise fail differently from one run to the next.
func DecodeValues(elem func([]byte) error) func([]byte) error {
	return func(data []byte) error {
		var vals map[string]json.RawMessage
		if err := json.Unmarshal(data, &vals); err != nil {
			return jsonDecodeRefusal(err)
		}
		keys := make([]string, 0, len(vals))
		for k := range vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := elem(vals[k]); err != nil {
				return jsonElemPathf(err, "[%s]", _schemagenQuote(k))
			}
		}
		return nil
	}
}

// jsonDecodeRefusal says what has to be written between a decode refusal and the
// path of the value it was raised on, and puts a refusal encoding/json worded
// from the Go type it was filling into the words the schema is written in.
//
// A message this file built already carries the answer and is passed through
// untouched -- that is what tells a nested type's path (`b.c: ...`, joined with
// a ".") from a sentence about the value itself (`null is not allowed`, joined
// with a ": "). Anything else reaching here came from encoding/json or from a
// leaf's own parser, and is a sentence: an unmarked message is not a path, so
// gluing a "." in front of one invents a step the document does not have.
func jsonDecodeRefusal(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(interface{ JSONPathSeparator() string }); ok {
		return err
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if want := jsonDecodeTypeName(typeErr.Type); want != "" {
			// encoding/json writes the literal beside the kind, and only there,
			// when the token was of the kind the destination wanted and the
			// value would not go into it -- 1e400 for a number, 1.5 for an
			// integer on a draft that requires the integer token. "expected
			// number, got number 1e400" would be a mystery; what is wrong with
			// the document is the value, not its type.
			if lit, over := strings.CutPrefix(typeErr.Value, "number "); over {
				return jsonValueErrorf("value %s cannot be held as %s", lit, jsonDecodeArticled(want))
			}
			return jsonValueErrorf("expected %s, got %s", want, jsonDecodeTokenName(typeErr.Value))
		}
	}
	return jsonValueErrorf("%s", err)
}

// jsonDecodeTypeName is the JSON type a Go destination was standing for, or ""
// where nothing here can say -- in which case the refusal is reported in the
// words it arrived in rather than in words invented for it.
//
// t is the destination type, and is asked first: a Go kind cannot answer for
// the leaves this file decodes a schema value through. json.Number and its
// shadow are strings underneath and stand for a JSON number; jsonDateTime is a
// time.Time and stands for a JSON string; and jsonInteger's kind says int64,
// which is also what a schema "number" held in a float64 would have to be told
// apart from. They are told by the type itself, not by its name: a type
// generated from a schema may be called Integer or Number, and the packages it
// is generated into all share this one.
//
// The kind answers for everything else, including every type generated from the
// schema, which is why there is no list of those here to fall out of step with
// the ones the generator emits.
func jsonDecodeTypeName(t reflect.Type) string {
	switch t {
	case jsonNumberType:
		return "number"
	case jsonTimeType, jsonAddrType:
		return "string"
	}
	// A container of a shadow is told as the shadow is. The helpers this replaced
	// were generated into each package and told the shadows by the last element
	// of the destination's printed name, and the name of []jsonNumber or
	// map[string]jsonInteger ends in the shadow's, so a refusal raised on the
	// container -- a token that was not an array, for a []jsonNumber -- was
	// worded for the element. It is kept as it was worded, so that moving the
	// helpers into a module changes nothing a caller can observe; a caller reading
	// "expected number, got number" is being told about the element and not the
	// token, and correcting it is a change of its own.
	elem := t
	for {
		switch elem.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map, reflect.Pointer:
			elem = elem.Elem()
			continue
		}
		break
	}
	switch elem {
	case jsonIntegerShadowType:
		return "integer"
	case jsonNumberShadowType:
		return "number"
	case jsonDateTimeShadowType:
		return "string"
	}
	switch t.Kind().String() {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return "integer"
	case "float32", "float64":
		return "number"
	case "slice", "array":
		return "array"
	case "map", "struct":
		return "object"
	}
	return ""
}

// jsonDecodeArticled writes a JSON type name with the article that goes in front
// of it, for the one message that reads as a sentence about a value.
func jsonDecodeArticled(name string) string {
	if name == "integer" {
		return "an integer"
	}
	return "a " + name
}

// jsonDecodeTokenName is the JSON kind encoding/json saw, in the vocabulary the
// schema is written in: JSON Schema spells the type "boolean". Every other
// spelling encoding/json uses -- "string", "number", "array", "object", "null",
// and the "number <literal>" it writes for a value no destination holds -- is
// already the schema's own.
func jsonDecodeTokenName(value string) string {
	if value == "bool" {
		return "boolean"
	}
	return value
}

// The destinations jsonDecodeTypeName tells apart by identity.
var (
	jsonAddrType           = reflect.TypeFor[netip.Addr]()
	jsonIntegerShadowType  = reflect.TypeFor[jsonInteger]()
	jsonNumberShadowType   = reflect.TypeFor[jsonNumber]()
	jsonDateTimeShadowType = reflect.TypeFor[jsonDateTime]()
)
