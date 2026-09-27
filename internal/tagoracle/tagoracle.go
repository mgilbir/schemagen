// Package tagoracle asks encoding/json, by experiment, whether a struct tag
// carries a JSON member name. It is test machinery: pkg/generator's tag tests
// use it in process, and ./probe runs it in a separate build so the same
// question can be put to an encoding/json the test binary was not built with.
//
// That second build is the point. Generated code is compiled by the caller,
// with whatever Go the caller has, and Go ships two implementations of
// encoding/json behind GOEXPERIMENT=jsonv2: the original, the default through
// Go 1.26, and one backed by encoding/json/v2, the default from Go 1.27 and
// available as an experiment since Go 1.25. They do not agree on which tag
// names are valid -- the v2-backed one accepts names the original silently
// discards, "🎉" among them -- so a name is only safe to put in a tag when
// *every* implementation a caller might build with carries it. A test binary
// is built with one of the two; ./probe is how it asks the other.
package tagoracle

import (
	"encoding/json"
	"reflect"
)

// Carries reports what encoding/json does with the tag the emitter would write
// for this property name: true only when, for every one of spellings (the tag
// bodies the struct template can put after the name), marshalling writes
// exactly this key and unmarshalling reads exactly this key back.
//
// It is deliberately an experiment rather than a re-implementation. The whole
// defect behind issues #246 and #247 was a hand-written model of the tag grammar
// that had drifted from the parser, and a second hand-written model would only
// move the drift somewhere else.
//
// The struct is built with reflect.StructOf, which models everything below
// encoding/json but not the Go source layer above it: a backtick would close the
// raw string literal the tag is written in, and the scanner drops a carriage
// return from one. Neither is visible here; the generator's predicate refuses
// both on its own, and its tests say so separately.
//
// The field is called "F" -- exported -- on purpose, and that is the boundary of
// what this measures: whether a *tag* can carry a name, not whether the field
// the emitter mints for it can be serialized at all (a Go field named 日本語 is
// unexported, and no tag helps it).
func Carries(jsonName string, spellings []string) bool {
	for _, opts := range spellings {
		typ := reflect.StructOf([]reflect.StructField{{
			Name: "F",
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(`json:"` + jsonName + opts + `"`),
		}})

		// Encode: the one key written must be this name.
		v := reflect.New(typ).Elem()
		v.Field(0).SetString("written")
		out, err := json.Marshal(v.Interface())
		if err != nil {
			return false
		}
		var got map[string]string
		if err := json.Unmarshal(out, &got); err != nil {
			return false
		}
		if len(got) != 1 || got[jsonName] != "written" {
			return false
		}

		// Decode: this name must reach the field.
		in, err := json.Marshal(map[string]string{jsonName: "read"})
		if err != nil {
			return false
		}
		p := reflect.New(typ)
		if err := json.Unmarshal(in, p.Interface()); err != nil {
			return false
		}
		if p.Elem().Field(0).String() != "read" {
			return false
		}
	}
	return true
}

// Request is what ./probe reads on stdin, and Response what it writes.
type Request struct {
	Names     []string `json:"names"`
	Spellings []string `json:"spellings"`
}

// Response answers a Request name by name, and says which implementation
// answered. Names echoes the names as the probe received them, so a name the
// JSON transport itself altered is a failure rather than a question answered
// about a different string.
type Response struct {
	JSONv2  bool     `json:"jsonv2"`
	Names   []string `json:"names"`
	Carried []bool   `json:"carried"`
}
